<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Adapter\EngineAdapterFactory;
use Upkeep\Adapter\IssueBranch;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\MrContextResolver;
use Upkeep\Workflow\WorkflowException;

/**
 * Begin work on a drupal.org issue: an environment, and a branch to write on.
 *
 * The entry point upkeep was missing. Every other verb starts from a
 * *contribution* — a merge request, or a patch somebody has already posted —
 * which meant the half of the job where a maintainer writes the fix happened
 * somewhere else entirely: a manual clone, a manual branch, a manual push.
 * There was nothing to start from if nobody had started yet.
 *
 * The branch follows drupal.org's issue-fork convention (`<nid>-<slug>`), so
 * it is the shape drupal.org, GitLab, and this tool's own reference parsing
 * all already recognise. Push it and the merge request is linked to the issue
 * with nothing further to configure — which is what `upkeep publish` then does.
 *
 * Resumes rather than restarts: an existing branch for that issue is checked
 * out as it stands. Nothing here resets, forces, or discards, because unlike
 * the disposable mr-/patch- branches this may hold the only copy of something
 * a human wrote.
 */
#[AsCommand(
    name: 'start',
    description: 'Start (or resume) work on a drupal.org issue: provision an environment and open a branch.',
)]
final class StartCommand extends UpkeepCommand
{
    public function __construct(
        private readonly EngineAdapterFactory $engines,
        private readonly ?DrupalOrgClient $drupalClient = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->setHelp(<<<'HELP'
            Begins work on an issue nobody has contributed to yet: provisions the
            environment and opens a branch named to drupal.org's issue-fork convention,
            so the merge request that follows is linked to the issue.

              <info>cd $(upkeep start pathauto 3223746)</info>
              <info>upkeep start pathauto 3223746 --version=11</info>

            Resumes rather than restarts: an existing branch is checked out as it stands,
            and nothing here ever resets or discards.
            When the work is ready: <info>upkeep publish <module> <issue></info>.
            HELP);

        $this->addModuleArgument()
            ->addTargetCoreOption()
            ->addCockpitOption()
            ->addProjectsRootOption();

        $this->addArgument(
            'issue',
            InputArgument::REQUIRED,
            'drupal.org issue node id to work on (see `upkeep issues <module>`)',
        );
        $this->addOption(
            'branch',
            null,
            InputOption::VALUE_REQUIRED,
            'Name the branch yourself instead of deriving it from the issue title',
        );
        $this->addOption(
            'base',
            null,
            InputOption::VALUE_REQUIRED,
            'Branch to start from (default: whatever the module working copy is currently on)',
        );
    }

    /** The environment path is the payload; diagnostics stay off stdout. */
    protected function diagnosticsOnStderr(): bool
    {
        return true;
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->cockpit($input);
        $module = MrContextResolver::requireModule(
            $this->modules($cockpit),
            self::stringArgument($input, 'module'),
        );
        $coreMajor = self::targetCore($input, $module);
        $nid = self::issueNid($input);

        $io->writeln(sprintf('Resolving issue #%d via drupal.org ...', $nid));
        $drupal = $this->drupalClient ?? new DrupalOrgClient(HttpClient::create());
        $issue = $drupal->issue($nid) ?? throw new WorkflowException(sprintf(
            'drupal.org issue #%d could not be read. Check the node id (it is the number in the issue URL).',
            $nid,
        ));

        $branchName = self::stringOption($input, 'branch');
        $branch = $branchName !== null
            ? IssueBranch::named($nid, $branchName)
            : IssueBranch::forIssue($nid, $issue->title);

        $io->writeln(sprintf('Issue #%d "%s" (%s)', $issue->nid, $issue->title, $issue->status->shortLabel()));
        $io->writeln(sprintf('Target: Drupal core %s, module %s', $coreMajor, $module->name));

        $adapter = $this->engines->create(
            $cockpit,
            self::stringOption($input, 'projects-root'),
            static fn (string $line) => $io->writeln($line),
            static fn (string $line) => $io->writeln($line, OutputInterface::VERBOSITY_VERBOSE),
        );

        $io->section('Environment');
        $environment = $adapter->ensureEnv($module, $coreMajor);

        $io->section('Branch');
        // A null base means "resolve it from the working copy" — the adapter
        // owns that knowledge, and it resolves it the same way applyMr and
        // applyPatch do, so a branch started here and a contribution checked
        // out here share an origin.
        $resumed = $adapter->startWork($environment, $branch, self::stringOption($input, 'base'));

        $io->success(sprintf(
            '%s %s. Write your fix, then: upkeep check %s --working-copy, and upkeep publish %s %d',
            $resumed ? 'Resumed' : 'Started',
            $branch->name,
            $module->name,
            $module->name,
            $nid,
        ));
        $io->writeln(sprintf('<fg=gray>Issue: %s</>', $issue->url));

        $output->writeln($environment->projectPath);

        return ExitCode::OK;
    }

    /**
     * @throws WorkflowException when the argument is not a node id
     */
    private static function issueNid(InputInterface $input): int
    {
        $raw = self::stringArgument($input, 'issue');
        if (preg_match('/^\d+$/', $raw) !== 1 || (int) $raw < 1) {
            throw new WorkflowException(sprintf(
                'The <issue> argument must be a drupal.org issue node id (a positive integer), got "%s".',
                $raw,
            ));
        }

        return (int) $raw;
    }
}

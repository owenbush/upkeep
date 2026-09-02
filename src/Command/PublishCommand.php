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
use Upkeep\Adapter\GitRemote;
use Upkeep\Adapter\IssueBranch;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\IssueReference;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Project;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\MrContextResolver;
use Upkeep\Workflow\WorkflowException;

/**
 * Push the work branch for an issue and open the merge request for it.
 *
 * The step that closes the loop. Everything upkeep already does well —
 * checking, gating, the dashboard, the fast lane — begins at a merge request,
 * and until now the only way to *get* one was to leave the tool. What this
 * pushes re-enters that pipeline immediately, as an ordinary MR that
 * `dashboard`, `check` and `merge` have always known how to handle.
 *
 * **It opens a merge request; it never merges one.** Those are opposite acts:
 * proposing work for review is the thing the Drupal Association's
 * one-approval-per-merge stance exists to protect, not the thing it restricts.
 * Merging stays where it is, behind `merge --fast-lane`'s per-MR prompt.
 *
 * Re-running is safe: an MR already open for the branch is reported rather
 * than duplicated, because the useful answer to "publish this again" is a link
 * to the one that exists.
 *
 * The target defaults to the base the adapter recorded when the work started —
 * never to a tracked core major. Those are versions of Drupal ("11"), not
 * branches on a project ("2.0.x", "8.x-1.x"), and defaulting to one made every
 * publish target a branch that does not exist.
 */
#[AsCommand(
    name: 'publish',
    description: 'Push an issue\'s work branch and open a merge request for it.',
)]
final class PublishCommand extends UpkeepCommand
{
    public function __construct(
        private readonly EngineAdapterFactory $engines,
        private readonly ?GitlabClient $gitlabClient = null,
        private readonly ?DrupalOrgClient $drupalClient = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->setHelp(<<<'HELP'
            Pushes the work branch for an issue and opens its merge request — after which
            it is an ordinary MR that <info>dashboard</info>, <info>check</info> and <info>merge</info> already handle.

              <info>upkeep publish pathauto 3223746</info>
              <info>upkeep publish pathauto 3223746 --draft</info>

            Opens merge requests; never merges one. Re-running after more commits updates
            the existing MR rather than opening a second.
            HELP);

        $this->addModuleArgument()
            ->addTargetCoreOption()
            ->addCockpitOption()
            ->addProjectsRootOption();

        $this->addArgument(
            'issue',
            InputArgument::REQUIRED,
            'drupal.org issue node id whose work branch should be published',
        );
        $this->addOption('branch', null, InputOption::VALUE_REQUIRED, 'Publish this branch instead of deriving it');
        $this->addOption('title', null, InputOption::VALUE_REQUIRED, 'Merge request title (default: from the issue)');
        $this->addOption(
            'target',
            null,
            InputOption::VALUE_REQUIRED,
            'Branch to merge into (default: the base the work branch was started from)',
        );
        $this->addOption('draft', null, InputOption::VALUE_NONE, 'Open it as a draft');
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

        $client = $this->gitlabClient ?? GitlabClientFactory::authenticated(
            GitlabClientFactory::resolver($io),
            static function (): void {
            },
        ) ?? throw new WorkflowException(
            GitlabClientFactory::missingTokenMessage(GitlabClientFactory::resolver($io)),
        );

        $drupal = $this->drupalClient ?? new DrupalOrgClient(HttpClient::create());
        $issue = $drupal->issue($nid) ?? throw new WorkflowException(sprintf(
            'drupal.org issue #%d could not be read, so there is nothing to name the merge request after.',
            $nid,
        ));

        $branchName = self::stringOption($input, 'branch');
        $branch = $branchName !== null
            ? IssueBranch::named($nid, $branchName)
            : IssueBranch::forIssue($nid, $issue->title);

        $project = $client->project($module->project);
        if ($project instanceof ApiFailure) {
            throw new WorkflowException(sprintf(
                'Cannot resolve the GitLab project for "%s": %s',
                $module->project,
                $project->message,
            ));
        }

        $io->section('Issue fork');
        $fork = self::requireIssueFork($client, $project, $issue->nid, $module->name);
        $io->writeln(sprintf('Pushing to %s', $fork->pathWithNamespace));

        $adapter = $this->engines->create(
            $cockpit,
            self::stringOption($input, 'projects-root'),
            static fn (string $line) => $io->writeln($line),
            static fn (string $line) => $io->writeln($line, OutputInterface::VERBOSITY_VERBOSE),
        );

        $io->section('Push');
        $environment = $adapter->ensureEnv($module, $coreMajor);
        $sha = $adapter->pushWork($environment, $branch, GitRemote::issueFork($issue->nid, $fork->sshUrl));
        $io->writeln(sprintf('Pushed %s at %s.', $branch->name, substr($sha, 0, 8)));

        $io->section('Merge request');
        $existing = $client->mergeRequestForBranch($project, $branch->name, $fork);
        if ($existing instanceof MergeRequest) {
            // Re-running publish after more commits is the normal way to update
            // an MR: the push above already moved it.
            $io->success(sprintf('Updated the open merge request for this branch: !%d', $existing->iid));
            $io->writeln($existing->webUrl);

            return ExitCode::OK;
        }
        if ($existing instanceof ApiFailure) {
            throw new WorkflowException(sprintf(
                'The branch was pushed, but its merge requests could not be listed: %s',
                $existing->message,
            ));
        }

        // The base the work was cut from, else what the project itself calls
        // default. Never a tracked core major: those name versions of Drupal,
        // not branches, and no contrib project has one called "11".
        $target = self::stringOption($input, 'target')
            ?? $adapter->recordedBaseBranch($environment)
            ?? ($project->defaultBranch !== '' ? $project->defaultBranch : null)
            ?? throw new WorkflowException(sprintf(
                "The branch was pushed, but upkeep does not know what to open the merge request against.\n"
                . 'Name it: upkeep publish %s %d --target=<branch> (a branch on the project, like 2.0.x — not '
                . 'a core version).',
                $module->name,
                $nid,
            ));

        // Posted to the *fork*, which holds the branch, naming the canonical
        // project as the destination. Backwards-looking until you remember
        // that the branch is the subject of the request.
        $created = $client->createMergeRequest(
            $fork,
            $branch->name,
            $target,
            self::title($input, $issue->nid, $issue->title),
            sprintf("Fixes %s\n\nOpened with `upkeep publish`.", $issue->url),
            into: $project,
        );

        if ($created instanceof ApiFailure) {
            throw new WorkflowException(sprintf(
                "The branch was pushed, but the merge request could not be opened: %s\n"
                . 'Open it in the browser: %s',
                $created->message,
                $created->browserUrl ?? $project->webUrl . '/-/merge_requests/new',
            ));
        }

        $io->success(sprintf('Opened !%d against %s.', $created->iid, $target));
        $io->writeln($created->webUrl);
        $io->writeln(sprintf(
            '<fg=gray>It is now an ordinary MR: upkeep check %s %d, and it appears on the dashboard.</>',
            $module->name,
            $created->iid,
        ));

        return ExitCode::OK;
    }

    /**
     * The issue fork, or a refusal explaining how to make one.
     *
     * upkeep does not create it. The fork is minted by drupal.org's own issue
     * page, which is also what associates it with the issue — a fork conjured
     * straight from the GitLab API would be a repository nothing links to,
     * which is harder to clean up than the click was to make. So this is a
     * browser handoff, like the issue status and the credit.
     *
     * Checked *before* anything is pushed: discovering it afterwards would
     * leave a branch on a remote the operator never chose.
     *
     * @throws WorkflowException when there is no fork, or it cannot be read
     */
    private static function requireIssueFork(
        GitlabClient $client,
        Project $project,
        int $nid,
        string $module,
    ): Project {
        $fork = $client->issueFork($project, $nid);

        if ($fork instanceof ApiFailure) {
            throw new WorkflowException(sprintf(
                'The issue fork for #%d could not be read: %s',
                $nid,
                $fork->message,
            ));
        }

        if ($fork === null) {
            throw new WorkflowException(sprintf(
                "Issue #%d has no issue fork yet, and that is where the branch has to go — on drupal.org a "
                . "merge request comes from a fork at issue/<module>-<nid>, never from the project itself.\n\n"
                . "  1. Open %s\n"
                . "  2. Click \"Create issue fork\" (under the issue summary)\n"
                . "  3. Re-run: upkeep publish %s %d\n\n"
                . 'upkeep does not create it: drupal.org mints the fork *and* links it to the issue, and one '
                . 'made straight from the GitLab API would be a repository nothing points at.',
                $nid,
                IssueReference::issueUrl($nid),
                $module,
                $nid,
            ));
        }

        if ($fork->sshUrl === '') {
            throw new WorkflowException(sprintf(
                'The issue fork %s reports no SSH URL, so upkeep does not know where to push. Report this — it '
                . 'is a shape this tool has not seen.',
                $fork->pathWithNamespace,
            ));
        }

        return $fork;
    }

    /**
     * The drupal.org title convention, so the MR is linked to its issue by the
     * same rule `IssueReference` parses everywhere else in this tool.
     */
    private static function title(InputInterface $input, int $nid, string $issueTitle): string
    {
        $given = self::stringOption($input, 'title');
        $title = $given ?? sprintf('Issue #%d: %s', $nid, $issueTitle);

        return $input->getOption('draft') === true ? 'Draft: ' . $title : $title;
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

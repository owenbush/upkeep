<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Symfony\Contracts\HttpClient\HttpClientInterface;
use Upkeep\Adapter\EngineAdapterFactory;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\Module;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueFile;
use Upkeep\Drupal\IssueVersion;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Patches\PatchFetcher;
use Upkeep\Patches\PatchSelector;
use Upkeep\Workflow\MrContextResolver;
use Upkeep\Workflow\PatchContext;
use Upkeep\Workflow\WorkflowException;

/**
 * Shared wiring for the patch commands (patch:apply, patch:check): the
 * argument surface, issue resolution, patch selection, and the download.
 *
 * The patch-side counterpart of AbstractMrCommand, and deliberately its
 * mirror image — a patch contribution should cost a maintainer exactly what a
 * merge request costs. The one thing this side has that the MR side does not
 * is a *choice*: an issue routinely carries several patches, so when the
 * operator has not pinned one and there is a terminal to ask at, they are
 * asked. When there is not — a pipe, a cron, --no-interaction — the newest
 * patch is taken and the choice is stated rather than hidden.
 */
abstract class AbstractPatchCommand extends UpkeepCommand
{
    public function __construct(
        private readonly EngineAdapterFactory $engines,
        private readonly ?DrupalOrgClient $drupalClient = null,
        private readonly ?HttpClientInterface $downloader = null,
        /**
         * Only for reading the project's branches, so an issue's version can
         * be turned into a base branch. The patch surface is otherwise
         * GitLab-free, and stays usable without a token: everything about
         * this resolution degrades to null.
         */
        private readonly ?GitlabClient $gitlabClient = null,
    ) {
        parent::__construct();
    }

    private ?DrupalOrgClient $drupal = null;

    protected function configurePatchSurface(): void
    {
        $this->addModuleArgument();
        $this->addArgument(
            'issue',
            InputArgument::REQUIRED,
            'drupal.org issue node id carrying the patch (see `upkeep patches`)',
        );
        $this->addTargetCoreOption();
        $this->addOption(
            'file',
            null,
            InputOption::VALUE_REQUIRED,
            'Apply this exact attachment by filename instead of choosing (e.g. 3597808-9-d11.patch)',
        );
        $this->addOption(
            'url',
            null,
            InputOption::VALUE_REQUIRED,
            'Fetch the patch from this URL instead of the issue\'s attachments (a fork, a re-roll posted elsewhere)',
        );
        $this->addOption(
            'latest',
            null,
            InputOption::VALUE_NONE,
            'Take the newest patch without asking, even when the issue carries several',
        );
        $this->addCockpitOption();
        $this->addProjectsRootOption();
        $this->addNoUpdateOption();
    }

    /**
     * The issue node id: a positive integer, refused here rather than turned
     * into a request for node 0 and a confusing miss. Same rule, same
     * reasoning, as the MR-IID one.
     *
     * @throws WorkflowException
     */
    protected static function issueNid(InputInterface $input): int
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

    /**
     * Resolves module, core version, issue and patch, and downloads the file.
     *
     * @throws WorkflowException when any part of the context cannot be resolved
     */
    protected function resolveContext(InputInterface $input, SymfonyStyle $io): PatchContext
    {
        $cockpit = $this->cockpit($input);
        $module = MrContextResolver::requireModule(
            $this->modules($cockpit),
            self::stringArgument($input, 'module'),
        );
        $coreMajor = self::targetCore($input, $module);
        $nid = self::issueNid($input);

        $io->writeln(sprintf('Resolving issue #%d via drupal.org ...', $nid));
        $issue = $this->drupal()->issue($nid) ?? throw new WorkflowException(sprintf(
            'drupal.org issue #%d could not be read. Check the node id (it is the number in the issue URL).',
            $nid,
        ));

        $patch = $this->choosePatch($input, $io, $issue);
        $url = self::stringOption($input, 'url') ?? $patch->url;

        $io->writeln(sprintf('Patch: %s', PatchSelector::describe($patch)));
        $localPath = $this->fetcher($cockpit)->fetch($nid, $patch->name, $url);

        return new PatchContext(
            $module,
            $coreMajor,
            $issue,
            $patch,
            $localPath,
            $this->resolveBaseBranch($io, $module, $issue),
        );
    }

    /**
     * The branch the issue is filed against, confirmed to exist.
     *
     * An issue carries a "Version" and its patches are cut from that branch.
     * Resolving it turns "does not apply to 1.0.x" — which was true, and
     * useless, because the patch was never meant for 1.0.x — into applying it
     * where it belongs.
     *
     * Everything here degrades to null, and null means the adapter resolves
     * the base from the working copy exactly as before. A version field
     * nobody set, a project whose branches cannot be listed, and a version
     * naming no real branch are all ordinary; none is worth refusing over.
     */
    private function resolveBaseBranch(SymfonyStyle $io, Module $module, Issue $issue): ?string
    {
        if (IssueVersion::branchCandidates($issue->version) === []) {
            return null;
        }

        $client = $this->gitlabClient ?? GitlabClientFactory::authenticated(
            GitlabClientFactory::resolver($io),
            static function (): void {
            },
        );
        if ($client === null) {
            return null;
        }

        $project = $client->project($module->project);
        if ($project instanceof ApiFailure) {
            return null;
        }

        $branches = $client->branchNames($project);
        if ($branches instanceof ApiFailure) {
            return null;
        }

        $branch = IssueVersion::resolveBranch($issue->version, $branches);
        if ($branch === null) {
            // Said out loud: the issue names a version, the project has no
            // such branch, and the apply is about to use a different one.
            $io->writeln(sprintf(
                '<comment>Issue version "%s" matches no branch on %s (%s); using the working copy\'s base.</>',
                (string) $issue->version,
                $module->project,
                implode(', ', $branches),
            ));

            return null;
        }

        $io->writeln(sprintf('Base branch: %s (from the issue version "%s")', $branch, (string) $issue->version));

        return $branch;
    }

    /**
     * @throws WorkflowException when no patch can be settled on
     */
    private function choosePatch(InputInterface $input, SymfonyStyle $io, Issue $issue): IssueFile
    {
        $explicitUrl = self::stringOption($input, 'url');
        if ($explicitUrl !== null) {
            // The URL names the file; the issue is still required, because it
            // is what the branch, the commit message and the report are keyed
            // on. A named attachment still wins for its metadata when the URL
            // happens to be one of them.
            foreach (PatchSelector::candidates($issue) as $candidate) {
                if ($candidate->url === $explicitUrl) {
                    return $candidate;
                }
            }

            return new IssueFile(PatchFetcher::safeName(basename($explicitUrl)), $explicitUrl, 0, 0);
        }

        $selection = PatchSelector::select(
            $issue,
            self::stringOption($input, 'file'),
            $input->getOption('latest') === true,
        );

        if ($selection->problem !== null) {
            throw new WorkflowException($selection->problem);
        }
        if ($selection->chosen !== null) {
            return $selection->chosen;
        }

        return $this->askWhichPatch($input, $io, $selection->candidates);
    }

    /**
     * @param list<IssueFile> $candidates newest first
     */
    private function askWhichPatch(InputInterface $input, SymfonyStyle $io, array $candidates): IssueFile
    {
        if (!$input->isInteractive()) {
            // Nothing to ask and nobody to ask: take the newest and say so,
            // because a scripted run that silently guessed would report a
            // verdict on a patch the operator never named.
            $io->note(sprintf(
                'Issue carries %d patches and none was named; taking the newest (%s). Pass --file or --latest '
                . 'to make this explicit.',
                \count($candidates),
                $candidates[0]->name,
            ));

            return $candidates[0];
        }

        // Labels must be distinct or the answer cannot be mapped back: the
        // update bot re-uploads under one filename, so an issue can carry
        // several attachments whose plain descriptions are identical.
        $labels = PatchSelector::labels($candidates);
        $labels[0] .= ' [newest]';

        $answer = $io->choice('Which patch should be applied?', $labels, $labels[0]);
        $index = array_search($answer, $labels, true);

        return $candidates[$index === false ? 0 : $index];
    }

    /**
     * The drupal.org client for this invocation — one instance, so a command
     * that asks a follow-up question (patch:promote resolving the author of
     * the file it just chose) shares the memo and the warning list with the
     * resolution that preceded it.
     */
    protected function drupal(): DrupalOrgClient
    {
        return $this->drupal ??= $this->drupalClient ?? new DrupalOrgClient(HttpClient::create());
    }

    private function fetcher(Cockpit $cockpit): PatchFetcher
    {
        return new PatchFetcher(
            $this->downloader ?? HttpClient::create(),
            $cockpit->patchCachePath(),
        );
    }

    /**
     * One-line summary of the resolved context, printed before the long steps.
     */
    protected static function describeContext(SymfonyStyle $io, PatchContext $context, ?string $requestedVersion): void
    {
        $io->writeln(sprintf(
            'Issue #%d "%s" (%s)',
            $context->issue->nid,
            $context->issue->title,
            $context->issue->status->shortLabel(),
        ));
        $io->writeln(sprintf(
            'Target: Drupal core %s%s, module %s',
            $context->coreMajor,
            $requestedVersion === null ? ' (default: first tracked core version in the registry)' : '',
            $context->module->name,
        ));
    }

    /**
     * The engine adapter for this invocation; the engine implementation comes
     * from the injected factory — no command names or chooses one.
     */
    protected function adapter(InputInterface $input, OutputInterface $output): EngineAdapterInterface
    {
        return $this->engines->create(
            $this->cockpit($input),
            self::stringOption($input, 'projects-root'),
            static fn (string $line) => $output->writeln($line),
            static fn (string $line) => $output->writeln($line, OutputInterface::VERBOSITY_VERBOSE),
        );
    }
}

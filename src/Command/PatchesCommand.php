<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Cockpit\Module;
use Upkeep\Dashboard\DashboardCache;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueReference;
use Upkeep\Drupal\IssueStatus;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Project;
use Upkeep\Patches\Contribution;
use Upkeep\Workflow\ExitCode;

/**
 * Surface drupal.org issues in Needs Review / RTBC whose contribution is not
 * reachable from the MR-centric dashboard — the patch files that never became
 * a branch, and the issues where a patch sits alongside a merge request.
 *
 * An issue is withheld only when a merge request genuinely *carries* the work:
 * it asserts authorship of the issue (IssueReference::extractOwning) and it
 * has a non-empty diff (MergeRequest::carriesChanges). Neither half is
 * incidental — an empty draft that merely mentions an issue used to hide every
 * patch on it, which is how a patch-only report came to hide patches.
 *
 * Running without a GitLab token is a documented degraded mode, not a
 * failure: the scan proceeds without cross-referencing merge requests, says
 * so once, and still exits 0. Only a missing cockpit, an unreadable registry
 * or an unregistered --module is an infrastructure failure.
 */
#[AsCommand(
    name: 'patches',
    description: 'Show drupal.org issues carrying patch files, and how they relate to merge requests.',
)]
final class PatchesCommand extends UpkeepCommand
{
    private ?GitlabClient $resolvedGitlab = null;

    private bool $gitlabResolved = false;

    private bool $degradedModeAnnounced = false;

    public function __construct(
        private readonly ?DrupalOrgClient $drupalClient = null,
        private readonly ?GitlabClient $gitlabClient = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->setHelp(<<<'HELP'
            Issues carrying patch files, and how each relates to a merge request — the
            contributions the MR-centric dashboard cannot see.

              <info>upkeep patches</info>                  every registered module
              <info>upkeep patches --module=pathauto</info>
              <info>upkeep patches --without-mr</info>     only what no branch carries

            To check one: <info>upkeep patch:check <module> <issue></info>. An MR shown as "empty"
            carries no commits, so any patch beside it is the only work there is —
            see <info>upkeep explain "empty MR"</info>.
            HELP);

        $this->addCockpitOption();
        $this->addOption(
            'module',
            null,
            InputOption::VALUE_REQUIRED,
            'Only scan this module (default: all registered modules)',
        );
        $this->addOption(
            'without-mr',
            null,
            InputOption::VALUE_NONE,
            'Only issues no merge request carries — omit those where a patch sits alongside a real MR',
        );
    }

    /** stdout carries the table; diagnostics stay pipe-safe on stderr. */
    protected function diagnosticsOnStderr(): bool
    {
        return true;
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->cockpit($input);
        $modules = $this->modules($cockpit);

        $moduleFilter = self::stringOption($input, 'module');
        if ($moduleFilter !== null) {
            $modules = [$moduleFilter => self::requireModule($modules, $moduleFilter)];
        }
        $withoutMr = $input->getOption('without-mr') === true;

        $drupal = $this->drupalClient ?? new DrupalOrgClient(HttpClient::create());
        $dashCache = new DashboardCache($cockpit->dashboardCachePath());
        $scannedStatuses = [IssueStatus::NeedsReview, IssueStatus::Rtbc];

        $contributions = [];
        ksort($modules);
        foreach ($modules as $name => $module) {
            $issues = $drupal->projectIssues($name, $scannedStatuses);
            if ($issues === []) {
                continue;
            }

            $byNid = $this->mergeRequestsByIssue($name, $module, $issues, $dashCache, $io);
            foreach ($issues as $issue) {
                $contribution = new Contribution($name, $issue, $byNid[$issue->nid] ?? []);
                $kind = $contribution->kind();
                if ($kind->isCoveredByMergeRequest()) {
                    continue;
                }
                if ($withoutMr && $kind->hasSubstantiveMergeRequest()) {
                    continue;
                }
                $contributions[] = $contribution;
            }
        }

        self::reportScanWarnings($io, $drupal);

        if ($contributions === []) {
            $io->success(
                'Nothing to report — every Needs Review / RTBC issue is covered by a merge request '
                . 'that carries changes.',
            );

            return ExitCode::OK;
        }

        $this->renderTable($contributions, $output);
        $output->writeln('');
        $output->writeln(self::summaryLine($contributions, $scannedStatuses));

        return ExitCode::OK;
    }

    /**
     * @param list<Contribution> $contributions
     */
    private function renderTable(array $contributions, OutputInterface $output): void
    {
        $rows = [];
        foreach ($contributions as $contribution) {
            $issue = $contribution->issue;
            $latest = $issue->latestPatch();
            $rows[] = [
                $contribution->module,
                '#' . $issue->nid,
                $issue->status->shortLabel(),
                $issue->patchCount() > 0 ? (string) $issue->patchCount() : '–',
                $latest !== null ? DashboardRow::truncate($latest->name, 30) : '–',
                $contribution->mergeRequestCell(),
                DashboardRow::truncate($issue->title),
            ];
        }

        ColumnTable::render(
            $output,
            ['MODULE', 'ISSUE', 'STATUS', 'PATCHES', 'LATEST PATCH', 'MR', 'TITLE'],
            $rows,
            self::colorCells(...),
        );
    }

    /**
     * @param list<string> $cells [MODULE, ISSUE, STATUS, PATCHES, LATEST PATCH, MR, TITLE]
     * @return list<string>
     */
    private static function colorCells(array $cells): array
    {
        $fmt = $cells;

        $fmt[2] = match ($cells[2]) {
            'RTBC' => '<fg=green>' . $cells[2] . '</>',
            'review' => '<fg=yellow>' . $cells[2] . '</>',
            default => $cells[2],
        };

        $fmt[3] = $cells[3] === '–'
            ? '<fg=gray>' . $cells[3] . '</>'
            : $cells[3];

        $fmt[4] = $cells[4] === '–'
            ? '<fg=gray>' . $cells[4] . '</>'
            : $cells[4];

        // An empty MR is the actionable cell on the row — it is why the patch
        // beside it has gone unreviewed — so it is the one that gets the
        // warning colour rather than the muted one a bare dash gets.
        $fmt[5] = match (true) {
            $cells[5] === '–' => '<fg=gray>' . $cells[5] . '</>',
            str_contains($cells[5], ' empty') => '<fg=yellow>' . $cells[5] . '</>',
            // Merged and nothing since: the row needs no work, and the colour
            // says so before the words are read. With newer work since, it is
            // an ordinary open contribution again.
            str_contains($cells[5], 'newer work since') => '<fg=yellow>' . $cells[5] . '</>',
            str_contains($cells[5], ' merged ') => '<fg=green>' . $cells[5] . '</>',
            default => $cells[5],
        };

        // Written back by index, so the result is repacked into a list:
        // ColumnTable's colouriser contract is list-in, list-out.
        return array_values($fmt);
    }

    /**
     * @param list<Contribution> $contributions
     * @param list<IssueStatus>  $scannedStatuses
     */
    private static function summaryLine(array $contributions, array $scannedStatuses): string
    {
        $counts = [];
        foreach ($contributions as $contribution) {
            $label = $contribution->kind()->summaryLabel();
            if ($label !== null) {
                $counts[$label] = ($counts[$label] ?? 0) + 1;
            }
        }

        $segments = [sprintf(
            '%d %s',
            \count($contributions),
            \count($contributions) === 1 ? 'issue' : 'issues',
        )];
        foreach ($counts as $label => $count) {
            $segments[] = $count . ' ' . $label;
        }

        $moduleNames = array_unique(array_map(
            static fn (Contribution $c): string => $c->module,
            $contributions,
        ));
        $segments[] = sprintf(
            '%d %s',
            \count($moduleNames),
            \count($moduleNames) === 1 ? 'module' : 'modules',
        );
        $segments[] = 'statuses: ' . implode(
            ', ',
            array_map(static fn (IssueStatus $s): string => $s->shortLabel(), $scannedStatuses),
        );

        return '<fg=gray>' . implode(' · ', $segments) . '</>';
    }

    /**
     * Every merge request that asserts authorship of one of $issues, keyed by
     * node id, with emptiness resolved as far as the available sources allow.
     *
     * @param list<Issue> $issues
     * @return array<int, list<MergeRequest>>
     */
    private function mergeRequestsByIssue(
        string $name,
        Module $module,
        array $issues,
        DashboardCache $dashCache,
        SymfonyStyle $io,
    ): array {
        $snapshot = $dashCache->load($name);
        if ($snapshot !== null) {
            // Snapshots hold single-MR detail payloads, so their diff refs —
            // and with them emptiness — are already settled. A snapshot cached
            // by an older upkeep has no diff refs at all and reads as unknown,
            // which Contribution treats as substantive: stale data narrows the
            // report back to its previous behaviour rather than mislabelling.
            return self::groupByOwningIssue($snapshot->mergeRequests(), $issues);
        }

        $gitlab = $this->gitlab($io);
        if ($gitlab === null) {
            if (!$this->degradedModeAnnounced) {
                $this->degradedModeAnnounced = true;
                $io->note(
                    'No dashboard cache and no GitLab token — showing all matching issues without '
                    . 'cross-referencing MRs.',
                );
            }

            return [];
        }

        $project = $gitlab->project($module->project);
        if ($project instanceof ApiFailure) {
            return [];
        }

        $list = $gitlab->openMergeRequests($project);
        if ($list instanceof ApiFailure) {
            return [];
        }

        // Merged ones too. An open issue whose work has already landed reads
        // exactly like an untouched one when only open MRs are fetched, and
        // Project Update Bot compatibility issues — kept open on purpose so
        // the bot can post again — are mostly that shape.
        $merged = $gitlab->mergedMergeRequests($project);
        $all = $merged instanceof ApiFailure ? $list->all() : [...$list->all(), ...$merged->all()];

        $forkNids = $gitlab->issueForkNids($project);

        return $this->withResolvedEmptiness(
            self::groupByOwningIssue($all, $issues, $forkNids instanceof ApiFailure ? [] : $forkNids),
            $gitlab,
            $project,
        );
    }

    /**
     * Merge requests keyed by the issue they claim, restricted to the issues
     * actually being scanned. The restriction is what bounds the follow-up
     * detail fetches: an MR on an issue that is neither Needs Review nor RTBC
     * cannot change a row, so it is never looked at twice.
     *
     * @param list<MergeRequest> $mrs
     * @param list<Issue>        $issues
     * @param array<int, int>    $forkNids source-project id => issue nid
     * @return array<int, list<MergeRequest>>
     */
    private static function groupByOwningIssue(array $mrs, array $issues, array $forkNids = []): array
    {
        $wanted = [];
        foreach ($issues as $issue) {
            $wanted[$issue->nid] = true;
        }

        $byNid = [];
        foreach ($mrs as $mr) {
            // The fork is authoritative — see Patches\Contribution::pair().
            $nid = ($mr->sourceProjectId !== null ? ($forkNids[$mr->sourceProjectId] ?? null) : null)
                ?? IssueReference::extractOwning($mr->title, $mr->sourceBranch, $mr->description);

            if ($nid === null || !isset($wanted[$nid])) {
                continue;
            }
            $byNid[$nid][] = $mr;
        }

        return $byNid;
    }

    /**
     * Re-fetch the merge requests whose emptiness the list endpoint could not
     * answer. GitLab omits diff_refs from list payloads and offers no bulk
     * alternative, so this costs one GET per candidate MR — bounded by
     * groupByOwningIssue to those attached to a scanned issue, and memoized by
     * GitlabClient for the rest of the run. An MR whose detail fetch fails
     * keeps its unknown emptiness and is treated as real work.
     *
     * @param array<int, list<MergeRequest>> $byNid
     * @return array<int, list<MergeRequest>>
     */
    private function withResolvedEmptiness(array $byNid, GitlabClient $gitlab, Project $project): array
    {
        foreach ($byNid as $nid => $mrs) {
            foreach ($mrs as $index => $mr) {
                if ($mr->carriesChanges() !== null) {
                    continue;
                }
                $detail = $gitlab->mergeRequest($project, $mr->iid);
                // The detail payload is strictly richer than the listed one, so
                // it replaces rather than augments — but only once it has
                // identified itself as the same merge request. A body that
                // answers with some other resource must not be written into
                // this row under the listed MR's identity.
                if (!$detail instanceof ApiFailure && $detail->iid === $mr->iid) {
                    $byNid[$nid][$index] = $detail;
                }
            }
        }

        return $byNid;
    }

    /**
     * The GitLab client for this run, resolved at most once. A missing
     * credential is announced as a warning, not an error: this command's
     * degraded mode is documented and still exits 0.
     */
    private function gitlab(SymfonyStyle $io): ?GitlabClient
    {
        if (!$this->gitlabResolved) {
            $this->gitlabResolved = true;
            $this->resolvedGitlab = $this->gitlabClient ?? GitlabClientFactory::authenticated(
                GitlabClientFactory::resolver($io),
                static function (string $message) use ($io): void {
                    $io->warning($message);
                },
            );
        }

        return $this->resolvedGitlab;
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Helper\ProgressBar;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Cockpit\Module;
use Upkeep\Dashboard\DashboardCache;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Dashboard\ModuleSnapshot;
use Upkeep\Dashboard\ModuleSummary;
use Upkeep\Dashboard\RowFactory;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueReference;
use Upkeep\Drupal\IssueStatus;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Results\ResultsCache;
use Upkeep\Workflow\ExitCode;

/**
 * One table of every open MR across all registered modules and tracked core
 * versions: MODULE, MR, ISSUE, CORE, TITLE, CI, LOCAL, STATUS.
 *
 * Remote data (GitLab MRs, drupal.org issues) is cached per module under
 * `<cockpit>/cache/dashboard/`. On repeat runs the cached snapshot is used;
 * pass `--refresh` to re-fetch all modules, or `--refresh=<module>` for one.
 * Local check results and gate verdicts are always resolved fresh.
 *
 * Classification is NOT done here: the rows come from Dashboard\RowFactory,
 * the same pipeline the fast-lane merge command consumes, so the dashboard's
 * READY-AUTO and the merge command's READY-AUTO cannot drift apart.
 *
 * NOTE for application wiring: the --version option collides with Symfony
 * Console's built-in application-level --version/-V. The hosting Application
 * must drop that default option before running this command (same convention
 * as AbstractMrCommand — see its docblock for the snippet).
 */
#[AsCommand(
    name: 'dashboard',
    description: 'Per-module overview of everything open; name a module to see its rows and what to run next.',
)]
final class DashboardCommand extends UpkeepCommand
{
    /**
     * @param ?GitlabClient    $client       injected in tests; built from the resolved token otherwise
     * @param ?DrupalOrgClient $drupalClient injected in tests; built with HttpClient::create() otherwise
     */
    public function __construct(
        private readonly ?GitlabClient $client = null,
        private readonly ?DrupalOrgClient $drupalClient = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->setHelp(<<<'HELP'
            Where to start. With no arguments it summarises every registered module —
            how many merge requests and patch issues are open, how many are ready, and
            how many have never been checked.

            Name a module to see its individual rows. Each row carries a NEXT column
            with the command to run for it.

              <info>upkeep dashboard</info>                    every module, one line each
              <info>upkeep dashboard pathauto</info>           that module's rows, with what to do about each
              <info>upkeep dashboard pathauto --refresh</info> re-fetch first (goes to the network)
              <info>upkeep dashboard --all</info>              every row of every module
              <info>upkeep dashboard pathauto -v</info>        the gate's own reason tokens

            Cached by default, so repeat runs are instant.
            <info>upkeep explain <term></info> defines any column or status.
            HELP);

        $this->addArgument(
            'module',
            InputArgument::OPTIONAL,
            'Drill into one module: its individual merge requests and patch issues. Omit for the overview.',
        );
        $this->addCockpitOption()
            ->addCoreFilterOption()
            ->addOption(
                'refresh',
                null,
                InputOption::VALUE_OPTIONAL,
                'Re-fetch remote data: --refresh for all modules, --refresh=<module> for one',
                false,
            )
            ->addOption(
                'no-patches',
                null,
                InputOption::VALUE_NONE,
                'Omit patch rows: only merge requests, as the dashboard showed before patch contributions '
                . 'were included',
            )
            ->addOption(
                'all',
                null,
                InputOption::VALUE_NONE,
                'Every row of every module, rather than the per-module overview',
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

        $versionFilter = self::stringOption($input, 'version');
        $withPatches = $input->getOption('no-patches') !== true;

        // Naming a module narrows everything about the run: which module is
        // shown in detail, and — with a bare --refresh — which one is
        // re-fetched. Refreshing a cockpit to look at one module would be the
        // expensive half of a command whose whole point was to be specific.
        $only = self::stringArgument($input, 'module');
        $only = $only === '' ? null : self::requireModule($modules, $only)->name;
        $detailed = $only !== null || $input->getOption('all') === true;

        $refresh = $input->getOption('refresh');
        $refreshAll = $refresh === null && $only === null;
        $refreshModule = \is_string($refresh) && $refresh !== ''
            ? $refresh
            : ($refresh === null ? $only : null);

        if ($only !== null) {
            $modules = [$only => $modules[$only]];
        }

        $dashCache = new DashboardCache($cockpit->dashboardCachePath());
        $rowFactory = new RowFactory(new ResultsCache($cockpit->resultsPath()));
        $drupal = $this->drupalClient ?? new DrupalOrgClient(HttpClient::create());

        ksort($modules);

        /** @var array<string, ModuleSnapshot> $snapshots */
        $snapshots = [];
        $client = null;
        $rows = [];

        // One pass per module, in registry order: resolve its snapshot (from
        // the cache, or by fetching), then turn it straight into rows. Every
        // module therefore leaves the loop having produced either its rows or
        // a failure row — there is no third outcome to defend against later.
        $progress = self::progress($io, $modules, $refreshAll, $refreshModule);

        foreach ($modules as $name => $module) {
            $shouldRefresh = $refreshAll || $refreshModule === $name;
            $snapshot = $shouldRefresh ? null : $dashCache->load($name);

            if ($snapshot === null) {
                $progress?->setMessage($name);
                $progress?->display();
                if ($client === null) {
                    // The factory reports the missing-token guidance itself;
                    // without a credential there is nothing to fetch and no
                    // table to show.
                    $client = $this->client ?? GitlabClientFactory::forConsole($io);
                    if ($client === null) {
                        return ExitCode::INFRASTRUCTURE;
                    }
                }

                $fetched = $this->fetchModule($client, $drupal, $module);
                if ($fetched instanceof ApiFailure) {
                    $rows[] = DashboardRow::forModuleFailure($name, $fetched);
                    continue;
                }

                $snapshot = $fetched;
                $dashCache->save($name, $snapshot);
                $progress?->advance();
            }

            $snapshots[$name] = $snapshot;

            foreach (
                $rowFactory->rows(
                    $module,
                    $snapshot->project(),
                    $snapshot->mergeRequests(),
                    $versionFilter,
                ) as $row
            ) {
                $rows[] = $row;
            }

            if ($withPatches) {
                foreach ($rowFactory->patchRows($module, $snapshot, $versionFilter) as $patchRow) {
                    $rows[] = $patchRow;
                }
            }
        }

        $progress?->finish();
        if ($progress !== null) {
            $io->newLine(2);
        }

        self::reportScanWarnings($io, $drupal);

        if ($rows === []) {
            $io->note(
                $versionFilter === null
                    ? 'No open contributions across the registered modules.'
                    : sprintf(
                        'No open contributions targeting core %s across the registered modules.',
                        $versionFilter,
                    ),
            );

            return ExitCode::OK;
        }

        if ($detailed) {
            $this->renderTable($output, $rows, $snapshots, $output->isVerbose());
            self::renderFooter($output, $rows, $snapshots);
            self::renderDetailHints($output, $rows, $output->isVerbose());

            return ExitCode::OK;
        }

        self::renderOverview($output, $rows, $snapshots);
        self::renderFooter($output, $rows, $snapshots);
        $output->writeln(
            '<fg=gray>upkeep dashboard <module> for one module\'s rows · --all for every row</>',
        );

        return ExitCode::OK;
    }

    /**
     * The overview: one line per module, aggregated from exactly the rows the
     * detailed view would print.
     *
     * Aggregated rather than recounted on purpose. A module whose overview
     * says three READY-AUTO must show three READY-AUTO when drilled into, and
     * the only way to guarantee that is for both to be the same list.
     *
     * @param list<DashboardRow>            $rows
     * @param array<string, ModuleSnapshot> $snapshots
     */
    private static function renderOverview(OutputInterface $output, array $rows, array $snapshots): void
    {
        $byModule = [];
        foreach ($rows as $row) {
            $byModule[$row->module][] = $row;
        }

        $now = new \DateTimeImmutable();
        $cells = [];
        foreach ($byModule as $module => $moduleRows) {
            $snapshot = $snapshots[$module] ?? null;
            $cells[] = ModuleSummary::fromRows($module, $moduleRows)
                ->toTableCells($snapshot !== null ? $snapshot->ageLabel($now) : 'never');
        }

        ColumnTable::render(
            $output,
            ['MODULE', 'CORES', 'MRS', 'PATCH ISSUES', 'READY', 'CI FAILED', 'UNCHECKED', 'CACHED'],
            $cells,
            self::colorOverviewCells(...),
        );
    }

    /**
     * @param list<string> $cells [MODULE, CORES, MRS, PATCH ISSUES, READY, CI FAILED, UNCHECKED, CACHED]
     * @return list<string>
     */
    private static function colorOverviewCells(array $cells): array
    {
        $fmt = $cells;

        // Three cells carry a verdict, and they are the three worth colour:
        // READY means you can act right now, CI FAILED means nobody here can,
        // and UNCHECKED is the queue of work that would turn one into the
        // other. Counts and dashes stay muted so those three stand out.
        $fmt[4] = $cells[4] === '–' ? '<fg=gray>–</>' : '<fg=green>' . $cells[4] . '</>';
        $fmt[5] = $cells[5] === '–' ? '<fg=gray>–</>' : '<fg=red>' . $cells[5] . '</>';
        $fmt[6] = $cells[6] === '–' ? '<fg=gray>–</>' : '<fg=yellow>' . $cells[6] . '</>';
        foreach ([1, 2, 3] as $i) {
            $fmt[$i] = $cells[$i] === '–' ? '<fg=gray>–</>' : $cells[$i];
        }
        $fmt[7] = '<fg=gray>' . $cells[7] . '</>';

        return array_values($fmt);
    }

    /**
     * What to read, under the table a maintainer has just been handed.
     *
     * The overview has always carried a hint and the drill-down carried none,
     * which is backwards: the overview is a summary a person can act on by
     * drilling in, while the drill-down is where the actual work is chosen.
     *
     * @param list<DashboardRow> $rows
     */
    private static function renderDetailHints(OutputInterface $output, array $rows, bool $verbose): void
    {
        $ready = \count(array_filter($rows, static fn (DashboardRow $r): bool => $r->isReadyAuto()));

        $hints = [];
        if ($ready > 0) {
            $hints[] = sprintf(
                '%d row%s ready to merge · upkeep merge --fast-lane',
                $ready,
                $ready === 1 ? '' : 's',
            );
        }
        $hints[] = 'Run the command in NEXT for any row · upkeep explain <term> for what a column means';
        if (!$verbose) {
            $hints[] = '-v shows the gate\'s own reason tokens instead of the plain-English status';
        }

        foreach ($hints as $hint) {
            $output->writeln('<fg=gray>' . $hint . '</>');
        }
    }

    /**
     * A per-module progress bar for the fetching path only.
     *
     * A refresh is minutes of silence otherwise: each module costs a GitLab
     * round trip plus a drupal.org scan whose attachment lookups are one
     * request per file, and drupal.org's origin is slow when its cache is
     * cold. A cached run needs none of this and gets none — the bar would be
     * gone before it rendered.
     *
     * It lives on the SymfonyStyle, which for this command is already stderr,
     * so stdout stays exactly the table a pipe expects.
     *
     * @param array<string, Module> $modules
     */
    private static function progress(
        SymfonyStyle $io,
        array $modules,
        bool $refreshAll,
        ?string $refreshModule,
    ): ?ProgressBar {
        $count = $refreshAll ? \count($modules) : ($refreshModule !== null ? 1 : 0);
        if ($count === 0 || !$io->isDecorated()) {
            return null;
        }

        $bar = $io->createProgressBar($count);
        $bar->setFormat(' %current%/%max% [%bar%] fetching %message%');
        $bar->setMessage('');

        return $bar;
    }

    /**
     * @param list<DashboardRow>            $rows
     * @param array<string, ModuleSnapshot> $snapshots
     */
    private function renderTable(
        OutputInterface $output,
        array $rows,
        array $snapshots,
        bool $verbose = false,
    ): void {
        $cells = [];
        $groups = [];
        foreach ($rows as $row) {
            $rowCells = $row->toTableCells($verbose);
            array_splice($rowCells, 2, 0, [$this->issueCell($row, $snapshots)]);
            $cells[] = $rowCells;
            $groups[] = $row->module;
        }

        ColumnTable::render(
            $output,
            ['MODULE', 'MR', 'ISSUE', 'CORE', 'TITLE', 'CI', 'LOCAL', 'STATUS', 'NEXT'],
            $cells,
            self::colorCells(...),
            $groups,
        );
    }

    /**
     * The ISSUE cell: the linked drupal.org issue number, flagged when the
     * issue carries a patch newer than the merge request's last update.
     *
     * @param array<string, ModuleSnapshot> $snapshots
     */
    private function issueCell(DashboardRow $row, array $snapshots): string
    {
        if ($row->contribution !== null) {
            return (string) $row->contribution->issue->nid;
        }
        if ($row->mergeRequest === null) {
            return '–';
        }

        $mergeRequest = $row->mergeRequest;
        $nid = IssueReference::extract(
            $mergeRequest->title,
            $mergeRequest->sourceBranch,
            $mergeRequest->description,
        );
        if ($nid === null) {
            return '–';
        }

        $latestPatch = ($snapshots[$row->module] ?? null)?->issue($nid)?->latestPatch();
        if ($latestPatch === null || $latestPatch->timestamp <= 0 || $mergeRequest->updatedAt === null) {
            return (string) $nid;
        }

        $mrUpdated = strtotime($mergeRequest->updatedAt);

        return $mrUpdated !== false && $latestPatch->timestamp > $mrUpdated
            ? $nid . ' patch↑'
            : (string) $nid;
    }

    /**
     * @param list<DashboardRow>            $rows
     * @param array<string, ModuleSnapshot> $snapshots
     */
    private static function renderFooter(OutputInterface $output, array $rows, array $snapshots): void
    {
        $mrKeys = [];
        $patchKeys = [];
        foreach ($rows as $row) {
            if ($row->mergeRequest !== null) {
                $mrKeys[$row->module . ':' . $row->mergeRequest->iid] = true;
            }
            if ($row->contribution !== null) {
                $patchKeys[$row->module . ':' . $row->contribution->issue->nid] = true;
            }
        }
        $moduleNames = array_unique(array_map(static fn (DashboardRow $r): string => $r->module, $rows));

        $oldestSnapshot = null;
        foreach ($snapshots as $snapshot) {
            if ($oldestSnapshot === null || $snapshot->fetchedAt < $oldestSnapshot->fetchedAt) {
                $oldestSnapshot = $snapshot;
            }
        }

        $segments = [sprintf('%d open MRs', \count($mrKeys))];
        if ($patchKeys !== []) {
            $segments[] = sprintf(
                '%d patch %s',
                \count($patchKeys),
                \count($patchKeys) === 1 ? 'issue' : 'issues',
            );
        }
        $segments[] = sprintf(
            '%d %s',
            \count($moduleNames),
            \count($moduleNames) === 1 ? 'module' : 'modules',
        );
        $segments[] = 'cached ' . ($oldestSnapshot !== null
            ? $oldestSnapshot->ageLabel(new \DateTimeImmutable())
            : 'never');
        $segments[] = '--refresh to update';

        $output->writeln('');
        $output->writeln('<fg=gray>' . implode(' · ', $segments) . '</>');
    }

    private function fetchModule(
        GitlabClient $client,
        DrupalOrgClient $drupal,
        Module $module,
    ): ModuleSnapshot|ApiFailure {
        $project = $client->project($module->project);
        if ($project instanceof ApiFailure) {
            return $project;
        }

        $list = $client->openMergeRequests($project);
        if ($list instanceof ApiFailure) {
            return $list;
        }

        $projectData = $project->toApiArray();
        $mrData = [];
        $issueNids = [];

        foreach ($list->all() as $listed) {
            $detail = $client->mergeRequest($project, $listed->iid);
            $mr = $detail instanceof ApiFailure ? $listed : $detail;
            $mrData[] = $mr->toApiArray();

            $nid = IssueReference::extract($mr->title, $mr->sourceBranch, $mr->description);
            if ($nid !== null) {
                $issueNids[$nid] = true;
            }
        }

        $issueData = [];
        foreach (array_keys($issueNids) as $nid) {
            $issue = $drupal->issue($nid);
            $issueData[$nid] = $issue?->toApiArray();
        }

        // Every *open* issue, not only the two statuses a contribution sits
        // in. One snapshot serves both questions a maintainer asks — "what is
        // waiting for me?" and "what could I work on?" — and each consumer
        // narrows it: the dashboard to contributions, `issues` and the browser
        // UI to the whole queue. This is the expensive half of a --refresh:
        // drupal.org returns attachments as references, so every one of them
        // is a request (see DrupalOrgClient).
        $patchIssueData = array_map(
            static fn (Issue $issue): array => $issue->toApiArray(),
            $drupal->projectIssues($module->name, IssueStatus::open()),
        );

        return new ModuleSnapshot(
            new \DateTimeImmutable(),
            $projectData,
            $mrData,
            $issueData,
            $patchIssueData,
        );
    }

    /**
     * @param list<string> $cells [MODULE, MR, ISSUE, CORE, TITLE, CI, LOCAL, STATUS, NEXT]
     * @return list<string>
     */
    private static function colorCells(array $cells): array
    {
        $fmt = $cells;

        // ISSUE (index 2) — highlight the patch↑ flag
        if (str_contains($cells[2], 'patch↑')) {
            $fmt[2] = str_replace('patch↑', '<fg=yellow>patch↑</>', $cells[2]);
        }

        // CI (index 5)
        $fmt[5] = match (true) {
            str_starts_with($cells[5], 'pass') => '<fg=green>' . $cells[5] . '</>',
            str_starts_with($cells[5], 'fail') => '<fg=red>' . $cells[5] . '</>',
            $cells[5] === '–' => '<fg=gray>' . $cells[5] . '</>',
            default => $cells[5],
        };

        // LOCAL (index 6). Unlike CI and STATUS this cell has a closed set of
        // values — DashboardRow::localCell() returns pass, fail, stale or the
        // en dash — so "anything else" is the muted case rather than an
        // unreachable arm left over from a wider vocabulary.
        $fmt[6] = match ($cells[6]) {
            'pass' => '<fg=green>' . $cells[6] . '</>',
            'fail' => '<fg=red>' . $cells[6] . '</>',
            default => '<fg=gray>' . $cells[6] . '</>',
        };

        // STATUS (index 7). Matched on both vocabularies, because -v swaps
        // the phrase for the gate's own tokens and both should read the same
        // way: green means go, red means stopped, amber means your move.
        $fmt[7] = match (true) {
            str_starts_with($cells[7], 'READY-AUTO'),
            str_starts_with($cells[7], 'ready to merge') => '<fg=green>' . $cells[7] . '</>',
            str_starts_with($cells[7], 'BLOCKED'),
            str_starts_with($cells[7], 'conflicts'),
            str_contains($cells[7], 'failed') => '<fg=red>' . $cells[7] . '</>',
            str_starts_with($cells[7], 'REVIEW'),
            str_starts_with($cells[7], 'needs'),
            str_starts_with($cells[7], 'checks are stale') => '<fg=yellow>' . $cells[7] . '</>',
            default => $cells[7],
        };

        // NEXT (index 8) — a command to run, or a parenthesised note saying
        // why there is none. The command is the point of the row, so it is the
        // thing that stands out; the note is muted, because "nothing to do
        // here" should not compete for attention.
        $fmt[8] = str_starts_with($cells[8], '(')
            ? '<fg=gray>' . $cells[8] . '</>'
            : '<fg=cyan>' . $cells[8] . '</>';

        // Written back by index, so the result is repacked into a list:
        // ColumnTable's colouriser contract is list-in, list-out.
        return array_values($fmt);
    }
}

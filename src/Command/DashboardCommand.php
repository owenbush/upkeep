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
use Upkeep\Dashboard\ModuleSnapshot;
use Upkeep\Dashboard\RowFactory;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\IssueReference;
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
    description: 'Show every open MR across registered modules and core versions with CI, local check, and '
    . 'fast-lane status.',
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
        $this->addCockpitOption()
            ->addCoreFilterOption()
            ->addOption(
                'refresh',
                null,
                InputOption::VALUE_OPTIONAL,
                'Re-fetch remote data: --refresh for all modules, --refresh=<module> for one',
                false,
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

        $refresh = $input->getOption('refresh');
        $refreshAll = $refresh === null;
        $refreshModule = \is_string($refresh) && $refresh !== '' ? $refresh : null;

        $dashCache = new DashboardCache($cockpit->dashboardCachePath());
        $rowFactory = new RowFactory(new ResultsCache($cockpit->resultsPath()));
        $drupal = $this->drupalClient ?? new DrupalOrgClient(HttpClient::create());

        ksort($modules);

        $needsFetch = [];
        $snapshots = [];

        foreach ($modules as $name => $module) {
            $shouldRefresh = $refreshAll || $refreshModule === $name;
            $cached = $shouldRefresh ? null : $dashCache->load($name);
            if ($cached !== null) {
                $snapshots[$name] = $cached;
            } else {
                $needsFetch[$name] = $module;
            }
        }

        /** @var array<string, ApiFailure> */
        $moduleFailures = [];

        if ($needsFetch !== []) {
            // The factory reports the missing-token guidance itself; without a
            // credential there is nothing to fetch and no table to show.
            $client = $this->client ?? GitlabClientFactory::forConsole($io);
            if ($client === null) {
                return ExitCode::INFRASTRUCTURE;
            }

            foreach ($needsFetch as $name => $module) {
                $result = $this->fetchModule($client, $drupal, $module);
                if ($result instanceof ModuleSnapshot) {
                    $snapshots[$name] = $result;
                    $dashCache->save($name, $result);
                } elseif ($result instanceof ApiFailure) {
                    $moduleFailures[$name] = $result;
                }
            }
        }

        $rows = [];
        foreach ($modules as $name => $module) {
            if (isset($moduleFailures[$name])) {
                $rows[] = DashboardRow::forModuleFailure($name, $moduleFailures[$name]);
                continue;
            }

            $snapshot = $snapshots[$name] ?? null;
            if ($snapshot === null) {
                continue;
            }

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
        }

        if ($rows === []) {
            $io->note(
                $versionFilter === null
                    ? 'No open merge requests across the registered modules.'
                    : sprintf(
                        'No open merge requests targeting core %s across the registered modules.',
                        $versionFilter,
                    ),
            );

            return ExitCode::OK;
        }

        $this->renderTable($output, $rows, $snapshots);
        self::renderFooter($output, $rows, $snapshots);

        return ExitCode::OK;
    }

    /**
     * @param list<DashboardRow>            $rows
     * @param array<string, ModuleSnapshot> $snapshots
     */
    private function renderTable(OutputInterface $output, array $rows, array $snapshots): void
    {
        $cells = [];
        $groups = [];
        foreach ($rows as $row) {
            $rowCells = $row->toTableCells();
            array_splice($rowCells, 2, 0, [$this->issueCell($row, $snapshots)]);
            $cells[] = $rowCells;
            $groups[] = $row->module;
        }

        ColumnTable::render(
            $output,
            ['MODULE', 'MR', 'ISSUE', 'CORE', 'TITLE', 'CI', 'LOCAL', 'STATUS'],
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
        foreach ($rows as $row) {
            if ($row->mergeRequest !== null) {
                $mrKeys[$row->module . ':' . $row->mergeRequest->iid] = true;
            }
        }
        $moduleNames = array_unique(array_map(static fn (DashboardRow $r): string => $r->module, $rows));

        $oldestSnapshot = null;
        foreach ($snapshots as $snapshot) {
            if ($oldestSnapshot === null || $snapshot->fetchedAt < $oldestSnapshot->fetchedAt) {
                $oldestSnapshot = $snapshot;
            }
        }

        $output->writeln('');
        $output->writeln(sprintf(
            '<fg=gray>%d open MRs · %d %s · cached %s · --refresh to update</>',
            \count($mrKeys),
            \count($moduleNames),
            \count($moduleNames) === 1 ? 'module' : 'modules',
            $oldestSnapshot !== null ? $oldestSnapshot->ageLabel(new \DateTimeImmutable()) : 'never',
        ));
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

        return new ModuleSnapshot(
            new \DateTimeImmutable(),
            $projectData,
            $mrData,
            $issueData,
        );
    }

    /**
     * @param list<string> $cells [MODULE, MR, ISSUE, CORE, TITLE, CI, LOCAL, STATUS]
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

        // LOCAL (index 6)
        $fmt[6] = match (true) {
            $cells[6] === 'pass' => '<fg=green>' . $cells[6] . '</>',
            str_starts_with($cells[6], 'fail') => '<fg=red>' . $cells[6] . '</>',
            $cells[6] === '–' || $cells[6] === 'stale' => '<fg=gray>' . $cells[6] . '</>',
            default => $cells[6],
        };

        // STATUS (index 7)
        $fmt[7] = match (true) {
            str_starts_with($cells[7], 'READY-AUTO') => '<fg=green>' . $cells[7] . '</>',
            str_starts_with($cells[7], 'REVIEW') => '<fg=yellow>' . $cells[7] . '</>',
            str_starts_with($cells[7], 'BLOCKED') => '<fg=red>' . $cells[7] . '</>',
            default => $cells[7],
        };

        // Written back by index, so the result is repacked into a list:
        // ColumnTable's colouriser contract is list-in, list-out.
        return array_values($fmt);
    }
}

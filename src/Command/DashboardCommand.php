<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\Module;
use Upkeep\Cockpit\RegistryException;
use Upkeep\Dashboard\DashboardCache;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Dashboard\ModuleSnapshot;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\IssueReference;
use Upkeep\Gate\FastLaneGate;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Results\ResultsCache;

/**
 * One table of every open MR across all registered modules and tracked core
 * versions: MODULE, MR, ISSUE, CORE, TITLE, CI, LOCAL, STATUS.
 *
 * Remote data (GitLab MRs, drupal.org issues) is cached per module under
 * `<cockpit>/cache/dashboard/`. On repeat runs the cached snapshot is used;
 * pass `--refresh` to re-fetch all modules, or `--refresh=<module>` for one.
 * Local check results and gate verdicts are always resolved fresh.
 *
 * NOTE for application wiring: the --version option collides with Symfony
 * Console's built-in application-level --version/-V. The hosting Application
 * must drop that default option before running this command (same convention
 * as AbstractMrCommand — see its docblock for the snippet).
 */
#[AsCommand(
    name: 'dashboard',
    description: 'Show every open MR across registered modules and core versions with CI, local check, and fast-lane status.',
)]
final class DashboardCommand extends Command
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
        $this->addOption(
            'cockpit',
            null,
            InputOption::VALUE_REQUIRED,
            sprintf('Path to the cockpit directory (defaults to $%s, then the current directory)', Cockpit::ENV_VAR),
        );
        $this->addOption(
            'version',
            null,
            InputOption::VALUE_REQUIRED,
            'Only show rows targeting this core major version (e.g. 11)',
        );
        $this->addOption(
            'refresh',
            null,
            InputOption::VALUE_OPTIONAL,
            'Re-fetch remote data: --refresh for all modules, --refresh=<module> for one',
            false,
        );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle(
            $input,
            $output instanceof ConsoleOutputInterface ? $output->getErrorOutput() : $output,
        );

        $cockpit = Cockpit::resolve($input->getOption('cockpit'));
        try {
            $registry = $cockpit->loadRegistry();
        } catch (RegistryException $e) {
            $io->error($e->getMessage());

            return Command::FAILURE;
        }

        $modules = $registry->modules();
        $versionFilter = $input->getOption('version');
        $versionFilter = $versionFilter === null ? null : (string) $versionFilter;

        $refresh = $input->getOption('refresh');
        $refreshAll = $refresh === null;
        $refreshModule = \is_string($refresh) && $refresh !== '' ? $refresh : null;

        $dashCache = new DashboardCache($cockpit->root . '/cache/dashboard');
        $resultsCache = new ResultsCache($cockpit->root . '/results');
        $gate = new FastLaneGate();
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
            $client = $this->client ?? $this->buildClient($io);
            if ($client === null) {
                return Command::FAILURE;
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

            $project = $snapshot->project();
            $mrs = $snapshot->mergeRequests();
            usort($mrs, static fn (MergeRequest $a, MergeRequest $b): int => $a->iid <=> $b->iid);

            foreach ($mrs as $mr) {
                $cores = $versionFilter === null
                    ? $module->coreVersions
                    : array_values(array_filter($module->coreVersions, static fn (string $c): bool => $c === $versionFilter));

                foreach ($cores as $core) {
                    $local = $resultsCache->latest($name, $mr->iid, $core);
                    $rows[] = DashboardRow::forMergeRequest(
                        $name,
                        $core,
                        $project,
                        $mr,
                        $local,
                        $gate->classify($mr, $core, $local),
                    );
                }
            }
        }

        if ($rows === []) {
            $io->note(
                $versionFilter === null
                    ? 'No open merge requests across the registered modules.'
                    : sprintf('No open merge requests targeting core %s across the registered modules.', $versionFilter),
            );

            return Command::SUCCESS;
        }

        $headers = ['MODULE', 'MR', 'ISSUE', 'CORE', 'TITLE', 'CI', 'LOCAL', 'STATUS'];
        $colWidths = array_map('mb_strlen', $headers);

        $tableData = [];
        foreach ($rows as $row) {
            $cells = $row->toTableCells();
            $nid = $row->mergeRequest !== null
                ? IssueReference::extract($row->mergeRequest->title, $row->mergeRequest->sourceBranch, $row->mergeRequest->description)
                : null;
            $issueCell = $nid !== null ? (string) $nid : '–';

            if ($nid !== null && $row->mergeRequest !== null) {
                $snapshot = $snapshots[$row->module] ?? null;
                $issue = $snapshot?->issue($nid);
                $latestPatch = $issue?->latestPatch();
                if ($latestPatch !== null && $latestPatch->timestamp > 0 && $row->mergeRequest->updatedAt !== null) {
                    $mrUpdated = strtotime($row->mergeRequest->updatedAt);
                    if ($mrUpdated !== false && $latestPatch->timestamp > $mrUpdated) {
                        $issueCell .= ' patch↑';
                    }
                }
            }

            array_splice($cells, 2, 0, [$issueCell]);

            foreach ($cells as $i => $cell) {
                $colWidths[$i] = max($colWidths[$i], mb_strlen($cell));
            }
            $tableData[] = ['raw' => $cells, 'module' => $row->module];
        }

        $gap = 4;

        $headerLine = '';
        foreach ($headers as $i => $h) {
            $headerLine .= str_pad($h, $colWidths[$i] + $gap);
        }
        $output->writeln('<fg=gray>' . rtrim($headerLine) . '</>');
        $output->writeln('');

        $lastModule = null;
        foreach ($tableData as $entry) {
            if ($lastModule !== null && $entry['module'] !== $lastModule) {
                $output->writeln('');
            }
            $lastModule = $entry['module'];

            $fmt = self::colorCells($entry['raw']);
            $line = '';
            foreach ($fmt as $i => $fmtCell) {
                $pad = $colWidths[$i] - mb_strlen($entry['raw'][$i]) + $gap;
                $line .= $fmtCell . str_repeat(' ', $pad);
            }
            $output->writeln(rtrim($line));
        }

        $now = new \DateTimeImmutable();
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
            $oldestSnapshot !== null ? $oldestSnapshot->ageLabel($now) : 'never',
        ));

        return Command::SUCCESS;
    }

    private function fetchModule(GitlabClient $client, DrupalOrgClient $drupal, Module $module): ModuleSnapshot|ApiFailure
    {
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

        return $fmt;
    }

    private function buildClient(SymfonyStyle $io): ?GitlabClient
    {
        $resolver = new TokenResolver();
        $token = $resolver->resolve();
        if ($token === null) {
            $io->error(sprintf(
                'No GitLab token found. Configure one of: %s. (The token is never printed or logged.)',
                $resolver->describeSources(),
            ));

            return null;
        }

        return new GitlabClient(HttpClient::create(), $token);
    }
}

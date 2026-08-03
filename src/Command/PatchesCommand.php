<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Cockpit\Cockpit;
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
use Upkeep\Workflow\ExitCode;

/**
 * Surface drupal.org issues in Needs Review / RTBC status that have no
 * corresponding GitLab merge request — the patch-only contributions that
 * aren't visible in the MR-centric dashboard.
 *
 * Running without a GitLab token is a documented degraded mode, not a
 * failure: the scan proceeds without cross-referencing merge requests, says
 * so once, and still exits 0. Only a missing cockpit, an unreadable registry
 * or an unregistered --module is an infrastructure failure.
 */
#[AsCommand(
    name: 'patches',
    description: 'Show drupal.org issues with patches but no merge request.',
)]
final class PatchesCommand extends UpkeepCommand
{
    public function __construct(
        private readonly ?DrupalOrgClient $drupalClient = null,
        private readonly ?GitlabClient $gitlabClient = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addCockpitOption();
        $this->addOption(
            'module',
            null,
            InputOption::VALUE_REQUIRED,
            'Only scan this module (default: all registered modules)',
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

        $drupal = $this->drupalClient ?? new DrupalOrgClient(HttpClient::create());
        $mrNids = $this->collectMrIssueNids($modules, $cockpit, $io);

        $orphans = [];
        $scannedStatuses = [IssueStatus::NeedsReview, IssueStatus::Rtbc];

        ksort($modules);
        foreach ($modules as $name => $module) {
            $issues = $drupal->projectIssues($name, $scannedStatuses);
            $linked = $mrNids[$name] ?? [];

            foreach ($issues as $issue) {
                if (!\in_array($issue->nid, $linked, true)) {
                    $orphans[] = ['module' => $name, 'issue' => $issue];
                }
            }
        }

        if ($orphans === []) {
            $io->success('No orphan issues found — all Needs Review / RTBC issues have corresponding MRs.');

            return ExitCode::OK;
        }

        $this->renderTable($orphans, $output);

        $moduleNames = array_unique(array_column($orphans, 'module'));
        $output->writeln('');
        $output->writeln(sprintf(
            '<fg=gray>%d %s without MRs · %d %s · statuses: %s</>',
            \count($orphans),
            \count($orphans) === 1 ? 'issue' : 'issues',
            \count($moduleNames),
            \count($moduleNames) === 1 ? 'module' : 'modules',
            implode(', ', array_map(static fn (IssueStatus $s): string => $s->shortLabel(), $scannedStatuses)),
        ));

        return ExitCode::OK;
    }

    /**
     * @param list<array{module: string, issue: Issue}> $orphans
     */
    private function renderTable(array $orphans, OutputInterface $output): void
    {
        $rows = [];
        foreach ($orphans as $entry) {
            $issue = $entry['issue'];
            $latest = $issue->latestPatch();
            $rows[] = [
                $entry['module'],
                '#' . $issue->nid,
                $issue->status->shortLabel(),
                $issue->patchCount() > 0 ? (string) $issue->patchCount() : '–',
                $latest !== null ? DashboardRow::truncate($latest->name, 30) : '–',
                DashboardRow::truncate($issue->title),
            ];
        }

        ColumnTable::render(
            $output,
            ['MODULE', 'ISSUE', 'STATUS', 'PATCHES', 'LATEST PATCH', 'TITLE'],
            $rows,
            self::colorCells(...),
        );
    }

    /**
     * @param list<string> $cells [MODULE, ISSUE, STATUS, PATCHES, LATEST PATCH, TITLE]
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

        return $fmt;
    }

    /**
     * @param array<string, Module> $modules
     * @return array<string, list<int>>
     */
    private function collectMrIssueNids(array $modules, Cockpit $cockpit, SymfonyStyle $io): array
    {
        $dashCache = new DashboardCache($cockpit->dashboardCachePath());
        $nids = [];
        $gitlabClient = null;
        $gitlabAttempted = false;

        foreach ($modules as $name => $module) {
            $snapshot = $dashCache->load($name);
            if ($snapshot !== null) {
                $nids[$name] = self::extractIssueNids($snapshot->mergeRequests());
                continue;
            }

            if ($gitlabClient === null && !$gitlabAttempted) {
                $gitlabAttempted = true;
                $gitlabClient = $this->gitlabClient ?? $this->buildGitlabClient($io);
            }
            if ($gitlabClient === null) {
                $io->note(
                    'No dashboard cache and no GitLab token — showing all matching issues without '
                    . 'cross-referencing MRs.',
                );
                $nids[$name] = [];
                continue;
            }

            $project = $gitlabClient->project($module->project);
            if ($project instanceof ApiFailure) {
                $nids[$name] = [];
                continue;
            }

            $list = $gitlabClient->openMergeRequests($project);
            if ($list instanceof ApiFailure) {
                $nids[$name] = [];
                continue;
            }

            $nids[$name] = self::extractIssueNids($list->all());
        }

        return $nids;
    }

    /**
     * @param list<MergeRequest> $mrs
     * @return list<int>
     */
    private static function extractIssueNids(array $mrs): array
    {
        $nids = [];
        foreach ($mrs as $mr) {
            $nid = IssueReference::extract($mr->title, $mr->sourceBranch, $mr->description);
            if ($nid !== null) {
                $nids[] = $nid;
            }
        }

        return $nids;
    }

    private function buildGitlabClient(SymfonyStyle $io): ?GitlabClient
    {
        $resolver = GitlabClientFactory::resolver($io);
        $token = $resolver->resolve();
        if ($token === null) {
            $io->warning(GitlabClientFactory::missingTokenMessage($resolver));

            return null;
        }

        return new GitlabClient(HttpClient::create(), $token);
    }
}

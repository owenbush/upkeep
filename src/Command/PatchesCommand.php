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
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueReference;
use Upkeep\Drupal\IssueStatus;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\TokenResolver;

/**
 * Surface drupal.org issues in Needs Review / RTBC status that have no
 * corresponding GitLab merge request — the patch-only contributions that
 * aren't visible in the MR-centric dashboard.
 */
#[AsCommand(
    name: 'patches',
    description: 'Show drupal.org issues with patches but no merge request.',
)]
final class PatchesCommand extends Command
{
    public function __construct(
        private readonly ?DrupalOrgClient $drupalClient = null,
        private readonly ?GitlabClient $gitlabClient = null,
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
            'module',
            null,
            InputOption::VALUE_REQUIRED,
            'Only scan this module (default: all registered modules)',
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
        $moduleFilter = $input->getOption('module');
        if ($moduleFilter !== null) {
            $moduleFilter = (string) $moduleFilter;
            if (!isset($modules[$moduleFilter])) {
                $io->error(sprintf('Module "%s" is not registered.', $moduleFilter));

                return Command::FAILURE;
            }
            $modules = [$moduleFilter => $modules[$moduleFilter]];
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

            return Command::SUCCESS;
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

        return Command::SUCCESS;
    }

    /**
     * @param list<array{module: string, issue: Issue}> $orphans
     */
    private function renderTable(array $orphans, OutputInterface $output): void
    {
        $headers = ['MODULE', 'ISSUE', 'STATUS', 'PATCHES', 'LATEST PATCH', 'TITLE'];
        $colWidths = array_map('mb_strlen', $headers);

        $rows = [];
        foreach ($orphans as $entry) {
            $issue = $entry['issue'];
            $latest = $issue->latestPatch();
            $cells = [
                $entry['module'],
                '#' . $issue->nid,
                $issue->status->shortLabel(),
                $issue->patchCount() > 0 ? (string) $issue->patchCount() : '–',
                $latest !== null ? self::truncate($latest->name, 30) : '–',
                self::truncate($issue->title, 44),
            ];

            foreach ($cells as $i => $cell) {
                $colWidths[$i] = max($colWidths[$i], mb_strlen($cell));
            }
            $rows[] = $cells;
        }

        $gap = 4;

        $headerLine = '';
        foreach ($headers as $i => $h) {
            $headerLine .= str_pad($h, $colWidths[$i] + $gap);
        }
        $output->writeln('<fg=gray>' . rtrim($headerLine) . '</>');
        $output->writeln('');

        foreach ($rows as $cells) {
            $fmt = self::colorCells($cells);
            $line = '';
            foreach ($fmt as $i => $fmtCell) {
                $pad = $colWidths[$i] - mb_strlen($cells[$i]) + $gap;
                $line .= $fmtCell . str_repeat(' ', $pad);
            }
            $output->writeln(rtrim($line));
        }
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
        $dashCache = new DashboardCache($cockpit->root . '/cache/dashboard');
        $nids = [];
        $gitlabClient = null;

        foreach ($modules as $name => $module) {
            $snapshot = $dashCache->load($name);
            if ($snapshot !== null) {
                $nids[$name] = self::extractIssueNids($snapshot->mergeRequests());
                continue;
            }

            $gitlabClient ??= $this->gitlabClient ?? $this->buildGitlabClient($io);
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

    private static function truncate(string $text, int $max): string
    {
        return mb_strlen($text) <= $max ? $text : mb_substr($text, 0, $max - 1) . '…';
    }

    private function buildGitlabClient(SymfonyStyle $io): ?GitlabClient
    {
        $token = (new TokenResolver())->resolve();
        if ($token === null) {
            return null;
        }

        return new GitlabClient(HttpClient::create(), $token);
    }
}

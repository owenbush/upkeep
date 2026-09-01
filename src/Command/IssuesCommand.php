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
use Upkeep\Dashboard\DashboardCache;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\IssueStatus;
use Upkeep\Patches\Contribution;
use Upkeep\Patches\ContributionKind;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\MrContextResolver;

/**
 * Every open issue on a module, and what — if anything — has been contributed
 * to it.
 *
 * The counterpart to `patches` and the MR dashboard, and the thing that makes
 * the other two make sense. Both of those start from a *contribution*: a merge
 * request, or a patch somebody posted. That answers "what is waiting for me?"
 * and is silent about "what could I work on?", which on a real module is most
 * of the queue — pathauto carries 93 open issues, of which the
 * contribution-shaped scan (Needs Review and RTBC) sees 42.
 *
 * So this scans every open status, and treats the contribution as a *column*
 * rather than as the price of admission. An Active bug report with nothing
 * attached is the most actionable row on the list: it is unclaimed work.
 */
#[AsCommand(
    name: 'issues',
    description: 'List a module\'s open drupal.org issues and what has been contributed to each.',
)]
final class IssuesCommand extends UpkeepCommand
{
    public function __construct(private readonly ?DrupalOrgClient $drupalClient = null)
    {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addArgument('module', InputArgument::REQUIRED, 'Registered module machine name');
        $this->addCockpitOption();
        $this->addOption(
            'status',
            null,
            InputOption::VALUE_REQUIRED,
            'Only this status: active, review, needs-work, rtbc, postponed (default: every open status)',
        );
        $this->addOption(
            'unclaimed',
            null,
            InputOption::VALUE_NONE,
            'Only issues nobody has contributed to yet — the work that has not started',
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
        $module = MrContextResolver::requireModule(
            $this->modules($cockpit),
            self::stringArgument($input, 'module'),
        );

        $statuses = self::selectedStatuses($input);
        $drupal = $this->drupalClient ?? new DrupalOrgClient(HttpClient::create());

        $io->writeln(sprintf('Reading %s issues from drupal.org ...', $module->name));
        $issues = $drupal->projectIssues($module->name, $statuses);

        // Merge requests come from the dashboard cache rather than a fresh
        // fetch: this command's job is the issue queue, and a cockpit that has
        // never run `dashboard --refresh` should still get its issues rather
        // than a credential error.
        $snapshot = (new DashboardCache($cockpit->dashboardCachePath()))->load($module->name);
        $contributions = Contribution::pair(
            $module->name,
            $issues,
            $snapshot?->mergeRequests() ?? [],
        );

        if ($input->getOption('unclaimed') === true) {
            $contributions = array_values(array_filter(
                $contributions,
                static fn (Contribution $c): bool => $c->kind() === ContributionKind::Nothing,
            ));
        }

        self::reportScanWarnings($io, $drupal);

        if ($contributions === []) {
            $io->success(sprintf('No matching open issues on %s.', $module->name));

            return ExitCode::OK;
        }

        self::renderTable($output, $contributions);
        $output->writeln('');
        $output->writeln(self::summary($contributions, $snapshot === null));

        return ExitCode::OK;
    }

    /**
     * @param list<Contribution> $contributions
     */
    private static function renderTable(OutputInterface $output, array $contributions): void
    {
        // Most actionable first: what awaits a maintainer's verdict, then
        // everything else, newest issue first within each group. An RTBC issue
        // is somebody waiting on you; an Active one is waiting on nobody.
        usort($contributions, static function (Contribution $a, Contribution $b): int {
            $byAttention = ($b->issue->status->needsMaintainer() ? 1 : 0)
                <=> ($a->issue->status->needsMaintainer() ? 1 : 0);

            return $byAttention !== 0 ? $byAttention : $b->issue->nid <=> $a->issue->nid;
        });

        $rows = [];
        foreach ($contributions as $contribution) {
            $issue = $contribution->issue;
            $rows[] = [
                '#' . $issue->nid,
                $issue->status->shortLabel(),
                $issue->priorityLabel() ?? '–',
                self::contributionCell($contribution),
                DashboardRow::truncate($issue->title, 52),
            ];
        }

        ColumnTable::render(
            $output,
            ['ISSUE', 'STATUS', 'PRIORITY', 'CONTRIBUTION', 'TITLE'],
            $rows,
            self::colorCells(...),
        );
    }

    /**
     * What has arrived on the issue, in one cell: the merge request, the patch
     * count, or a dash meaning nobody has started.
     */
    private static function contributionCell(Contribution $contribution): string
    {
        $patches = $contribution->issue->patchCount();
        $mr = $contribution->mergeRequestCell();

        return match (true) {
            $mr !== '–' && $patches > 0 => sprintf('%s, %d patch', $mr, $patches),
            $mr !== '–' => $mr,
            $patches > 0 => sprintf('%d patch', $patches),
            // The word, not a dash: this is the row the command exists to
            // surface, and it goes in the raw cell because ColumnTable
            // measures raw widths and forbids the coloriser from changing a
            // cell's visible length.
            default => 'unclaimed',
        };
    }

    /**
     * @param list<string> $cells [ISSUE, STATUS, PRIORITY, CONTRIBUTION, TITLE]
     * @return list<string>
     */
    private static function colorCells(array $cells): array
    {
        $fmt = $cells;

        $fmt[1] = match ($cells[1]) {
            'RTBC' => '<fg=green>' . $cells[1] . '</>',
            'review' => '<fg=yellow>' . $cells[1] . '</>',
            'active' => '<fg=cyan>' . $cells[1] . '</>',
            default => '<fg=gray>' . $cells[1] . '</>',
        };

        $fmt[2] = match ($cells[2]) {
            'Critical', 'Major' => '<fg=red>' . $cells[2] . '</>',
            '–' => '<fg=gray>–</>',
            default => $cells[2],
        };

        // Highlighted rather than muted: unclaimed work is the point of the
        // command, not an absence.
        $fmt[3] = $cells[3] === 'unclaimed' ? '<fg=cyan>unclaimed</>' : $cells[3];

        return array_values($fmt);
    }

    /**
     * @param list<Contribution> $contributions
     */
    private static function summary(array $contributions, bool $withoutSnapshot): string
    {
        $unclaimed = \count(array_filter(
            $contributions,
            static fn (Contribution $c): bool => $c->kind() === ContributionKind::Nothing,
        ));
        $awaiting = \count(array_filter(
            $contributions,
            static fn (Contribution $c): bool => $c->issue->status->needsMaintainer(),
        ));

        $segments = [
            sprintf('%d open %s', \count($contributions), \count($contributions) === 1 ? 'issue' : 'issues'),
            sprintf('%d awaiting you', $awaiting),
            sprintf('%d unclaimed', $unclaimed),
        ];
        if ($withoutSnapshot) {
            // Said rather than left to look like "no merge requests exist".
            $segments[] = 'no cached MRs — run `upkeep dashboard --refresh=<module>` to fill the CONTRIBUTION column';
        }

        return '<fg=gray>' . implode(' · ', $segments) . '</>';
    }

    /**
     * @return list<IssueStatus>
     */
    private static function selectedStatuses(InputInterface $input): array
    {
        $requested = self::stringOption($input, 'status');
        if ($requested === null) {
            return IssueStatus::open();
        }

        $matched = array_values(array_filter(
            IssueStatus::open(),
            static fn (IssueStatus $s): bool => str_replace(' ', '-', $s->shortLabel()) === strtolower($requested),
        ));

        return $matched === [] ? IssueStatus::open() : $matched;
    }
}

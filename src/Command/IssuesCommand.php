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
use Upkeep\Cockpit\Module;
use Upkeep\Cockpit\ModuleResolution;
use Upkeep\Dashboard\DashboardCache;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\IssueStatus;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Gitlab\MergeRequest;
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
    /**
     * @param ?GitlabClient $gitlabClient injected in tests; built read-only
     *                                    otherwise, which needs no credential
     */
    public function __construct(
        private readonly ?DrupalOrgClient $drupalClient = null,
        private readonly ?GitlabClient $gitlabClient = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->setHelp(<<<'HELP'
            Every open issue on a module, whether or not anyone has contributed to it —
            the difference from <info>dashboard</info> and <info>patches</info>, which both start from a
            contribution.

              <info>upkeep issues pathauto</info>              every open issue
              <info>upkeep issues pathauto --unclaimed</info>  only what nobody has started
              <info>upkeep issues pathauto --status=active</info>

            The merge-request column is filled from the dashboard cache, so run
            <info>upkeep dashboard --refresh=<module></info> if it is empty.
            To begin work on one: <info>upkeep start <module> <issue></info>.
            HELP);

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
        $module = $this->resolveModule(
            $cockpit,
            $this->modules($cockpit),
            self::stringArgument($input, 'module'),
        );

        $statuses = self::selectedStatuses($input);
        $drupal = $this->drupalClient ?? new DrupalOrgClient(HttpClient::create());

        $io->writeln(sprintf('Reading %s issues from drupal.org ...', $module->name));
        $issues = $drupal->projectIssues($module->name, $statuses);

        // Merge requests come from the dashboard cache when there is one, and
        // from a live read when there is not.
        //
        // It used to be cache-only, to spare "a cockpit that has never run
        // `dashboard --refresh`" a credential error. Anonymous reads removed
        // that constraint, and the watchlist split made the gap harmful: an
        // unwatched module has no snapshot and cannot be given one, since
        // `dashboard --refresh` surveys the watchlist — so every issue looked
        // unclaimed and NEXT said `upkeep start` on work somebody had already
        // done.
        $snapshot = (new DashboardCache($cockpit->dashboardCachePath()))->load($module->name);
        [$mergeRequests, $forkNids] = $snapshot !== null
            ? [$snapshot->mergeRequests(), $snapshot->forkNids]
            : $this->readMergeRequests($module, $io);

        $contributions = Contribution::pair($module->name, $issues, $mergeRequests, $forkNids);

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
        $output->writeln(self::summary(
            $contributions,
            $mergeRequests === [] && $snapshot === null,
            $module->name,
            ModuleResolution::isRegistered($this->modules($cockpit), $module->name),
        ));

        return ExitCode::OK;
    }

    /**
     * The module's open merge requests, read live.
     *
     * Only reached when there is no snapshot to read them from. Needs no
     * credential — git.drupalcode.org serves a public project's merge requests
     * and forks anonymously — which is what makes this affordable at all;
     * until reading without a token was possible, doing it here would have
     * turned `issues` into a command that demands a PAT.
     *
     * Every failure yields nothing and lets the run continue. The issue queue
     * is this command's subject; the contribution column is context, and
     * losing context is not worth losing the list for. The footer says so.
     *
     * The fork map is fetched with them, because it is **the only thing that
     * pairs a Project Update Bot merge request to its issue** — those are
     * titled "Automated Project Update Bot fixes" and mention their issue in a
     * way `extractOwning()` rejects by design. Without it a compatibility
     * issue with a bot MR on it reads as untouched, which on a module with
     * many of them is most of the difference.
     *
     * @return array{list<MergeRequest>, array<int, int>}
     */
    private function readMergeRequests(Module $module, SymfonyStyle $io): array
    {
        $client = GitlabClientFactory::readOnlyOr(
            $this->gitlabClient,
            GitlabClientFactory::resolver($io),
            $io->note(...),
        );

        $project = $client->project($module->project);
        if ($project instanceof ApiFailure) {
            return [[], []];
        }

        $list = $client->openMergeRequests($project);
        if ($list instanceof ApiFailure) {
            return [[], []];
        }

        $forkNids = $client->issueForkNids($project);

        return [$list->all(), $forkNids instanceof ApiFailure ? [] : $forkNids];
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
                DashboardRow::truncate($issue->title, 44),
                self::nextCommand($contribution),
            ];
        }

        ColumnTable::render(
            $output,
            ['ISSUE', 'STATUS', 'PRIORITY', 'CONTRIBUTION', 'TITLE', 'NEXT'],
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
     * The command for that row.
     *
     * An unclaimed issue is the one this view exists to surface, so it gets
     * the verb that starts work. Anything already carrying a contribution
     * points at whichever command evaluates that contribution — the same
     * commands the dashboard's NEXT column names, so the two views never
     * suggest different things about the same issue.
     */
    private static function nextCommand(Contribution $contribution): string
    {
        $module = $contribution->module;
        $nid = $contribution->issue->nid;
        $substantive = $contribution->substantiveMergeRequests();

        return match (true) {
            $substantive !== [] => sprintf('upkeep check %s %d', $module, $substantive[0]->iid),
            $contribution->issue->patchCount() > 0 => sprintf('upkeep patch:check %s %d', $module, $nid),
            default => sprintf('upkeep start %s %d', $module, $nid),
        };
    }

    /**
     * @param list<string> $cells [ISSUE, STATUS, PRIORITY, CONTRIBUTION, TITLE, NEXT]
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

        $fmt[5] = '<fg=cyan>' . $cells[5] . '</>';

        return array_values($fmt);
    }

    /**
     * @param list<Contribution> $contributions
     * @param bool               $watched whether the dashboard surveys this
     *                                    module, which decides whether the
     *                                    suggestion below is one it would
     *                                    accept
     */
    private static function summary(
        array $contributions,
        bool $withoutSnapshot,
        string $module,
        bool $watched,
    ): string {
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
            // Said rather than left to look like "no merge requests exist" —
            // and the suggestion has to be one that works for *this* module.
            // The dashboard surveys the watchlist, so pointing an unwatched
            // module at `--refresh` would send somebody to a command that
            // refuses them.
            $segments[] = $watched
                ? 'no cached MRs — run `upkeep dashboard --refresh=' . $module . '` to fill the CONTRIBUTION column'
                : 'no cached MRs — ' . $module . ' is not on the dashboard\'s watchlist, so the CONTRIBUTION '
                    . 'column stays empty; `upkeep modules:add` to watch it';
        }

        return '<fg=gray>' . implode(' · ', $segments) . "</>\n"
            . '<fg=gray>Run the command in NEXT for any row · upkeep explain <term> for what a column means</>';
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

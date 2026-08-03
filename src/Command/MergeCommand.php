<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\RegistryException;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Dashboard\RowAssembler;
use Upkeep\Gate\FastLaneGate;
use Upkeep\Gate\GateStatus;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\EndpointClosed;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Results\ResultsCache;

/**
 * The fast-lane merge command: presents the current READY-AUTO rows one at a
 * time and, on an explicit per-MR human approval, performs that single merge
 * via the GitLab client — one individual action the user could have done in
 * the browser.
 *
 * DA-policy stance, enforced structurally: one prompt, one approval, one API
 * call. There is no batch mode and no flag that merges without prompting.
 * Immediately before each merge the MR is re-fetched with an unmemoized
 * client (freshness re-check): head-SHA drift, CI regression, a state
 * change, or a new draft marker demotes the row with its reasons instead of
 * merging. The merge call itself carries the expected head SHA so GitLab
 * rejects races the re-check cannot see.
 *
 * Rows not READY-AUTO are listed in a non-actionable summary only — they are
 * never offered for merge. When the instance refuses API merges
 * (EndpointClosed), the approved action prints the exact browser merge URL
 * and the row is marked handled-manually (the documented degraded path).
 */
#[AsCommand(
    name: 'merge',
    description: 'Fast-lane merge: prompt per READY-AUTO merge request and merge only on an explicit per-MR approval.',
)]
final class MergeCommand extends Command
{
    /** @param ?GitlabClient $client injected in tests; built from the resolved token otherwise */
    public function __construct(private readonly ?GitlabClient $client = null)
    {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addOption(
            'fast-lane',
            null,
            InputOption::VALUE_NONE,
            'Required: run the fast-lane loop (the only mode; named explicitly because it performs merges)',
        );
        $this->addOption(
            'cockpit',
            null,
            InputOption::VALUE_REQUIRED,
            sprintf('Path to the cockpit directory (defaults to $%s, then the current directory)', Cockpit::ENV_VAR),
        );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        if (!$input->getOption('fast-lane')) {
            $io->error(
                'The merge command only operates in fast-lane mode; re-run as `upkeep merge --fast-lane`. It will '
                . 'still prompt per MR — the flag names the workflow, it never skips approval.',
            );

            return Command::INVALID;
        }

        $cockpit = Cockpit::resolve($input->getOption('cockpit'));
        try {
            $registry = $cockpit->loadRegistry();
        } catch (RegistryException $e) {
            $io->error($e->getMessage());

            return Command::FAILURE;
        }

        $client = $this->client ?? $this->buildClient($io);
        if ($client === null) {
            return Command::FAILURE;
        }

        $cache = new ResultsCache($cockpit->root . '/results');
        $assembler = new RowAssembler($client, $cache);
        $rows = $assembler->assemble($registry->modules());

        $ready = array_values(array_filter($rows, static fn (DashboardRow $row): bool => $row->isReadyAuto()));
        $rest = array_values(array_filter($rows, static fn (DashboardRow $row): bool => !$row->isReadyAuto()));

        if ($rest !== []) {
            $io->section('Needs a human (never offered for merge)');
            foreach ($rest as $row) {
                $io->writeln(self::describeNonActionable($row));
            }
        }

        if ($ready === []) {
            $io->newLine();
            $io->writeln('No READY-AUTO rows — nothing eligible for fast-lane merge.');

            return Command::SUCCESS;
        }

        $tally = ['merged' => 0, 'handed-to-browser' => 0, 'skipped' => 0, 'demoted' => 0, 'failed' => 0];

        if (!$input->isInteractive()) {
            // No terminal means no explicit per-MR affirmative is possible,
            // and without one nothing merges — ever. The READY-AUTO rows are
            // reported as skipped instead of silently consuming defaults.
            $io->newLine();
            $io->writeln(
                'Approval requires an interactive terminal: every merge needs an explicit per-MR "merge" answer, '
                . 'so a non-interactive run merges nothing.',
            );
            foreach ($ready as $row) {
                \assert($row->mergeRequest !== null);
                $tally['skipped']++;
                $io->writeln(sprintf(
                    'Skipped %s !%d (no interactive approval possible).',
                    $row->module,
                    $row->mergeRequest->iid,
                ));
            }
            $this->renderSummary($io, $tally);

            return Command::SUCCESS;
        }

        foreach ($ready as $row) {
            \assert($row->project !== null && $row->mergeRequest !== null);
            $this->renderContext($io, $row);

            $action = $io->choice(
                sprintf('Fast-lane action for %s !%d', $row->module, $row->mergeRequest->iid),
                ['merge', 'skip', 'quit'],
                'skip',
            );
            if ($action === 'quit') {
                $io->writeln('Quit — leaving the remaining rows untouched.');
                break;
            }
            if ($action !== 'merge') {
                $tally['skipped']++;
                $io->writeln(sprintf('Skipped %s !%d.', $row->module, $row->mergeRequest->iid));
                continue;
            }

            $this->mergeOne($io, $client, $cache, $row, $tally);
        }

        $this->renderSummary($io, $tally);

        return Command::SUCCESS;
    }

    /**
     * One approved merge: the freshness re-check against a live re-fetch,
     * then the single-action API call with the head-SHA guard, and the
     * typed-failure handling around it.
     *
     * @param array<string, int> $tally
     */
    private function mergeOne(
        SymfonyStyle $io,
        GitlabClient $client,
        ResultsCache $cache,
        DashboardRow $row,
        array &$tally,
    ): void {
        \assert($row->project !== null && $row->mergeRequest !== null);
        $iid = $row->mergeRequest->iid;

        // Freshness re-check: re-fetch the MR (and its head pipeline) with an
        // unmemoized client so we see it as it is NOW, not as classified at
        // row-assembly time. Anything moved → demote with reasons, no merge.
        $fresh = $client->fresh()->mergeRequest($row->project, $iid);
        if ($fresh instanceof ApiFailure) {
            $tally['demoted']++;
            $io->writeln(sprintf(
                'Demoted %s !%d — freshness re-check failed (%s); not merging.',
                $row->module,
                $iid,
                $fresh->message,
            ));

            return;
        }

        $reasons = [];
        if ($fresh->state !== 'opened') {
            $reasons[] = 'state-changed:' . $fresh->state;
        }
        if ($fresh->headSha !== $row->mergeRequest->headSha) {
            $reasons[] = 'sha-drift';
        }
        // Re-classify against the fresh MR and re-read local evidence: this
        // catches CI regression, a new draft marker, and stale local results
        // with the exact same conservative logic that admitted the row.
        $verdict = (new FastLaneGate())->classify($fresh, $row->core, $cache->latest($row->module, $iid, $row->core));
        if ($verdict->status !== GateStatus::ReadyAuto) {
            $reasons = array_merge($reasons, $verdict->reasons);
        }
        if ($reasons !== []) {
            $tally['demoted']++;
            $io->writeln(sprintf(
                'Demoted %s !%d to REVIEW (%s) — not merging.',
                $row->module,
                $iid,
                implode(', ', array_values(array_unique($reasons))),
            ));

            return;
        }

        $result = $client->merge($row->project, $iid, $fresh->headSha);
        if ($result instanceof MergeRequest) {
            $tally['merged']++;
            $io->writeln(sprintf('Merged %s !%d (state: %s).', $row->module, $iid, $result->state));

            return;
        }

        if ($result instanceof EndpointClosed) {
            // The documented degraded path: the instance refuses API merges,
            // so the approved action becomes handing the human the exact
            // browser URL to perform it there.
            $tally['handed-to-browser']++;
            $io->writeln(sprintf('GitLab refuses API merges here (HTTP %d).', $result->status));
            $io->writeln(sprintf('Merge in the browser: %s', $result->browserUrl));
            $io->writeln(sprintf('Marked %s !%d handled-manually.', $row->module, $iid));

            return;
        }

        $tally['failed']++;
        $io->writeln(sprintf('Merge failed for %s !%d: %s', $row->module, $iid, $result->message));
    }

    private function renderContext(SymfonyStyle $io, DashboardRow $row): void
    {
        \assert($row->mergeRequest !== null);
        $mr = $row->mergeRequest;
        $io->section(sprintf('%s !%d (core %s) — READY-AUTO', $row->module, $mr->iid, $row->core));
        $io->writeln('  Title:  ' . $mr->title);
        $io->writeln(sprintf('  Branch: %s -> %s', $mr->sourceBranch, $mr->targetBranch));
        $io->writeln('  Head:   ' . ($mr->headSha ?? 'unknown'));
        $io->writeln(sprintf(
            '  CI:     %s%s',
            $row->ciCell(),
            $mr->headPipeline !== null && $mr->headPipeline->webUrl !== '' ? ' — ' . $mr->headPipeline->webUrl : '',
        ));
        $io->writeln(sprintf(
            '  Local:  %s%s',
            $row->localCell(),
            $row->local !== null ? ' (recorded ' . $row->local->recordedAt->format(\DateTimeInterface::ATOM) . ')' : '',
        ));
        $io->writeln('  URL:    ' . $mr->webUrl);
    }

    /** @param array<string, int> $tally */
    private function renderSummary(SymfonyStyle $io, array $tally): void
    {
        $io->section('Fast-lane summary');
        $io->writeln(sprintf('  Merged: %d', $tally['merged']));
        $io->writeln(sprintf('  Handed to browser: %d', $tally['handed-to-browser']));
        $io->writeln(sprintf('  Skipped: %d', $tally['skipped']));
        $io->writeln(sprintf('  Demoted: %d', $tally['demoted']));
        $io->writeln(sprintf('  Failed: %d', $tally['failed']));
    }

    private static function describeNonActionable(DashboardRow $row): string
    {
        if ($row->mergeRequest === null) {
            return sprintf('  %s: merge requests unavailable — %s', $row->module, $row->statusCell());
        }

        return sprintf(
            '  %s !%d (core %s): %s — %s',
            $row->module,
            $row->mergeRequest->iid,
            $row->core,
            $row->statusCell(),
            DashboardRow::truncate($row->mergeRequest->title),
        );
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

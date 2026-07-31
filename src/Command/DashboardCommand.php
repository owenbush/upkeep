<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Helper\Table;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\Module;
use Upkeep\Cockpit\RegistryException;
use Upkeep\Gate\FastLaneGate;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\EndpointClosed;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\NotFound;
use Upkeep\Gitlab\Pipeline;
use Upkeep\Gitlab\PipelineStatus;
use Upkeep\Gitlab\RateLimited;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Gitlab\TransportError;
use Upkeep\Results\CachedResult;
use Upkeep\Results\ResultsCache;

/**
 * One table of every open MR across all registered modules and tracked core
 * versions: MODULE, MR, CORE, TITLE, CI (live from GitLab), LOCAL (cached
 * check results), STATUS (fast-lane gate verdict).
 *
 * Strictly read-only aggregation: registry + GET-backed client calls + the
 * results cache. It never provisions environments, never runs checks, and
 * never mutates anything — the gate's verdicts are consumed by the human (and
 * by task 14's merge command), not acted on here.
 *
 * Thin wiring by design: classification lives in FastLaneGate (exhaustively
 * unit-tested), staleness semantics in ResultsCache/CachedResult, typed
 * failures in GitlabClient. This command assembles rows and renders a table;
 * its tests cover row assembly, the --version filter, and failure cells.
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
    /** @param ?GitlabClient $client injected in tests; built from the resolved token otherwise */
    public function __construct(private readonly ?GitlabClient $client = null)
    {
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
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        // Diagnostics go to stderr; stdout carries only the table.
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

        $client = $this->client ?? $this->buildClient($io);
        if ($client === null) {
            return Command::FAILURE;
        }

        $cache = new ResultsCache($cockpit->root . '/results');
        $gate = new FastLaneGate();
        $versionFilter = $input->getOption('version');
        $versionFilter = $versionFilter === null ? null : (string) $versionFilter;

        $rows = [];
        $modules = $registry->modules();
        ksort($modules);
        foreach ($modules as $module) {
            foreach ($this->moduleRows($client, $cache, $gate, $module, $versionFilter) as $row) {
                $rows[] = $row;
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

        $table = new Table($output);
        $table->setHeaders(['MODULE', 'MR', 'CORE', 'TITLE', 'CI', 'LOCAL', 'STATUS']);
        $table->setRows($rows);
        $table->render();

        return Command::SUCCESS;
    }

    /**
     * Rows for one module: every open MR x every tracked (and not filtered
     * out) core version, sorted by MR iid. Typed client failures become
     * explicit cell states, never crashes.
     *
     * @return list<list<string>>
     */
    private function moduleRows(
        GitlabClient $client,
        ResultsCache $cache,
        FastLaneGate $gate,
        Module $module,
        ?string $versionFilter,
    ): array {
        $cores = $versionFilter === null
            ? $module->coreVersions
            : array_values(array_filter($module->coreVersions, static fn (string $core): bool => $core === $versionFilter));
        if ($cores === []) {
            return [];
        }

        $project = $client->project($module->project);
        if ($project instanceof ApiFailure) {
            return [self::moduleFailureRow($module->name, $project)];
        }

        $list = $client->openMergeRequests($project);
        if ($list instanceof ApiFailure) {
            return [self::moduleFailureRow($module->name, $list)];
        }

        $mrs = $list->all();
        usort($mrs, static fn (MergeRequest $a, MergeRequest $b): int => $a->iid <=> $b->iid);

        $rows = [];
        foreach ($mrs as $listed) {
            // The list payload lacks head_pipeline; the single-MR endpoint
            // provides it (memoized by the client). If that fetch fails, fall
            // back to the listed data: the row still renders, the CI cell
            // carries the failure state, and the gate — seeing no pipeline —
            // conservatively denies READY-AUTO.
            $detail = $client->mergeRequest($project, $listed->iid);
            $ciFailure = null;
            if ($detail instanceof ApiFailure) {
                $ciFailure = $detail;
                $detail = $listed;
            }

            foreach ($cores as $core) {
                $local = $cache->latest($module->name, $detail->iid, $core);
                $rows[] = [
                    $module->name,
                    (string) $detail->iid,
                    $core,
                    self::truncate($detail->title),
                    $ciFailure === null ? self::ciCell($detail->headPipeline) : self::failureCell($ciFailure),
                    self::localCell($local, $detail->headSha),
                    $gate->classify($detail, $core, $local)->describe(),
                ];
            }
        }

        return $rows;
    }

    /**
     * A module whose MRs cannot be listed still gets a visible row — the
     * failure state in place of data, never a crash or a silent omission.
     *
     * @return list<string>
     */
    private static function moduleFailureRow(string $module, ApiFailure $failure): array
    {
        $cell = self::failureCell($failure);

        return [$module, '-', '-', '(merge requests unavailable)', $cell, '-', $cell];
    }

    /** Compact, explicit cell state for a typed client failure, e.g. "n/a (403)". */
    private static function failureCell(ApiFailure $failure): string
    {
        return 'n/a (' . match (true) {
            $failure instanceof EndpointClosed => (string) $failure->status,
            $failure instanceof NotFound => '404',
            $failure instanceof RateLimited => 'rate-limited',
            $failure instanceof TransportError => $failure->status === null ? 'transport' : (string) $failure->status,
            default => 'error',
        } . ')';
    }

    private static function ciCell(?Pipeline $pipeline): string
    {
        if ($pipeline === null) {
            return '-';
        }

        return match (true) {
            $pipeline->status->isGreen() => 'ok',
            $pipeline->status === PipelineStatus::Failed => 'fail',
            default => $pipeline->rawStatus !== '' ? $pipeline->rawStatus : $pipeline->status->value,
        };
    }

    /**
     * LOCAL column: latest cached check result for the (module, MR, core) —
     * "-" when never checked, "stale" when recorded against an older head
     * SHA, otherwise ok / fail (failing check names).
     */
    private static function localCell(?CachedResult $local, ?string $headSha): string
    {
        if ($local === null) {
            return '-';
        }
        if ($headSha === null || $local->sha !== $headSha) {
            return 'stale';
        }
        if ($local->result->allPassed()) {
            return 'ok';
        }

        $failed = array_map(
            static fn ($failure): string => $failure->type->value,
            $local->result->failures(),
        );

        return 'fail (' . implode(', ', $failed) . ')';
    }

    private static function truncate(string $title, int $max = 44): string
    {
        return mb_strlen($title) <= $max ? $title : mb_substr($title, 0, $max - 1) . '…';
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

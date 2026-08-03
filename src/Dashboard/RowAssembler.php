<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Cockpit\Module;
use Upkeep\Gate\FastLaneGate;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Results\ResultsCache;

/**
 * Shared row-assembly service: turns the registry's modules into the one
 * canonical list of dashboard rows (every open MR x every tracked core
 * version, classified by the fast-lane gate).
 *
 * Extracted from DashboardCommand so the fast-lane merge command consumes the
 * exact same classification pipeline instead of re-deriving status. Strictly
 * read-only: registry data + GET-backed client calls + the results cache.
 * Typed client failures become explicit row states, never crashes.
 */
final readonly class RowAssembler
{
    public function __construct(
        private GitlabClient $client,
        private ResultsCache $cache,
        private FastLaneGate $gate = new FastLaneGate(),
    ) {
    }

    /**
     * @param array<string, Module> $modules       the registry's modules
     * @param ?string               $versionFilter only rows targeting this core major, when given
     *
     * @return list<DashboardRow> ordered by module name, then MR iid
     */
    public function assemble(array $modules, ?string $versionFilter = null): array
    {
        ksort($modules);

        $rows = [];
        foreach ($modules as $module) {
            foreach ($this->moduleRows($module, $versionFilter) as $row) {
                $rows[] = $row;
            }
        }

        return $rows;
    }

    /**
     * Rows for one module: every open MR x every tracked (and not filtered
     * out) core version, sorted by MR iid.
     *
     * @return list<DashboardRow>
     */
    private function moduleRows(Module $module, ?string $versionFilter): array
    {
        $cores = $versionFilter === null
            ? $module->coreVersions
            : array_values(array_filter(
                $module->coreVersions,
                static fn (string $core): bool => $core === $versionFilter,
            ));
        if ($cores === []) {
            return [];
        }

        $project = $this->client->project($module->project);
        if ($project instanceof ApiFailure) {
            return [DashboardRow::forModuleFailure($module->name, $project)];
        }

        $list = $this->client->openMergeRequests($project);
        if ($list instanceof ApiFailure) {
            return [DashboardRow::forModuleFailure($module->name, $list)];
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
            $detail = $this->client->mergeRequest($project, $listed->iid);
            $ciFailure = null;
            if ($detail instanceof ApiFailure) {
                $ciFailure = $detail;
                $detail = $listed;
            }

            foreach ($cores as $core) {
                $local = $this->cache->latest($module->name, $detail->iid, $core);
                $rows[] = DashboardRow::forMergeRequest(
                    $module->name,
                    $core,
                    $project,
                    $detail,
                    $local,
                    $this->gate->classify($detail, $core, $local),
                    $ciFailure,
                );
            }
        }

        return $rows;
    }
}

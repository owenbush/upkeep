<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Cockpit\Module;
use Upkeep\Gate\FastLaneGate;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Results\ResultsCache;

/**
 * Shared row-assembly service: fetches each registered module's project and
 * open merge requests from GitLab, then hands them to RowFactory — the one
 * canonical classification pipeline the dashboard also uses.
 *
 * Strictly read-only: registry data + GET-backed client calls + the results
 * cache. Typed client failures become explicit row states, never crashes.
 */
final readonly class RowAssembler
{
    private RowFactory $factory;

    public function __construct(
        private GitlabClient $client,
        ResultsCache $cache,
        FastLaneGate $gate = new FastLaneGate(),
    ) {
        $this->factory = new RowFactory($cache, $gate);
    }

    /**
     * @param array<string, Module> $modules       the registry's modules
     * @param ?string               $versionFilter gather evidence for this core only, when given
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
     * Rows for one module: one per open merge request.
     *
     * No snapshot, so no issues and nothing to group by — every merge request
     * is its own row here, where the dashboard would gather an issue's onto
     * one. The *classification* is identical (same gate, same evidence, same
     * cores), which is the property that matters: the fast lane prompts per
     * merge request, so a row per merge request is exactly what it wants.
     *
     * @return list<DashboardRow>
     */
    private function moduleRows(Module $module, ?string $versionFilter): array
    {
        if (RowFactory::cores($module, $versionFilter) === []) {
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

        $mergeRequests = [];
        $ciFailures = [];
        foreach ($list->all() as $listed) {
            // The list payload lacks head_pipeline; the single-MR endpoint
            // provides it (memoized by the client). If that fetch fails, fall
            // back to the listed data: the row still renders, the CI cell
            // carries the failure state, and the gate — seeing no pipeline —
            // conservatively denies READY-AUTO.
            $detail = $this->client->mergeRequest($project, $listed->iid);
            if ($detail instanceof ApiFailure) {
                $ciFailures[$listed->iid] = $detail;
                $detail = $listed;
            }
            $mergeRequests[] = $detail;
        }

        return $this->factory->rows($module, $project, $mergeRequests, $versionFilter, $ciFailures);
    }
}

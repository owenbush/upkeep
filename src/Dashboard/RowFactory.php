<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Cockpit\Module;
use Upkeep\Gate\FastLaneGate;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Project;
use Upkeep\Results\ResultsCache;

/**
 * The one classification pipeline: (module, project, merge requests) →
 * dashboard rows, expanded across the module's tracked core versions and
 * classified by the fast-lane gate.
 *
 * It takes merge requests it did not fetch, precisely so both consumers can
 * use it: RowAssembler feeds it live client data, and the dashboard feeds it
 * its cached per-module snapshot. Neither re-derives status — a drift here
 * would misclassify merge eligibility, which is the one thing FastLaneGate
 * says must not happen.
 */
final readonly class RowFactory
{
    public function __construct(
        private ResultsCache $cache,
        private FastLaneGate $gate = new FastLaneGate(),
    ) {
    }

    /**
     * @param list<MergeRequest>      $mergeRequests open MRs, in any order
     * @param ?string                 $versionFilter only rows targeting this core major, when given
     * @param array<int, ApiFailure>  $ciFailures    detail-fetch failure keyed by MR iid, when the
     *                                               row fell back to listed data
     *
     * @return list<DashboardRow> ordered by MR iid, then tracked core version
     */
    public function rows(
        Module $module,
        Project $project,
        array $mergeRequests,
        ?string $versionFilter = null,
        array $ciFailures = [],
    ): array {
        $cores = self::cores($module, $versionFilter);
        if ($cores === []) {
            return [];
        }

        usort($mergeRequests, static fn (MergeRequest $a, MergeRequest $b): int => $a->iid <=> $b->iid);

        $rows = [];
        foreach ($mergeRequests as $mergeRequest) {
            foreach ($cores as $core) {
                $local = $this->cache->latest($module->name, $mergeRequest->iid, $core);
                $rows[] = DashboardRow::forMergeRequest(
                    $module->name,
                    $core,
                    $project,
                    $mergeRequest,
                    $local,
                    $this->gate->classify($mergeRequest, $core, $local),
                    $ciFailures[$mergeRequest->iid] ?? null,
                );
            }
        }

        return $rows;
    }

    /**
     * The core versions a run covers for this module: all tracked ones, or
     * the single filtered one when it is tracked.
     *
     * @return list<string>
     */
    public static function cores(Module $module, ?string $versionFilter): array
    {
        if ($versionFilter === null) {
            return $module->coreVersions;
        }

        return array_values(array_filter(
            $module->coreVersions,
            static fn (string $core): bool => $core === $versionFilter,
        ));
    }
}

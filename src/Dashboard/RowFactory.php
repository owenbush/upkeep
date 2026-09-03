<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Cockpit\Module;
use Upkeep\Gate\FastLaneGate;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Drupal\IssueReference;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Project;
use Upkeep\Patches\Contribution;
use Upkeep\Results\ResultKey;
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
        ?ModuleSnapshot $snapshot = null,
    ): array {
        $cores = self::cores($module, $versionFilter);
        if ($cores === []) {
            return [];
        }

        usort($mergeRequests, static fn (MergeRequest $a, MergeRequest $b): int => $a->iid <=> $b->iid);

        // Landings are looked up by the *issue* an MR belongs to, not by the
        // MR itself: the row a maintainer is staring at is usually the bot's
        // open draft, while the work that landed came from a different merge
        // request entirely on the same issue.
        $landings = $snapshot === null ? [] : self::landingsByIssue($module->name, $snapshot);

        $rows = [];
        foreach ($mergeRequests as $mergeRequest) {
            foreach ($cores as $core) {
                $local = $this->cache->latest($module->name, ResultKey::mergeRequest($mergeRequest->iid), $core);
                $rows[] = DashboardRow::forMergeRequest(
                    $module->name,
                    $core,
                    $project,
                    $mergeRequest,
                    $local,
                    $this->gate->classify($mergeRequest, $core, $local),
                    $ciFailures[$mergeRequest->iid] ?? null,
                    ...self::landingFor($mergeRequest, $landings, $snapshot),
                );
            }
        }

        return $rows;
    }

    /**
     * Contributions keyed by issue nid, so a row can ask what has landed on
     * the issue it belongs to.
     *
     * @return array<int, Contribution>
     */
    private static function landingsByIssue(string $module, ModuleSnapshot $snapshot): array
    {
        $contributions = Contribution::pair(
            $module,
            $snapshot->patchIssues(),
            [...$snapshot->mergeRequests(), ...$snapshot->mergedMergeRequests()],
            $snapshot->forkNids,
        );

        $byNid = [];
        foreach ($contributions as $contribution) {
            $byNid[$contribution->issue->nid] = $contribution;
        }

        return $byNid;
    }

    /**
     * The landing arguments for one merge request's row.
     *
     * A merge request never reports *itself* as the landing: a merged MR's own
     * row saying "merged" is a tautology, while the useful statement is about
     * the issue — that the work is in, whichever branch carried it.
     *
     * @param array<int, Contribution> $landings
     * @return array{?MergeRequest, bool}
     */
    private static function landingFor(
        MergeRequest $mergeRequest,
        array $landings,
        ?ModuleSnapshot $snapshot,
    ): array {
        $nid = ($snapshot !== null && $mergeRequest->sourceProjectId !== null
            ? ($snapshot->forkNids[$mergeRequest->sourceProjectId] ?? null)
            : null)
            ?? IssueReference::extract(
                $mergeRequest->title,
                $mergeRequest->sourceBranch,
                $mergeRequest->description,
            );

        $contribution = $nid === null ? null : ($landings[$nid] ?? null);
        $landed = $contribution?->landed();

        if ($landed === null || $landed->iid === $mergeRequest->iid) {
            return [null, false];
        }

        return [$landed, $contribution->hasWorkNewerThanLanding()];
    }

    /**
     * The patch rows for a module: its Needs Review / RTBC issues whose work
     * is not reachable from a merge request, expanded across tracked cores.
     *
     * Classification is Patches\Contribution's, the same one `upkeep patches`
     * uses, so an issue the patch report calls covered is one the dashboard
     * leaves out — the two views cannot disagree about what needs attention.
     * An issue carried entirely by a real branch already has an MR row above
     * and contributes nothing here, and neither does one carrying no patch:
     * that is `upkeep issues`' subject, not the dashboard's.
     *
     * @return list<DashboardRow> ordered by issue nid, then tracked core version
     */
    public function patchRows(Module $module, ModuleSnapshot $snapshot, ?string $versionFilter = null): array
    {
        $cores = self::cores($module, $versionFilter);
        if ($cores === []) {
            return [];
        }

        $contributions = Contribution::pair(
            $module->name,
            $snapshot->patchIssues(),
            [...$snapshot->mergeRequests(), ...$snapshot->mergedMergeRequests()],
            $snapshot->forkNids,
        );
        usort(
            $contributions,
            static fn (Contribution $a, Contribution $b): int => $a->issue->nid <=> $b->issue->nid,
        );

        $rows = [];
        foreach ($contributions as $contribution) {
            if ($contribution->kind()->isCoveredByMergeRequest()) {
                continue;
            }
            // The dashboard is about *contributions*, so a patch row needs a
            // patch. This became a real filter when the snapshot widened to
            // every open status: an issue nobody has contributed to is no
            // longer a rare curiosity but most of the queue (29 of pathauto's
            // 93), and it belongs in `upkeep issues`, where being unclaimed is
            // the point rather than an oddity.
            if ($contribution->issue->patchCount() === 0) {
                continue;
            }
            foreach ($cores as $core) {
                $rows[] = DashboardRow::forPatch(
                    $core,
                    $contribution,
                    $this->cache->latest($module->name, ResultKey::patch($contribution->issue->nid), $core),
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

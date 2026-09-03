<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Cockpit\Module;
use Upkeep\Drupal\IssueReference;
use Upkeep\Drupal\IssueVersion;
use Upkeep\Gate\FastLaneGate;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Project;
use Upkeep\Patches\Contribution;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;

/**
 * The one classification pipeline: (module, project, merge requests, issues) →
 * dashboard rows, one per (issue, branch), classified by the fast-lane gate.
 *
 * It takes merge requests it did not fetch, precisely so both consumers can
 * use it: RowAssembler feeds it live client data, and the dashboard feeds it
 * its cached per-module snapshot. Neither re-derives status — a drift here
 * would misclassify merge eligibility, which is the one thing FastLaneGate
 * says must not happen.
 *
 * **The tracked core versions are no longer a row multiplier.** They are the
 * cores a row's evidence is gathered on, which is what they always meant: a
 * module branch supports several at once, so a row per (subject x core) was
 * describing one piece of work several times. See
 * `docs/dashboard-row-model.md`.
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
     * @param ?string                 $versionFilter gather evidence for this core only, when given
     * @param array<int, ApiFailure>  $ciFailures    detail-fetch failure keyed by MR iid, when the
     *                                               row fell back to listed data
     * @param ?ModuleSnapshot         $snapshot      the module's issues and merged MRs. Without one
     *                                               there is nothing to group merge requests *by*,
     *                                               so every one of them is its own row — which is
     *                                               RowAssembler's case, and why the fast lane still
     *                                               prompts per merge request.
     *
     * @return list<DashboardRow> issue rows by nid then branch, then the
     *                            merge requests belonging to no listed issue,
     *                            by iid
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

        $merged = $snapshot?->mergedMergeRequests() ?? [];
        $contributions = $snapshot === null ? [] : Contribution::pair(
            $module->name,
            $snapshot->patchIssues(),
            [...$mergeRequests, ...$merged],
            $snapshot->forkNids,
        );
        usort(
            $contributions,
            static fn (Contribution $a, Contribution $b): int => $a->issue->nid <=> $b->issue->nid,
        );

        $branches = self::knownBranches($project, [...$mergeRequests, ...$merged]);

        $rows = [];
        $claimed = [];
        foreach ($contributions as $contribution) {
            foreach ($contribution->mergeRequests as $mr) {
                $claimed[$mr->iid] = true;
            }
            foreach ($this->issueRows($module, $project, $contribution, $branches, $cores, $ciFailures) as $row) {
                $rows[] = $row;
            }
        }

        foreach ($mergeRequests as $mergeRequest) {
            if (isset($claimed[$mergeRequest->iid])) {
                continue;
            }
            $rows[] = $this->unlinkedRow($module, $project, $mergeRequest, $cores, $ciFailures);
        }

        return $rows;
    }

    /**
     * One issue's rows: one per branch its open merge requests target, or —
     * when nothing of its is open — one for the patches it carries.
     *
     * The branch multiplier is the only one left, and it is a real
     * distinction: an issue with work on 1.0.x and on 2.0.x is a backport,
     * which is two pieces of work rather than one seen twice.
     *
     * An issue with nothing open and no patch attached yields no row at all.
     * That is `upkeep issues`' subject — being unclaimed is the point there,
     * where here it would be 29 of pathauto's 93 issues saying nothing.
     *
     * @param list<string>           $branches   the project's known branch names
     * @param list<string>           $cores      cores to gather evidence on
     * @param array<int, ApiFailure> $ciFailures
     *
     * @return list<DashboardRow>
     */
    private function issueRows(
        Module $module,
        Project $project,
        Contribution $contribution,
        array $branches,
        array $cores,
        array $ciFailures,
    ): array {
        /** @var array<array-key, list<MergeRequest>> $byBranch */
        $byBranch = [];
        foreach ($contribution->mergeRequests as $mr) {
            $byBranch[$mr->targetBranch][] = $mr;
        }
        ksort($byBranch);

        $openBranches = array_keys(array_filter(
            $byBranch,
            static fn (array $mrs): bool => self::open($mrs) !== [],
        ));

        if ($openBranches === []) {
            $branch = self::dormantBranch($contribution, $project, $branches);

            return $contribution->issue->patchCount() === 0 ? [] : [DashboardRow::forIssue(
                $module->name,
                $branch,
                new Contribution($module->name, $contribution->issue, $byBranch[$branch] ?? []),
                $project,
                null,
                $this->evidence(
                    $module->name,
                    ResultKey::patch($contribution->issue->nid),
                    $cores,
                    $contribution->currentRevision(),
                ),
                null,
            )];
        }

        $rows = [];
        foreach ($openBranches as $key) {
            $branch = (string) $key;
            $onBranch = $byBranch[$key];
            $open = self::open($onBranch);
            // A substantive merge request represents the branch in preference
            // to an empty one: an issue can carry both a real branch and the
            // bot's empty draft, and the real branch is what gets merged.
            $representative = Contribution::substantive($open)[0] ?? $open[0];
            $local = $this->evidence(
                $module->name,
                ResultKey::mergeRequest($representative->iid),
                $cores,
                $representative->headSha,
            );

            $rows[] = DashboardRow::forIssue(
                $module->name,
                $branch,
                new Contribution($module->name, $contribution->issue, $onBranch),
                $project,
                $representative,
                $local,
                $this->gate->classify($representative, $cores, $local),
                $ciFailures[$representative->iid] ?? null,
            );
        }

        return $rows;
    }

    /**
     * A merge request belonging to no listed issue.
     *
     * Not an edge case: 33 of pathauto's 162 merge requests claim no issue at
     * all, and others claim one already marked fixed and so outside the open
     * queue. The nid it does claim is still shown where there is one — an
     * unpaired merge request is not an anonymous one.
     *
     * @param list<string>           $cores
     * @param array<int, ApiFailure> $ciFailures
     */
    private function unlinkedRow(
        Module $module,
        Project $project,
        MergeRequest $mergeRequest,
        array $cores,
        array $ciFailures,
    ): DashboardRow {
        $local = $this->evidence(
            $module->name,
            ResultKey::mergeRequest($mergeRequest->iid),
            $cores,
            $mergeRequest->headSha,
        );

        return DashboardRow::forUnlinkedMergeRequest(
            $module->name,
            $mergeRequest->targetBranch,
            $project,
            $mergeRequest,
            $local,
            $this->gate->classify($mergeRequest, $cores, $local),
            $ciFailures[$mergeRequest->iid] ?? null,
            IssueReference::extract(
                $mergeRequest->title,
                $mergeRequest->sourceBranch,
                $mergeRequest->description,
            ),
        );
    }

    /**
     * What is known locally about one subject, on every core that applies.
     *
     * @param list<string> $cores
     */
    private function evidence(string $module, ResultKey $key, array $cores, ?string $revision): LocalEvidence
    {
        $byCore = [];
        foreach ($cores as $core) {
            $byCore[$core] = $this->cache->latest($module, $key, $core);
        }

        return LocalEvidence::of($byCore, $revision);
    }

    /**
     * The branch a row belongs to when no merge request of the issue's is
     * open: the one that carried the landing, else the one the issue is filed
     * against, else the project's default.
     *
     * Resolved rather than parsed. The version field holds whatever anyone
     * has ever typed into it — `2.0.0`, `8.0.x-dev`, `5.1`, and `x.y.z` 56
     * times in one sample — so IssueVersion proposes candidates and only a
     * name the project actually has is accepted.
     *
     * @param list<string> $branches
     */
    private static function dormantBranch(Contribution $contribution, Project $project, array $branches): string
    {
        $landed = $contribution->landed();
        if ($landed !== null) {
            return $landed->targetBranch;
        }

        return IssueVersion::resolveBranch($contribution->issue->version, $branches)
            ?? ($project->defaultBranch !== '' ? $project->defaultBranch : '-');
    }

    /**
     * Every branch name this module's merge requests are known to target.
     *
     * The branch list without a request for one: an issue's version field is
     * only usable when checked against branches that exist, and the merge
     * requests already name them. Cheaper than asking, and never stale in a
     * way the rest of the snapshot is not.
     *
     * @param list<MergeRequest> $mergeRequests
     * @return list<string>
     */
    private static function knownBranches(Project $project, array $mergeRequests): array
    {
        $branches = [];
        if ($project->defaultBranch !== '') {
            $branches[$project->defaultBranch] = true;
        }
        foreach ($mergeRequests as $mr) {
            $branches[$mr->targetBranch] = true;
        }

        return array_keys($branches);
    }

    /**
     * @param list<MergeRequest> $mergeRequests
     * @return list<MergeRequest>
     */
    private static function open(array $mergeRequests): array
    {
        return array_values(array_filter(
            $mergeRequests,
            static fn (MergeRequest $mr): bool => $mr->state !== 'merged',
        ));
    }

    /**
     * The core versions a run gathers evidence on for this module: all tracked
     * ones, or the single filtered one when it is tracked.
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

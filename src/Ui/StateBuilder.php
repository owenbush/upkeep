<?php

declare(strict_types=1);

namespace Upkeep\Ui;

use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\Module;
use Upkeep\Dashboard\DashboardCache;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Dashboard\ModuleSnapshot;
use Upkeep\Dashboard\ModuleSummary;
use Upkeep\Dashboard\RowFactory;
use Upkeep\Patches\Contribution;
use Upkeep\Patches\ContributionKind;
use Upkeep\Results\ResultsCache;

/**
 * The dashboard, as JSON for the browser.
 *
 * Built from `Dashboard\RowFactory` — the same pipeline the CLI table and the
 * fast-lane gate consume — so the page cannot show a verdict the terminal
 * disagrees with. The UI is a second renderer, never a second source of truth.
 *
 * **Read-only, and cache-only.** Nothing here goes to the network: a request
 * that arrives while drupal.org is slow must not hold an HTTP worker for
 * thirty seconds, and the built-in server has few enough workers that one
 * blocked request is most of them. Refreshing is an *action* — a job that runs
 * `upkeep dashboard --refresh`, whose progress the page can watch like any
 * other.
 */
final readonly class StateBuilder
{
    public function __construct(private Cockpit $cockpit)
    {
    }

    /**
     * @return array<string, mixed>
     */
    public function build(): array
    {
        $dashCache = new DashboardCache($this->cockpit->dashboardCachePath());
        $rowFactory = new RowFactory(new ResultsCache($this->cockpit->resultsPath()));

        $modules = $this->cockpit->loadRegistry()->modules();
        ksort($modules);

        $now = new \DateTimeImmutable();
        $out = [];
        foreach ($modules as $name => $module) {
            $snapshot = $dashCache->load($name);
            if ($snapshot === null) {
                // Never fetched, or the cache was pruned. Said plainly rather
                // than rendered as a module with nothing open, which is a
                // different and much more comfortable claim.
                $out[] = [
                    'module' => $name,
                    'cores' => $module->coreVersions,
                    'cached' => null,
                    'summary' => null,
                    'rows' => [],
                    'issues' => [],
                ];
                continue;
            }

            $rows = array_merge(
                $rowFactory->rows($module, $snapshot->project(), $snapshot->mergeRequests(), snapshot: $snapshot),
                $rowFactory->patchRows($module, $snapshot),
            );

            $out[] = [
                'module' => $name,
                'cores' => $module->coreVersions,
                'cached' => $snapshot->ageLabel($now),
                'summary' => self::summary(ModuleSummary::fromRows($name, $rows)),
                'rows' => array_map(self::row(...), $rows),
                'issues' => self::issues($name, $snapshot),
            ];
        }

        return ['modules' => $out, 'generated_at' => $now->format(\DateTimeInterface::ATOM)];
    }

    /**
     * The module's whole open issue queue, contribution as a *column*.
     *
     * The counterpart to rows(): those answer "what is waiting for me?", these
     * answer "what could I work on?". Both come out of the same snapshot, so
     * the page cannot show an issue the terminal would not.
     *
     * @return list<array<string, mixed>>
     */
    private static function issues(string $module, ModuleSnapshot $snapshot): array
    {
        $contributions = Contribution::pair(
            $module,
            $snapshot->patchIssues(),
            $snapshot->mergeRequests(),
        );

        // Most actionable first, exactly as `upkeep issues` orders it: what
        // awaits a maintainer's verdict, then everything else, newest first.
        usort($contributions, static function (Contribution $a, Contribution $b): int {
            $byAttention = ($b->issue->status->needsMaintainer() ? 1 : 0)
                <=> ($a->issue->status->needsMaintainer() ? 1 : 0);

            return $byAttention !== 0 ? $byAttention : $b->issue->nid <=> $a->issue->nid;
        });

        return array_map(static function (Contribution $contribution): array {
            $issue = $contribution->issue;
            $substantive = $contribution->substantiveMergeRequests();

            return [
                'nid' => $issue->nid,
                'title' => $issue->title,
                'url' => $issue->url,
                'status' => $issue->status->shortLabel(),
                'priority' => $issue->priorityLabel(),
                'awaits_maintainer' => $issue->status->needsMaintainer(),
                'patches' => $issue->patchCount(),
                'mr' => $substantive === [] ? null : $substantive[0]->iid,
                'unclaimed' => $contribution->kind() === ContributionKind::Nothing,
            ];
        }, $contributions);
    }

    /**
     * @return array<string, mixed>
     */
    private static function summary(ModuleSummary $summary): array
    {
        return [
            'merge_requests' => $summary->mergeRequests,
            'ready_auto' => $summary->readyAuto,
            'review' => $summary->review,
            'blocked' => $summary->blocked,
            'patch_issues' => $summary->patchIssues,
            'unchecked' => $summary->unchecked,
            'failed' => $summary->failed,
        ];
    }

    /**
     * One row, flattened to what the page renders and what it needs to start a
     * job about it — never a raw model, so the wire format is a decision
     * rather than an accident of what happened to be public.
     *
     * @return array<string, mixed>
     */
    private static function row(DashboardRow $row): array
    {
        $mr = $row->mergeRequest;
        $contribution = $row->contribution;

        return [
            'kind' => match (true) {
                $row->moduleFailure !== null => 'failure',
                $contribution !== null => 'patch',
                default => 'mr',
            },
            'core' => $row->core,
            'mr' => $mr?->iid,
            'issue' => $contribution?->issue->nid,
            'title' => match (true) {
                $contribution !== null => $contribution->issue->title,
                $mr !== null => $mr->title,
                default => 'merge requests unavailable',
            },
            'url' => $contribution !== null ? $contribution->issue->url : $mr?->webUrl,
            'ci' => $row->ciCell(),
            'local' => $row->localCell(),
            'status' => $row->statusCell(),
            'ready_auto' => $row->isReadyAuto(),
            'patches' => $contribution?->issue->patchCount(),
        ];
    }

    /**
     * The registry's module names, for the actions the page may offer.
     *
     * @return list<string>
     */
    public function moduleNames(): array
    {
        return array_map(
            static fn (Module $m): string => $m->name,
            array_values($this->cockpit->loadRegistry()->modules()),
        );
    }
}

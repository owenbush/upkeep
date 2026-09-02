<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Gate\GateStatus;

/**
 * One module's row on the overview dashboard.
 *
 * Derived from exactly the rows the detailed view would render, never
 * recounted from the underlying data — so a module whose overview says three
 * READY-AUTO shows three READY-AUTO when you drill into it. Two counts of the
 * same thing that can disagree are worse than one count.
 *
 * The overview exists because per-module volume is real: a mature contrib
 * project can carry a hundred open merge requests and forty Needs Review /
 * RTBC issues, and multiplying that by tracked core versions puts one module
 * past two hundred rows. A cockpit-wide detailed table stopped being readable
 * some time before it stopped being printable.
 */
final readonly class ModuleSummary
{
    /**
     * @param list<string> $cores tracked core versions this run covered
     */
    private function __construct(
        public string $module,
        public array $cores,
        public int $mergeRequests,
        public int $readyAuto,
        public int $review,
        public int $blocked,
        public int $patchIssues,
        public int $unchecked,
        public bool $failed,
    ) {
    }

    /**
     * @param list<DashboardRow> $rows every row for one module
     */
    public static function fromRows(string $module, array $rows): self
    {
        $cores = [];
        $mrs = [];
        $patches = [];
        $readyAuto = 0;
        $review = 0;
        $blocked = 0;
        $unchecked = 0;
        $failed = false;

        foreach ($rows as $row) {
            if ($row->moduleFailure !== null) {
                $failed = true;
                continue;
            }
            if ($row->core !== '-') {
                // Keyed *and* valued by the core version: a numeric-looking
                // array key is coerced to int by PHP, and array_keys() would
                // then hand back list<int> where list<string> is declared.
                $cores[$row->core] = $row->core;
            }

            // Counted per distinct subject, not per row: a merge request
            // tracked across two core versions is one merge request with two
            // sets of evidence, and reporting it as two would overstate the
            // queue by however many cores the module tracks.
            if ($row->mergeRequest !== null) {
                $mrs[$row->mergeRequest->iid] = true;
            }
            if ($row->contribution !== null) {
                $patches[$row->contribution->issue->nid] = true;
            }

            // Verdicts and evidence *are* per (subject x core): the same branch
            // can be green on 10 and red on 11, and that is the whole point of
            // tracking both.
            $status = $row->verdict?->status;
            $readyAuto += $status === GateStatus::ReadyAuto ? 1 : 0;
            $review += $status === GateStatus::Review ? 1 : 0;
            $blocked += $status === GateStatus::Blocked ? 1 : 0;

            if (\in_array($row->localCell(), ['–', 'stale'], true)) {
                ++$unchecked;
            }
        }

        return new self(
            $module,
            array_values($cores),
            \count($mrs),
            $readyAuto,
            $review,
            $blocked,
            \count($patches),
            $unchecked,
            $failed,
        );
    }

    /**
     * The overview row's cells: MODULE, CORES, MRS, PATCH ISSUES, READY,
     * CI FAILED, UNCHECKED, CACHED. The cache age is the caller's — it comes
     * from the snapshot, not from the rows.
     *
     * Three deliberate departures from the gate's own vocabulary, so the
     * overview reads the same way as the rows it summarises:
     *
     *   - "PATCH ISSUES", not "PATCHES", because it counts *issues* carrying
     *     patches while a row's own patch count is *files*. One word for two
     *     things is how a summary comes to disagree with its detail.
     *   - "CI FAILED", not "BLOCKED", because that is what the gate's Blocked
     *     verdict is set by and the only thing it is set by.
     *   - No REVIEW column. It counted everything neither ready nor CI-failed,
     *     which on a real module is every row — a number that is always the
     *     total tells a maintainer nothing. What is actionable is UNCHECKED,
     *     which is beside it. The count is still on the object for the browser
     *     UI and anything else that wants it.
     *
     * @return list<string>
     */
    public function toTableCells(string $cacheAge): array
    {
        if ($this->failed) {
            return [$this->module, '–', '–', '–', '–', '–', '–', $cacheAge];
        }

        return [
            $this->module,
            $this->cores === [] ? '–' : implode(',', $this->cores),
            (string) $this->mergeRequests,
            (string) $this->patchIssues,
            self::count($this->readyAuto),
            self::count($this->blocked),
            self::count($this->unchecked),
            $cacheAge,
        ];
    }

    /** Zero reads better as a dash: the eye should catch the non-zero cells. */
    private static function count(int $value): string
    {
        return $value === 0 ? '–' : (string) $value;
    }
}

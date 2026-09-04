<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Gate\GateStatus;
use Upkeep\Gate\GateVerdict;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\PipelineStatus;
use Upkeep\Gitlab\Project;
use Upkeep\Patches\Contribution;
use Upkeep\Workflow\WorkflowException;

/**
 * One assembled dashboard row.
 *
 * **A row is one issue's work on one module branch.** That is the whole
 * change from what came before, and it removes two multipliers that added
 * rows without adding information (see `docs/dashboard-row-model.md`):
 *
 *   - An issue used to appear once per merge request *and* again as a patch
 *     row, which is why `RowFactory` carried a filter whose only job was to
 *     suppress the duplicate it had just created.
 *   - Every row was multiplied by the module's tracked core versions. But a
 *     module *branch* supports several cores at once — pathauto's single
 *     8.x-1.x declares `^10.2 || ^11 || ^12` — so that produced rows
 *     describing the same branch and the same work, differing only in a
 *     column. Core is **evidence**, and lives in `LocalEvidence`.
 *
 * The multiplier that remains is real: an issue with work on two branches is
 * a backport, which is two pieces of work rather than one seen twice.
 *
 * A merge request claiming no issue keeps a row of its own — not an edge
 * case, since 33 of pathauto's 162 merge requests claim none — and a module
 * whose merge requests cannot be listed gets a visible failure row.
 *
 * The dashboard renders rows as table cells; the fast-lane merge command
 * partitions them by verdict and needs the underlying objects (Project for
 * the merge call, MergeRequest for the freshness re-check, LocalEvidence for
 * the context block). Both consume the same cell-formatting helpers so the
 * two commands never describe the same evidence differently.
 */
final readonly class DashboardRow
{
    /**
     * @param list<MergeRequest> $mergeRequests every merge request this row
     *                                          covers — the issue's, narrowed
     *                                          to this branch
     * @param ?MergeRequest      $mergeRequest  the open one the row acts on:
     *                                          what gets checked, gated and
     *                                          merged. Null for a row carrying
     *                                          only patches or only a landing.
     */
    private function __construct(
        public string $module,
        public string $branch,
        public ?int $issueNid,
        public ?Contribution $contribution,
        public array $mergeRequests,
        public ?MergeRequest $mergeRequest,
        public ?Project $project,
        public LocalEvidence $local,
        public ?GateVerdict $verdict,
        public ?ApiFailure $ciFailure,
        public ?ApiFailure $moduleFailure,
        /**
         * A merge request on this row's issue whose work has already landed.
         *
         * The fact a row cannot otherwise carry. Project Update Bot
         * compatibility issues are kept open on purpose so the bot can post
         * again, so an open issue with an open draft on it may nonetheless
         * have had its real work merged — as happens the moment a maintainer
         * promotes a patch, fixes it and merges the result, leaving the
         * bot's draft sitting there looking like the only contribution.
         */
        public ?MergeRequest $landed,
        /** Whether anything on the issue is newer than that landing. */
        public bool $newerWorkSinceLanding,
        public int $patchCount,
        /**
         * Whether the newest patch on the issue postdates the merge request's
         * last update — the old `patch↑` flag, which used to be a
         * cross-reference between two rows and is now a statement about one.
         */
        public bool $patchNewerThanMergeRequest,
    ) {
    }

    /**
     * An issue's work on one branch: its merge requests, its patches, or both.
     *
     * Both used to be separate row kinds, and an issue carrying both produced
     * one of each — the duplication the covered-by-a-merge-request filter
     * existed to hide. Here they are columns of the same row, which is what
     * they always were: an issue with a patch in comment 4 and an MR in
     * comment 9 is one piece of work that arrived twice.
     *
     * @param ?MergeRequest $open  the open merge request to act on, if any
     * @param ?GateVerdict  $verdict null when there is no open merge request,
     *                               which is what makes isReadyAuto() false by
     *                               construction for a patch-only row rather
     *                               than by a check someone has to remember
     */
    public static function forIssue(
        string $module,
        string $branch,
        Contribution $contribution,
        ?Project $project,
        ?MergeRequest $open,
        LocalEvidence $local,
        ?GateVerdict $verdict,
        ?ApiFailure $ciFailure = null,
    ): self {
        $landed = $contribution->landed();

        return new self(
            $module,
            $branch,
            $contribution->issue->nid,
            $contribution,
            $contribution->mergeRequests,
            $open,
            $project,
            $local,
            $verdict,
            $ciFailure,
            null,
            $landed,
            $landed !== null && $contribution->hasWorkNewerThanLanding(),
            $contribution->issue->patchCount(),
            self::patchIsNewer($contribution, $open),
        );
    }

    /**
     * A merge request with no issue behind it on this dashboard.
     *
     * Either it claims none — 20% of pathauto's merge requests do — or it
     * claims one outside the module's open queue, typically an issue already
     * marked fixed. Both keep a row: the merge request is open, and an open
     * merge request is a contribution whatever its issue says.
     *
     * @param ?int $issueNid the nid its metadata claims, when there is one.
     *                       Shown, but not paired: without the issue there is
     *                       no status, no patch count and nothing to group by.
     */
    public static function forUnlinkedMergeRequest(
        string $module,
        string $branch,
        ?Project $project,
        MergeRequest $mergeRequest,
        LocalEvidence $local,
        GateVerdict $verdict,
        ?ApiFailure $ciFailure = null,
        ?int $issueNid = null,
    ): self {
        return new self(
            $module,
            $branch,
            $issueNid,
            null,
            [$mergeRequest],
            $mergeRequest,
            $project,
            $local,
            $verdict,
            $ciFailure,
            null,
            null,
            false,
            0,
            false,
        );
    }

    /** A module whose MRs cannot be listed still gets a visible row. */
    public static function forModuleFailure(string $module, ApiFailure $failure): self
    {
        return new self(
            $module,
            '-',
            null,
            null,
            [],
            null,
            null,
            LocalEvidence::none(),
            null,
            null,
            $failure,
            null,
            false,
            0,
            false,
        );
    }

    public function isReadyAuto(): bool
    {
        return $this->verdict !== null && $this->verdict->status === GateStatus::ReadyAuto;
    }

    /**
     * The merge request this row describes.
     *
     * A real guard, not an \assert(): assertions are compiled out under the
     * production php.ini default, and this invariant is load-bearing — the
     * merge command feeds the result straight into a merge call.
     *
     * @throws WorkflowException when this row has no open merge request
     */
    public function requireMergeRequest(): MergeRequest
    {
        return $this->mergeRequest ?? throw self::notAMergeRequestRow('merge request');
    }

    /**
     * @throws WorkflowException when this row has no open merge request
     */
    public function requireProject(): Project
    {
        return $this->project ?? throw self::notAMergeRequestRow('project');
    }

    /**
     * @throws WorkflowException when this row has no open merge request
     */
    public function requireVerdict(): GateVerdict
    {
        return $this->verdict ?? throw self::notAMergeRequestRow('gate verdict');
    }

    private static function notAMergeRequestRow(string $what): WorkflowException
    {
        return new WorkflowException(sprintf(
            'Dashboard row has no %s: it carries no open merge request.',
            $what,
        ));
    }

    /**
     * The row exactly as the dashboard table renders it:
     * MODULE, ISSUE, VERSION, TITLE, MR, PATCH, CI, LOCAL, STATUS, NEXT.
     *
     * STATUS is a phrase and NEXT is a command, because a row that says only
     * what it *is* leaves a maintainer with a hundred of them and no idea
     * which to touch. The gate's own reason tokens are still available —
     * `describe()` renders them, and the dashboard prints them under `-v`.
     *
     * @param bool $verbose swap the phrase for the gate's reason tokens, and
     *                      the LOCAL cell for every core's own state
     *
     * @return list<string>
     */
    public function toTableCells(bool $verbose = false): array
    {
        $guidance = Guidance::forRow($this);

        if ($this->moduleFailure !== null) {
            $cell = self::failureCell($this->moduleFailure);

            return [
                $this->module,
                '–',
                '–',
                $this->titleCell(),
                '–',
                '–',
                $cell,
                '–',
                $cell,
                $guidance->command,
            ];
        }

        return [
            $this->module,
            $this->issueCell(),
            $this->branch,
            self::truncate($this->titleCell()),
            $this->mergeRequestCell(),
            $this->patchCell(),
            $this->ciCell(),
            $verbose ? $this->local->describe() : $this->local->cell(),
            $verbose ? $this->statusCell() : $guidance->status,
            $guidance->command,
        ];
    }

    /**
     * ISSUE cell: the nid and drupal.org's own status word.
     *
     * The status is the half that was missing. "#3598272 review" and
     * "#3598272 RTBC" call for different things from a maintainer, and the
     * dashboard used to print the same cell for both.
     */
    public function issueCell(): string
    {
        if ($this->contribution !== null) {
            return $this->contribution->issue->nid . ' ' . $this->contribution->issue->status->shortLabel();
        }

        return $this->issueNid === null ? '–' : (string) $this->issueNid;
    }

    /** TITLE cell: the issue's title, or the merge request's when there is no issue. */
    public function titleCell(): string
    {
        if ($this->contribution !== null) {
            return $this->contribution->issue->title;
        }

        if ($this->mergeRequest !== null) {
            return $this->mergeRequest->title;
        }

        return '(merge requests unavailable)';
    }

    /** MR cell: the representative merge request, or the landing that outranks it. */
    public function mergeRequestCell(): string
    {
        return Contribution::renderMergeRequestCell(
            $this->mergeRequests,
            $this->landed,
            $this->newerWorkSinceLanding,
        );
    }

    /**
     * PATCH cell: how many patch files the issue carries, flagged when the
     * newest of them postdates the merge request.
     *
     * `patch↑` used to live in the ISSUE column of a merge-request row and
     * mean "the issue this MR mentions has a newer patch on it" — a
     * cross-reference between two rows that no glossary explained and nobody
     * could read. Here the patch and the merge request are the same row, so
     * the flag is a statement about one thing.
     */
    public function patchCell(): string
    {
        if ($this->patchCount === 0) {
            return '–';
        }

        return $this->patchCount . ($this->patchNewerThanMergeRequest ? ' ↑' : '');
    }

    /** CI cell: pipeline state, or the explicit typed-failure state. */
    public function ciCell(): string
    {
        if ($this->ciFailure !== null) {
            return self::failureCell($this->ciFailure);
        }

        $pipeline = $this->mergeRequest?->headPipeline;
        if ($pipeline === null) {
            return '–';
        }

        return match (true) {
            $pipeline->status->isGreen() => 'pass',
            $pipeline->status === PipelineStatus::Failed => 'fail',
            default => $pipeline->rawStatus !== '' ? $pipeline->rawStatus : $pipeline->status->value,
        };
    }

    /**
     * LOCAL cell: the worst state across every applicable core, naming the
     * core it came from. See LocalEvidence.
     */
    public function localCell(): string
    {
        return $this->local->cell();
    }

    /** STATUS cell: the gate verdict, the contribution kind, or the failure. */
    public function statusCell(): string
    {
        if ($this->moduleFailure !== null) {
            return self::failureCell($this->moduleFailure);
        }

        if ($this->verdict !== null) {
            return $this->verdict->describe();
        }

        return $this->contribution?->dashboardStatus() ?? '–';
    }

    /**
     * Compact, explicit cell state for a typed client failure, e.g. "n/a (403)".
     *
     * The discriminator lives on the failure type itself, so this cannot drift
     * out of step with the taxonomy and needs no unreachable default arm.
     */
    public static function failureCell(ApiFailure $failure): string
    {
        return 'n/a (' . $failure->shortCode() . ')';
    }

    public static function truncate(string $title, int $max = 44): string
    {
        return mb_strlen($title) <= $max ? $title : mb_substr($title, 0, $max - 1) . '…';
    }

    /**
     * Whether the issue's newest patch postdates the merge request's last
     * update — i.e. somebody posted a re-roll the branch does not carry.
     */
    private static function patchIsNewer(Contribution $contribution, ?MergeRequest $open): bool
    {
        $latest = $contribution->issue->latestPatch();
        if ($latest === null || $latest->timestamp <= 0 || $open?->updatedAt === null) {
            return false;
        }

        $updated = strtotime($open->updatedAt);

        return $updated !== false && $latest->timestamp > $updated;
    }
}

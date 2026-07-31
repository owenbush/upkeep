<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Gate\GateStatus;
use Upkeep\Gate\GateVerdict;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\EndpointClosed;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\NotFound;
use Upkeep\Gitlab\Pipeline;
use Upkeep\Gitlab\PipelineStatus;
use Upkeep\Gitlab\Project;
use Upkeep\Gitlab\RateLimited;
use Upkeep\Gitlab\TransportError;
use Upkeep\Results\CachedResult;

/**
 * One assembled dashboard row: either an (MR x core) row carrying the full
 * structured evidence the consumers key on, or a module-level failure row
 * (the module's MRs could not be listed at all).
 *
 * The dashboard renders rows as table cells; the fast-lane merge command
 * partitions them by verdict and needs the underlying objects (Project for
 * the merge call, MergeRequest for the freshness re-check, CachedResult for
 * the context block). Both consume the same cell-formatting helpers so the
 * two commands never describe the same evidence differently.
 */
final readonly class DashboardRow
{
    private function __construct(
        public string $module,
        public string $core,
        public ?Project $project,
        public ?MergeRequest $mergeRequest,
        public ?CachedResult $local,
        public ?GateVerdict $verdict,
        public ?ApiFailure $ciFailure,
        public ?ApiFailure $moduleFailure,
    ) {
    }

    /** @param ?ApiFailure $ciFailure detail-fetch failure; the row fell back to the listed MR data */
    public static function forMergeRequest(
        string $module,
        string $core,
        Project $project,
        MergeRequest $mergeRequest,
        ?CachedResult $local,
        GateVerdict $verdict,
        ?ApiFailure $ciFailure = null,
    ): self {
        return new self($module, $core, $project, $mergeRequest, $local, $verdict, $ciFailure, null);
    }

    /** A module whose MRs cannot be listed still gets a visible row. */
    public static function forModuleFailure(string $module, ApiFailure $failure): self
    {
        return new self($module, '-', null, null, null, null, null, $failure);
    }

    public function isReadyAuto(): bool
    {
        return $this->verdict !== null && $this->verdict->status === GateStatus::ReadyAuto;
    }

    /**
     * The row exactly as the dashboard table renders it:
     * MODULE, MR, CORE, TITLE, CI, LOCAL, STATUS.
     *
     * @return list<string>
     */
    public function toTableCells(): array
    {
        if ($this->moduleFailure !== null) {
            $cell = self::failureCell($this->moduleFailure);

            return [$this->module, '-', '-', '(merge requests unavailable)', $cell, '-', $cell];
        }

        \assert($this->mergeRequest !== null && $this->verdict !== null);

        return [
            $this->module,
            (string) $this->mergeRequest->iid,
            $this->core,
            self::truncate($this->mergeRequest->title),
            $this->ciCell(),
            $this->localCell(),
            $this->verdict->describe(),
        ];
    }

    /** CI cell: pipeline state, or the explicit typed-failure state. */
    public function ciCell(): string
    {
        if ($this->ciFailure !== null) {
            return self::failureCell($this->ciFailure);
        }

        $pipeline = $this->mergeRequest?->headPipeline;
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
     * LOCAL cell: latest cached check result for the (module, MR, core) —
     * "-" when never checked, "stale" when recorded against an older head
     * SHA, otherwise ok / fail (failing check names).
     */
    public function localCell(): string
    {
        if ($this->local === null) {
            return '-';
        }
        $headSha = $this->mergeRequest?->headSha;
        if ($headSha === null || $this->local->sha !== $headSha) {
            return 'stale';
        }
        if ($this->local->result->allPassed()) {
            return 'ok';
        }

        $failed = array_map(
            static fn ($failure): string => $failure->type->value,
            $this->local->result->failures(),
        );

        return 'fail (' . implode(', ', $failed) . ')';
    }

    /** STATUS cell: the gate verdict, or the module-level failure state. */
    public function statusCell(): string
    {
        if ($this->moduleFailure !== null) {
            return self::failureCell($this->moduleFailure);
        }

        \assert($this->verdict !== null);

        return $this->verdict->describe();
    }

    /** Compact, explicit cell state for a typed client failure, e.g. "n/a (403)". */
    public static function failureCell(ApiFailure $failure): string
    {
        return 'n/a (' . match (true) {
            $failure instanceof EndpointClosed => (string) $failure->status,
            $failure instanceof NotFound => '404',
            $failure instanceof RateLimited => 'rate-limited',
            $failure instanceof TransportError => $failure->status === null ? 'transport' : (string) $failure->status,
            default => 'error',
        } . ')';
    }

    public static function truncate(string $title, int $max = 44): string
    {
        return mb_strlen($title) <= $max ? $title : mb_substr($title, 0, $max - 1) . '…';
    }
}

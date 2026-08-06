<?php

declare(strict_types=1);

namespace Upkeep\Patches;

/**
 * How the work on an issue has been delivered: as patch files, as a merge
 * request that carries changes, as both, or as neither.
 *
 * The distinction `patches` exists to draw is not "MR or no MR" but "is the
 * work reachable from the MR-centric dashboard?" — so an MR that carries no
 * changes counts as no MR at all, and an issue holding both a patch and a
 * real MR is worth showing rather than hiding: the patch may be newer than
 * the branch, or may be what the branch should have been built from.
 */
enum ContributionKind
{
    /** Patch files, no merge request referencing the issue. */
    case PatchOnly;

    /** Patch files, and a merge request that carries changes. */
    case PatchAndMergeRequest;

    /** Patch files, and merge request(s) — all of them empty. */
    case PatchWithEmptyMergeRequest;

    /** No patch files; a merge request carries the work. */
    case MergeRequestOnly;

    /** Neither patch files nor a merge request that carries anything. */
    case Nothing;

    /**
     * Whether this kind is reachable from the MR-centric dashboard, and so has
     * no business on a patch report.
     */
    public function isCoveredByMergeRequest(): bool
    {
        return $this === self::MergeRequestOnly;
    }

    /** Whether any merge request carries the work for this issue. */
    public function hasSubstantiveMergeRequest(): bool
    {
        return $this === self::PatchAndMergeRequest || $this === self::MergeRequestOnly;
    }

    /** Segment label for the summary line; null for kinds never summarised. */
    public function summaryLabel(): ?string
    {
        return match ($this) {
            self::PatchOnly => 'patch-only',
            self::PatchAndMergeRequest => 'patch + MR',
            self::PatchWithEmptyMergeRequest => 'patch, empty MR',
            self::Nothing => 'nothing attached',
            self::MergeRequestOnly => null,
        };
    }
}

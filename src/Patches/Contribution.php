<?php

declare(strict_types=1);

namespace Upkeep\Patches;

use Upkeep\Drupal\Issue;
use Upkeep\Gitlab\MergeRequest;

/**
 * One drupal.org issue together with every merge request that claims
 * authorship of it — the unit `upkeep patches` classifies and renders.
 *
 * Both halves are kept because an issue can hold both kinds of contribution
 * at once, which is normal in the Drupal community: a patch posted in comment
 * 4, an MR opened in comment 9, and a re-roll posted in comment 14 that never
 * made it onto the branch.
 */
final readonly class Contribution
{
    /**
     * @param list<MergeRequest> $mergeRequests every MR whose metadata asserts
     *                                          authorship of this issue
     *                                          (IssueReference::extractOwning)
     */
    public function __construct(
        public string $module,
        public Issue $issue,
        public array $mergeRequests = [],
    ) {
    }

    /**
     * The merge requests that carry changes.
     *
     * An MR whose emptiness is *unknown* counts as substantive. Unknown is the
     * reading a merge-request list payload gives (it omits diff_refs) and the
     * reading a snapshot cached by an older upkeep gives, and in both cases
     * the safe direction is the one that preserves the pre-existing behaviour
     * — treat the MR as real work — rather than one that invents empty MRs
     * out of missing data.
     *
     * @return list<MergeRequest>
     */
    public function substantiveMergeRequests(): array
    {
        return array_values(array_filter(
            $this->mergeRequests,
            static fn (MergeRequest $mr): bool => $mr->carriesChanges() !== false,
        ));
    }

    public function kind(): ContributionKind
    {
        $hasPatches = $this->issue->patchCount() > 0;
        $substantive = $this->substantiveMergeRequests() !== [];

        if (!$hasPatches) {
            return $substantive ? ContributionKind::MergeRequestOnly : ContributionKind::Nothing;
        }
        if ($substantive) {
            return ContributionKind::PatchAndMergeRequest;
        }

        return $this->mergeRequests === []
            ? ContributionKind::PatchOnly
            : ContributionKind::PatchWithEmptyMergeRequest;
    }

    /**
     * The MR column: the representative merge request, flagged when it carries
     * nothing, with a count of any others. A substantive MR represents the
     * issue in preference to an empty one — an issue can carry both a real
     * branch and a bot's empty draft, and the real branch is the answer to
     * "is this already in git?".
     */
    public function mergeRequestCell(): string
    {
        if ($this->mergeRequests === []) {
            return '–';
        }

        $substantive = $this->substantiveMergeRequests();
        $representative = $substantive[0] ?? $this->mergeRequests[0];

        $cell = '!' . $representative->iid;
        if ($representative->carriesChanges() === false) {
            $cell .= ' empty';
        }

        $others = \count($this->mergeRequests) - 1;

        return $others > 0 ? $cell . ' +' . $others : $cell;
    }
}

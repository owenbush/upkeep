<?php

declare(strict_types=1);

namespace Upkeep\Patches;

use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueReference;
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
     * Pair each issue with the merge requests that claim authorship of it.
     *
     * Shared by every consumer that has to decide what a contribution *is* —
     * the patch report and the dashboard — so the two can never disagree about
     * whether an issue is covered. Merge requests claiming an issue outside
     * $issues are dropped: they cannot change any row, and dropping them is
     * what bounds the emptiness probing the caller may do.
     *
     * @param list<Issue>        $issues
     * @param list<MergeRequest> $mergeRequests
     * @return list<self>
     */
    public static function pair(string $module, array $issues, array $mergeRequests): array
    {
        $wanted = [];
        foreach ($issues as $issue) {
            $wanted[$issue->nid] = true;
        }

        $byNid = [];
        foreach ($mergeRequests as $mr) {
            $nid = IssueReference::extractOwning($mr->title, $mr->sourceBranch, $mr->description);
            if ($nid === null || !isset($wanted[$nid])) {
                continue;
            }
            $byNid[$nid][] = $mr;
        }

        $contributions = [];
        foreach ($issues as $issue) {
            $contributions[] = new self($module, $issue, $byNid[$issue->nid] ?? []);
        }

        return $contributions;
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
     * The revision a cached check result must name to be current for this
     * issue: the newest patch on it. Null when the issue carries no patch at
     * all, in which case there is nothing a result could be about.
     */
    public function currentRevision(): ?string
    {
        $latest = $this->issue->latestPatch();

        return $latest === null ? null : PatchRevision::of($latest->url);
    }

    /**
     * The dashboard's STATUS cell for a patch row: what arrived, and how much
     * of it. Deliberately not a gate verdict — nothing here is mergeable, and
     * a cell that looked like one would invite the wrong action.
     */
    public function dashboardStatus(): string
    {
        $count = $this->issue->patchCount();
        $files = $count === 1 ? '1 patch' : $count . ' patches';

        return match ($this->kind()) {
            ContributionKind::PatchOnly => 'PATCH ' . $files,
            ContributionKind::PatchAndMergeRequest => 'PATCH ' . $files . ', ' . $this->mergeRequestCell(),
            ContributionKind::PatchWithEmptyMergeRequest => 'PATCH ' . $files . ', ' . $this->mergeRequestCell(),
            ContributionKind::Nothing => 'PATCH nothing attached',
            ContributionKind::MergeRequestOnly => 'PATCH covered by ' . $this->mergeRequestCell(),
        };
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

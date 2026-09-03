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
     * @param array<int, int>    $forkNids source-project id => issue nid, from
     *                                     GitlabClient::issueForkNids(). The
     *                                     authoritative pairing where it has
     *                                     an answer: drupal.org made that fork
     *                                     *for* that issue, which is a fact
     *                                     about how the repository exists
     *                                     rather than a string in a title.
     * @return list<self>
     */
    public static function pair(string $module, array $issues, array $mergeRequests, array $forkNids = []): array
    {
        $wanted = [];
        foreach ($issues as $issue) {
            $wanted[$issue->nid] = true;
        }

        $byNid = [];
        foreach ($mergeRequests as $mr) {
            // The fork wins. It is the only thing that pairs a Project Update
            // Bot MR at all: those are titled "Automated Project Update Bot
            // fixes" on a branch called project-update-bot-only, and say only
            // "Relates to #NNN" — which extractOwning() rejects by design.
            $nid = ($mr->sourceProjectId !== null ? ($forkNids[$mr->sourceProjectId] ?? null) : null)
                ?? IssueReference::extractOwning($mr->title, $mr->sourceBranch, $mr->description);

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
     * The merge request whose work has landed, if one has.
     *
     * The question this whole surface exists to answer for a class of issue
     * that cannot answer it itself: Project Update Bot compatibility issues
     * are kept open on purpose, so the bot can post again as core moves, and
     * an open one may already have had its work merged months ago. Two such
     * issues look identical until you ask whether anything was merged.
     */
    public function landed(): ?MergeRequest
    {
        return MergeRequest::latestMerged($this->mergeRequests);
    }

    /**
     * Whether anything on the issue is newer than the merge.
     *
     * The discriminator, and the reason this never says "resolved". A bot that
     * posts again after its earlier work merged has raised new work; an issue
     * where nothing has happened since is one whose open status is only the
     * convention. Both are true statements about evidence, and which of them
     * warrants closing the issue stays the maintainer's call.
     */
    public function hasWorkNewerThanLanding(): bool
    {
        $landed = $this->landed();
        if ($landed?->mergedAt === null) {
            return false;
        }

        $mergedAt = strtotime($landed->mergedAt);
        if ($mergedAt === false) {
            return false;
        }

        foreach ($this->issue->files as $file) {
            if ($file->timestamp > $mergedAt) {
                return true;
            }
        }

        foreach ($this->mergeRequests as $mr) {
            if ($mr->state === 'merged' || $mr->updatedAt === null) {
                continue;
            }
            $updated = strtotime($mr->updatedAt);
            if ($updated !== false && $updated > $mergedAt) {
                return true;
            }
        }

        return false;
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
        return self::substantive($this->mergeRequests);
    }

    /**
     * @param list<MergeRequest> $mergeRequests
     * @return list<MergeRequest>
     */
    public static function substantive(array $mergeRequests): array
    {
        return array_values(array_filter(
            $mergeRequests,
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
        return self::renderMergeRequestCell(
            $this->mergeRequests,
            $this->landed(),
            $this->hasWorkNewerThanLanding(),
        );
    }

    /**
     * The MR column, from merge requests alone.
     *
     * Static because the dashboard row renders the same cell and only
     * sometimes holds an Issue: a merge request whose issue is closed, or
     * outside the snapshot's queue, still has an MR column and no
     * contribution to ask. One implementation, so the two views cannot
     * describe the same merge requests differently.
     *
     * @param list<MergeRequest> $mergeRequests every MR the row covers
     * @param ?MergeRequest      $landed        the one whose work is already in
     */
    public static function renderMergeRequestCell(
        array $mergeRequests,
        ?MergeRequest $landed,
        bool $newerWorkSinceLanding,
    ): string {
        if ($mergeRequests === []) {
            return '–';
        }

        // A landing outranks everything else the cell could say. An open issue
        // whose work is already merged is the one case a maintainer cannot
        // read off the issue at all, and it is the commonest shape of a
        // Project Update Bot compatibility issue, which convention keeps open
        // so the bot can post again.
        if ($landed !== null) {
            return sprintf(
                '!%d merged %s%s',
                $landed->iid,
                substr((string) $landed->mergedAt, 0, 10),
                $newerWorkSinceLanding ? ', newer work since' : '',
            );
        }

        $substantive = self::substantive($mergeRequests);
        $representative = $substantive[0] ?? $mergeRequests[0];

        $cell = '!' . $representative->iid;
        if ($representative->carriesChanges() === false) {
            $cell .= ' empty';
        }

        $others = \count($mergeRequests) - 1;

        return $others > 0 ? $cell . ' +' . $others : $cell;
    }
}

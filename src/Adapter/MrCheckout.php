<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Upkeep\Gitlab\MergeRequest;

/**
 * Pure logic behind EngineAdapterInterface::applyMr(): how a merge request
 * maps onto git refs in the module working copy, and the native-base rule.
 *
 * **The ref is the merge, not the head, and that is the whole point.** GitLab
 * publishes two refs per merge request: `/head` is the contributor's branch,
 * `/merge` is that branch merged into the *current* tip of the target. CI
 * analyses `/merge`. upkeep fetched `/head`, so the two were reading different
 * trees on any merge request whose branch had fallen behind — which is nearly
 * all of them. Measured on pathauto: of 25 open merge requests, **23 have a
 * merge tree that differs from their head tree**, and branches run 7 to 41
 * commits behind the target.
 *
 * An MR branch is not stale in the way a fetch fixes. It is one commit of work
 * on top of the target *as it was months ago*, and the tree CI runs exists on
 * neither side until GitLab computes it. Fetching the branch harder never
 * produces it. This is the same failure as applying a patch to a stale base —
 * a clean merge whose result nobody has ever compiled — arriving by the other
 * door.
 *
 * `/merge` is absent when GitLab cannot compute it, which means the merge
 * request conflicts with its target. That is worth saying rather than quietly
 * substituting `/head`, so the fallback is loud.
 *
 * Backport testing (running an MR against a base other than the branch it
 * targets) is out of scope for upkeep, so applyMr refuses target/base
 * mismatches up front.
 */
final readonly class MrCheckout
{
    /** GitLab's ref for the branch merged into the current target tip. */
    public static function mergeRef(int $iid): string
    {
        return sprintf('refs/merge-requests/%d/merge', $iid);
    }

    /** GitLab's ref for the contributor's branch as it stands. */
    public static function headRef(int $iid): string
    {
        return sprintf('refs/merge-requests/%d/head', $iid);
    }

    /**
     * The refspec to fetch, for whichever ref is being used.
     *
     * Force-updating (+) so re-applying an MR that moved since the last fetch
     * updates mr-<iid> instead of failing non-fast-forward. The merge ref
     * moves for a second reason the head ref does not: GitLab recomputes it
     * whenever the *target* gains a commit, so a re-check after an unchanged
     * merge request can still be a different tree — which is exactly the
     * thing worth re-checking.
     */
    public static function fetchRefspec(string $ref, int $iid): string
    {
        return sprintf('+%s:%s', $ref, self::branchName($iid));
    }

    /**
     * Which ref to check out, given what the remote actually advertises.
     *
     * Returns null when the merge request has neither, which is not a state
     * to guess about: the iid is wrong, or the MR was removed.
     *
     * @param list<string> $advertised ref names from `git ls-remote`
     */
    public static function preferredRef(array $advertised, int $iid): ?string
    {
        foreach ([self::mergeRef($iid), self::headRef($iid)] as $ref) {
            if (\in_array($ref, $advertised, true)) {
                return $ref;
            }
        }

        return null;
    }

    /**
     * What to say when only the head ref exists.
     *
     * GitLab computes no merge ref for a merge request that conflicts with its
     * target, so this is a diagnosis and not a detail: the contribution does
     * not currently apply, CI has nothing to run either, and whatever is
     * checked locally is the branch alone.
     */
    public static function noMergeRefWarning(int $iid): string
    {
        return sprintf(
            'MR !%d has no merge ref: GitLab could not merge it into its target, which normally means a conflict. '
            . 'Checking the branch on its own instead — this is not what CI runs, and the result says nothing '
            . 'about how the work behaves once merged.',
            $iid,
        );
    }

    /**
     * @throws AdapterException when the merge request advertises no ref at all
     */
    public static function noRefsAtAll(int $iid): AdapterException
    {
        return new AdapterException(sprintf(
            'MR !%d publishes no refs on origin — neither %s nor %s. Check the merge request number.',
            $iid,
            self::mergeRef($iid),
            self::headRef($iid),
        ));
    }

    /**
     * The local branch an applied MR is checked out on.
     */
    public static function branchName(int $iid): string
    {
        return ManagedBranch::forMergeRequest($iid);
    }

    /**
     * The working copy's base branch: the branch recorded by a previous apply
     * when present (the working copy then sits on a managed branch), otherwise
     * the currently checked-out branch — which must not itself be a managed
     * branch (mr-*, patch-*) or a detached HEAD, because then the base is
     * unknowable.
     *
     * Shared by both apply paths: a patch applied on top of a merge request's
     * branch, or the reverse, would be testing two contributions at once while
     * reporting on one.
     *
     * @param string|null $currentBranch null when HEAD is detached
     * @param string|null $recordedBase  the upkeep.base-branch git config value, when set
     *
     * @throws AdapterException when no base branch can be determined
     */
    public static function resolveBaseBranch(?string $currentBranch, ?string $recordedBase): string
    {
        if ($recordedBase !== null && $recordedBase !== '') {
            return $recordedBase;
        }

        if ($currentBranch === null || $currentBranch === '') {
            throw new AdapterException(
                'Cannot determine the module working copy\'s base branch: HEAD is detached '
                . 'and no base branch is recorded.',
            );
        }

        if (ManagedBranch::isManaged($currentBranch)) {
            throw new AdapterException(sprintf(
                'Cannot determine the module working copy\'s base branch: it sits on the upkeep-managed branch "%s" '
                . 'and no base branch is recorded.',
                $currentBranch,
            ));
        }

        // A work branch is a base nobody meant. Cutting mr-<iid> or
        // patch-<nid> from it would test the contribution *plus* whatever the
        // maintainer has written and not pushed, and report the result as a
        // verdict on the contribution alone. Refused rather than guessed,
        // because the wrong answer here is a green check on code that was
        // never actually tested.
        if (IssueBranch::isWorkBranch($currentBranch)) {
            throw new AdapterException(sprintf(
                'The module working copy is on your own work branch "%s". Checking a contribution from here would '
                . 'test it on top of that work. Switch to the target branch first (git -C <module> checkout <base>), '
                . 'then re-run.',
                $currentBranch,
            ));
        }

        return $currentBranch;
    }

    /**
     * The native-base rule: an MR may only be applied against the branch it
     * targets. Applying it to any other base would be backport testing,
     * which upkeep deliberately does not do.
     *
     * @throws AdapterException when the MR targets a different branch than the working copy's base
     */
    public static function assertNativeBase(MergeRequest $mergeRequest, string $baseBranch): void
    {
        if ($mergeRequest->targetBranch === $baseBranch) {
            return;
        }

        throw new AdapterException(sprintf(
            'MR !%d targets branch "%s", but the environment\'s module working copy is based on branch "%s". '
            . 'Backport testing is out of scope: apply the MR in an environment whose base is its target branch.',
            $mergeRequest->iid,
            $mergeRequest->targetBranch,
            $baseBranch,
        ));
    }
}

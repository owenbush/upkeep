<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Upkeep\Gitlab\MergeRequest;

/**
 * Pure logic behind EngineAdapterInterface::applyMr(): how a merge request
 * maps onto git refs in the module working copy, and the native-base rule.
 *
 * Drupalcode GitLab exposes every MR's head as refs/merge-requests/<iid>/head;
 * the adapter fetches that into a local mr-<iid> branch. Backport testing
 * (running an MR against a base other than the branch it targets) is out of
 * scope for upkeep, so applyMr refuses target/base mismatches up front.
 */
final readonly class MrCheckout
{
    /**
     * The refspec to fetch: force-updating (+) so re-applying an MR that
     * gained commits since the last fetch updates mr-<iid> instead of
     * failing non-fast-forward.
     */
    public static function fetchRefspec(int $iid): string
    {
        return sprintf('+refs/merge-requests/%d/head:%s', $iid, self::branchName($iid));
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

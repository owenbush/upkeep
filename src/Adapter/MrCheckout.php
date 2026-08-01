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
    private const BRANCH_PREFIX = 'mr-';

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
        return self::BRANCH_PREFIX . $iid;
    }

    /**
     * The working copy's base branch: the branch recorded by a previous
     * applyMr when present (the working copy then sits on an mr-* branch),
     * otherwise the currently checked-out branch — which must not itself be
     * an mr-* branch or a detached HEAD, because then the base is unknowable.
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
            throw new AdapterException('Cannot determine the module working copy\'s base branch: HEAD is detached and no base branch is recorded.');
        }

        if (preg_match('/^' . self::BRANCH_PREFIX . '\d+$/', $currentBranch) === 1) {
            throw new AdapterException(sprintf(
                'Cannot determine the module working copy\'s base branch: it sits on MR branch "%s" and no base branch is recorded.',
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

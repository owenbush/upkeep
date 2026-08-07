<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The branch names upkeep creates in a module working copy, and the test for
 * whether a branch is one of them.
 *
 * Two operations put the working copy on a branch of their own — applying a
 * merge request (`mr-<iid>`) and applying a patch file (`patch-<nid>`) — and
 * both must be recognised by the base-branch resolution, because a managed
 * branch is never a valid *base*. Resolving a base of `patch-3597808` would
 * silently test the next contribution on top of the previous one; naming both
 * prefixes in one place is what stops the second operation from having to
 * remember the first one's convention.
 */
final class ManagedBranch
{
    private const MR_PREFIX = 'mr-';

    private const PATCH_PREFIX = 'patch-';

    /** The local branch an applied merge request is checked out on. */
    public static function forMergeRequest(int $iid): string
    {
        return self::MR_PREFIX . $iid;
    }

    /**
     * The local branch an applied patch is checked out on. Keyed by issue,
     * not by file: re-applying a different patch from the same issue replaces
     * the branch rather than accumulating one branch per re-roll.
     */
    public static function forPatch(int $nid): string
    {
        return self::PATCH_PREFIX . $nid;
    }

    /** Whether a branch name is one upkeep created, and so never a base. */
    public static function isManaged(string $branch): bool
    {
        return preg_match('/^(' . self::MR_PREFIX . '|' . self::PATCH_PREFIX . ')\d+$/', $branch) === 1;
    }
}

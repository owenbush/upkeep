<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Pure logic behind EngineAdapterInterface::applyPatch(): how a downloaded
 * patch file maps onto git operations in the module working copy.
 *
 * A patch has no ref to fetch, so unlike a merge request it cannot simply be
 * checked out. It is applied onto a fresh branch off the base and committed,
 * for two reasons that both matter to what the checks then report:
 *
 *   - The checks must run against a *clean* tree. Left uncommitted, the patch
 *     would show up as local modification to every subsequent inspection, and
 *     applyMr's dirty-working-copy guard would refuse to run afterwards.
 *   - The branch is reset from the base on every apply, so re-running with a
 *     newer re-roll tests that re-roll alone rather than the sum of every
 *     patch ever applied to the issue.
 *
 * Patch files on drupal.org are `git diff` output taken at the repository
 * root, so they apply at -p1. A patch that will not apply is a result, not a
 * crash: the caller is told which patch failed against which base, because
 * "this needs a re-roll" is exactly the review outcome worth reporting.
 */
final readonly class PatchCheckout
{
    /**
     * Arguments for the apply attempt. `--index` stages what it applies, so
     * the commit that follows needs no separate `git add`, and a partial
     * application cannot leave staged and unstaged halves disagreeing.
     *
     * @return list<string>
     */
    public static function applyArgs(string $localPath): array
    {
        return ['apply', '--index', '-p1', $localPath];
    }

    /**
     * A three-way apply, retried when the straight one fails. Git can often
     * place a hunk that context-matching alone rejects, provided the blobs the
     * patch was generated against are in the repository — which for a
     * drupal.org patch cut from the same project they generally are.
     *
     * @return list<string>
     */
    public static function threeWayApplyArgs(string $localPath): array
    {
        return ['apply', '--index', '-p1', '--3way', $localPath];
    }

    /**
     * The commit message recording what was applied. It names the file rather
     * than just the issue, because an issue routinely carries several
     * re-rolls and the working copy should say which one it holds.
     */
    public static function commitMessage(PatchApplication $patch): string
    {
        return sprintf('Apply %s (issue #%d) [upkeep]', $patch->name, $patch->issueNid);
    }

    /**
     * The failure a patch that will not apply produces.
     *
     * Phrased as a review finding rather than a tool error: a patch that no
     * longer applies to its target branch has told the maintainer something
     * true and useful about the contribution.
     */
    public static function unappliableException(
        PatchApplication $patch,
        string $baseBranch,
        string $detail,
    ): AdapterException {
        return new AdapterException(sprintf(
            "Patch \"%s\" (issue #%d) does not apply to \"%s\" — it needs a re-roll.\n%s",
            $patch->name,
            $patch->issueNid,
            $baseBranch,
            trim($detail) === '' ? '(git reported no detail)' : trim($detail),
        ));
    }
}

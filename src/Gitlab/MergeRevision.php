<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * Which revision a merge request's local check evidence is *about*.
 *
 * One definition, because two would be a silent bug. The dashboard decides
 * whether a cached result is still current, and `check` decides what to file
 * it under; if those ever disagreed, evidence would read as fresh forever or
 * as stale forever, and both failures look like the tool working.
 *
 * It is the **merge** ref's SHA, not the head's. upkeep checks the branch
 * merged into the current tip of its target, because that is what CI analyses
 * — and that tree changes when *either* side moves. A result keyed on the head
 * SHA would still read as current after the target gained a commit, which is
 * the same "evidence about a tree nobody checked" that keying on a SHA was
 * introduced to prevent.
 *
 * The head SHA is the fallback for the one case that has no merge ref: a merge
 * request GitLab cannot merge into its target, normally a conflict. There the
 * adapter checks the branch alone and says so, so the revision follows it —
 * both halves fall back together or neither does.
 */
final readonly class MergeRevision
{
    /**
     * Null when neither is known, which means nothing can be cached: an entry
     * keyed on a guess is permanently-fresh evidence, and the fast-lane gate
     * reads these.
     */
    public static function of(?string $mergeRefSha, ?string $headSha): ?string
    {
        foreach ([$mergeRefSha, $headSha] as $candidate) {
            if ($candidate !== null && $candidate !== '') {
                return $candidate;
            }
        }

        return null;
    }
}

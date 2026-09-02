<?php

declare(strict_types=1);

namespace Upkeep\Drupal;

/**
 * Which git branch a drupal.org issue's "Version" field refers to.
 *
 * An issue is filed against a version, and any patch on it is generated
 * against that branch. upkeep applied patches onto whatever the working copy
 * happened to sit on — the clone's default branch — so a patch for a 2.0.0
 * issue was applied to 1.0.x and reported as needing a re-roll. The patch was
 * fine; the base was wrong.
 *
 * **This never decides anything on its own.** It proposes candidates, most
 * specific first, and the caller intersects them with the branches the project
 * actually has. That inversion is deliberate: the version field holds whatever
 * anyone has ever typed into it, and a sample of real issues turns up `2.0.0`,
 * `8.0.x-dev`, `4.6.x-dev`, `5.1`, `6.14` and — 56 times in one sample — the
 * literal string `x.y.z`. No parser is going to be right about all of that.
 * Proposing and checking is right about all of it, because a candidate that
 * names no real branch simply matches nothing and the caller falls back.
 */
final readonly class IssueVersion
{
    /**
     * Branch names this version might mean, most specific first and without
     * duplicates.
     *
     * @return list<string> empty when the version says nothing usable
     */
    public static function branchCandidates(?string $version): array
    {
        $raw = trim($version ?? '');
        if ($raw === '') {
            return [];
        }

        // "2.0.x-dev" and "2.0.x" mean the same branch; the suffix is
        // drupal.org's, not git's.
        $base = preg_replace('/-dev$/', '', $raw) ?? $raw;

        $candidates = [$base];

        // Legacy contrib: "8.x-1.4" and "8.x-1.x" are both the 8.x-1.x branch.
        if (preg_match('/^(\d+\.x)-(\d+)\./', $base, $m) === 1) {
            $candidates[] = $m[1] . '-' . $m[2] . '.x';
        }

        // Semver-ish: 2.0.0 lives on 2.0.x, and some projects branch at 2.x.
        if (preg_match('/^(\d+)\.(\d+)(?:\.\d+)?/', $base, $m) === 1) {
            $candidates[] = $m[1] . '.' . $m[2] . '.x';
            $candidates[] = $m[1] . '.x';
        }

        return array_values(array_unique(array_filter(
            $candidates,
            static fn (string $candidate): bool => $candidate !== '',
        )));
    }

    /**
     * The first candidate that is a real branch on the project, or null.
     *
     * Null is "cannot tell", and callers must fall back to what they would
     * have done anyway rather than refuse. An issue whose version is `x.y.z`
     * is not an error, it is an issue nobody set the field on.
     *
     * @param list<string> $existingBranches the project's actual branch names
     */
    public static function resolveBranch(?string $version, array $existingBranches): ?string
    {
        foreach (self::branchCandidates($version) as $candidate) {
            if (\in_array($candidate, $existingBranches, true)) {
                return $candidate;
            }
        }

        return null;
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Drupal;

/**
 * Extracts a drupal.org issue node ID from merge request metadata.
 *
 * Three sources, checked in order of reliability:
 *   1. MR title: "Issue #3467675: Make URL field required"
 *   2. MR description: drupal.org URLs or "Issue #NNN" references
 *      (auto-populated when creating MRs from issue forks)
 *   3. Branch name: "3467675-fix-whatever" (issue number prefix)
 */
final class IssueReference
{
    public static function extract(string $title, string $sourceBranch, ?string $description = null): ?int
    {
        if (preg_match('/Issue\s*#(\d+)/i', $title, $m) === 1) {
            return (int) $m[1];
        }

        if ($description !== null && $description !== '') {
            $nid = self::fromText($description);
            if ($nid !== null) {
                return $nid;
            }
        }

        if (preg_match('/^(\d{4,})-/', $sourceBranch, $m) === 1) {
            return (int) $m[1];
        }

        return null;
    }

    public static function issueUrl(int $nid): string
    {
        return sprintf('https://www.drupal.org/node/%d', $nid);
    }

    private static function fromText(string $text): ?int
    {
        if (preg_match('/Issue\s*#(\d+)/i', $text, $m) === 1) {
            return (int) $m[1];
        }

        if (preg_match('/Relates to\s*#(\d+)/i', $text, $m) === 1) {
            return (int) $m[1];
        }

        if (preg_match('#drupal\.org/project/[^/]+/issues/(\d+)#', $text, $m) === 1) {
            return (int) $m[1];
        }

        if (preg_match('#drupal\.org/node/(\d+)#', $text, $m) === 1) {
            return (int) $m[1];
        }

        return null;
    }
}

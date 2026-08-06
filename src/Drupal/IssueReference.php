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
 *
 * Two strengths, because the answer is used for two different jobs:
 *
 *   - extract() — "which issue is this MR about?" Generous: a mere mention
 *     ("Relates to #NNN") counts, because labelling a dashboard row with a
 *     probably-right issue beats labelling it with nothing.
 *   - extractOwning() — "does this MR carry the work for that issue?" Strict:
 *     a mention does not count. The Project Update Bot opens an MR whose
 *     description reads "Relates to #NNN. This merge request was automatically
 *     created by the Project Update Bot", on a large fraction of contrib
 *     projects. Under the generous rule that MR *claims* the issue, which is
 *     fine for a label and wrong as grounds for suppressing the issue's
 *     patches from `upkeep patches`.
 */
final class IssueReference
{
    public static function extract(string $title, string $sourceBranch, ?string $description = null): ?int
    {
        return self::resolve($title, $sourceBranch, $description, false);
    }

    /**
     * As extract(), but only from references that assert authorship of the
     * issue's fix — the "Issue #NNN" title convention, an issue-fork branch
     * name, or a drupal.org issue URL in the auto-populated description.
     * A loose "Relates to #NNN" mention yields null.
     */
    public static function extractOwning(string $title, string $sourceBranch, ?string $description = null): ?int
    {
        return self::resolve($title, $sourceBranch, $description, true);
    }

    public static function issueUrl(int $nid): string
    {
        return sprintf('https://www.drupal.org/node/%d', $nid);
    }

    private static function resolve(
        string $title,
        string $sourceBranch,
        ?string $description,
        bool $owningOnly,
    ): ?int {
        if (preg_match('/Issue\s*#(\d+)/i', $title, $m) === 1) {
            return (int) $m[1];
        }

        if ($description !== null && $description !== '') {
            $nid = self::fromText($description, $owningOnly);
            if ($nid !== null) {
                return $nid;
            }
        }

        if (preg_match('/^(\d{4,})-/', $sourceBranch, $m) === 1) {
            return (int) $m[1];
        }

        return null;
    }

    private static function fromText(string $text, bool $owningOnly): ?int
    {
        if (preg_match('/Issue\s*#(\d+)/i', $text, $m) === 1) {
            return (int) $m[1];
        }

        if (!$owningOnly && preg_match('/Relates to\s*#(\d+)/i', $text, $m) === 1) {
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

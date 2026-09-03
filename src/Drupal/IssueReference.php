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

    /**
     * The issue an issue fork was made for, from its project path.
     *
     * `issue/pathauto-3616056` means drupal.org created that repository *for*
     * issue 3616056 — a stronger claim than anything in an MR's own metadata,
     * because it is a fact about how the fork came to exist rather than a
     * string somebody typed.
     *
     * It is the only thing that pairs a Project Update Bot merge request to
     * its issue. Those are titled "Automated Project Update Bot fixes", their
     * branch is `project-update-bot-only`, and their description says only
     * "Relates to #NNN" — which extractOwning() rejects on purpose, so the bot
     * cannot suppress an issue's patches by mentioning it. Correct, and it
     * left every bot MR paired to nothing.
     *
     * @param string $pathWithNamespace e.g. "issue/pathauto-3616056"
     */
    public static function fromForkPath(string $pathWithNamespace): ?int
    {
        if (preg_match('#^issue/.+-(\d{4,})$#', trim($pathWithNamespace), $m) !== 1) {
            return null;
        }

        return (int) $m[1];
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

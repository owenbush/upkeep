<?php

declare(strict_types=1);

namespace Upkeep\Patches;

use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueFile;

/**
 * Which patch on an issue the operator meant.
 *
 * An issue routinely carries several: an original, two re-rolls, an interdiff,
 * and a screenshot. Picking the wrong one wastes a full environment build and
 * reports a verdict on code nobody submitted, so the rules are explicit rather
 * than clever:
 *
 *   --file=NAME   exactly that attachment, matched case-insensitively on the
 *                 filename; an unmatched name is a problem, never a silent
 *                 fallback to something else.
 *   --latest      the newest patch, no question asked.
 *   neither       one patch settles itself; several are ambiguous, and the
 *                 command decides whether to ask or to take the newest.
 *
 * "Newest" is Issue::latestPatch()'s ordering — the drupal.org comment number
 * when the filename carries one, the upload timestamp otherwise — so the
 * ordering shown in a picker is the same one `upkeep patches` prints.
 */
final class PatchSelector
{
    public static function select(Issue $issue, ?string $file, bool $latest): PatchSelection
    {
        $candidates = self::candidates($issue);

        if ($candidates === []) {
            return PatchSelection::problem(sprintf(
                'Issue #%d has no patch files attached (%d attachment(s), none of them a .patch or .diff).',
                $issue->nid,
                \count($issue->files),
            ));
        }

        if ($file !== null) {
            foreach ($candidates as $candidate) {
                if (strcasecmp($candidate->name, $file) === 0) {
                    // Newest first, so a name shared by several re-uploads
                    // settles on the most recent of them. Picking an older one
                    // is what --url is for: drupal.org keeps the filenames
                    // identical but the URLs distinct.
                    return PatchSelection::settled($candidate, $candidates);
                }
            }

            return PatchSelection::problem(sprintf(
                "Issue #%d has no patch named \"%s\". It carries:\n  %s",
                $issue->nid,
                $file,
                implode("\n  ", self::labels($candidates)),
            ));
        }

        if ($latest || \count($candidates) === 1) {
            return PatchSelection::settled($candidates[0], $candidates);
        }

        return PatchSelection::ambiguous($candidates);
    }

    /**
     * Every patch on the issue, newest first.
     *
     * @return list<IssueFile>
     */
    public static function candidates(Issue $issue): array
    {
        $patches = array_values(array_filter(
            $issue->files,
            static fn (IssueFile $f): bool => $f->isPatch(),
        ));

        usort($patches, static function (IssueFile $a, IssueFile $b): int {
            $ca = $a->commentNumber();
            $cb = $b->commentNumber();
            if ($ca !== null && $cb !== null) {
                return $cb <=> $ca;
            }

            return $b->timestamp <=> $a->timestamp;
        });

        return $patches;
    }

    /**
     * Descriptions for a list of patches, guaranteed distinct.
     *
     * Distinctness is not cosmetic. The Project Update Bot re-uploads its
     * patch under the *same* filename on every run, so a real issue routinely
     * carries four attachments called
     * `entity_type_access_conditions.1.0.1.rector.patch` differing only by
     * date and URL. A picker offering four identical lines cannot be answered,
     * and a prompt whose answer is matched back by label would resolve every
     * one of them to the first. The upload date usually separates them; when
     * even that collides, an ordinal does.
     *
     * @param list<IssueFile> $patches
     * @return list<string>
     */
    public static function labels(array $patches): array
    {
        $labels = [];
        $seen = [];
        foreach ($patches as $patch) {
            $label = self::describe($patch);
            if (isset($seen[$label])) {
                $label .= sprintf(' (#%d)', ++$seen[$label]);
            } else {
                $seen[$label] = 1;
            }
            $labels[] = $label;
        }

        return $labels;
    }

    /**
     * The one-line description of a patch a picker offers, e.g.
     * "3597808-9-d11.patch (comment 9, 4.2 KB)".
     */
    public static function describe(IssueFile $patch): string
    {
        $comment = $patch->commentNumber();
        $parts = [];
        if ($comment !== null) {
            $parts[] = 'comment ' . $comment;
        }
        if ($patch->size > 0) {
            $parts[] = self::humanSize($patch->size);
        }
        if ($patch->timestamp > 0) {
            $parts[] = gmdate('Y-m-d', $patch->timestamp);
        }

        return $parts === []
            ? $patch->name
            : sprintf('%s (%s)', $patch->name, implode(', ', $parts));
    }

    private static function humanSize(int $bytes): string
    {
        if ($bytes < 1024) {
            return $bytes . ' B';
        }
        if ($bytes < 1024 * 1024) {
            return sprintf('%.1f KB', $bytes / 1024);
        }

        return sprintf('%.1f MB', $bytes / (1024 * 1024));
    }
}

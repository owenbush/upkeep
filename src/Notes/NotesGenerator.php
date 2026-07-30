<?php

declare(strict_types=1);

namespace Upkeep\Notes;

use Upkeep\Config\BotPattern;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Tag;

/**
 * Turns a module's merged-MR history into a paste-ready Markdown release-notes
 * draft for a Drupal.org release node.
 *
 * Pure formatting/grouping logic (unit-tested); fetching is the command's job.
 * Bot compatibility MRs — the homogeneous noise — are compressed under one
 * "Compatibility updates" heading; everything else is listed individually
 * under "Changes". Bot classification is author-only here, via the shared
 * BotPattern (task 12's gate uses the stricter full match).
 */
final readonly class NotesGenerator
{
    public function __construct(
        private BotPattern $botPattern = new BotPattern(),
    ) {
    }

    /**
     * Select the latest tag by commit/creation date. Tags without a resolvable
     * date are skipped: an undated tag cannot serve as a "since" boundary.
     *
     * @param list<Tag> $tags
     */
    public static function latestTag(array $tags): ?Tag
    {
        $latest = null;
        foreach ($tags as $tag) {
            if ($tag->createdAt === null) {
                continue;
            }
            if ($latest === null || $tag->createdAt > $latest->createdAt) {
                $latest = $tag;
            }
        }

        return $latest;
    }

    /**
     * Render the release-notes draft.
     *
     * @param Tag|null            $sinceTag the latest tag, or null when the project has none
     * @param list<MergeRequest>  $merged   MRs merged since that tag (or the full history when tagless)
     */
    public function generate(string $module, ?Tag $sinceTag, array $merged): string
    {
        $lines = [$this->heading($module, $sinceTag)];

        if ($sinceTag === null) {
            $lines[] = '';
            $lines[] = 'No previous tag exists; the list below covers every merged merge request.';
        }

        if ($merged === []) {
            $lines[] = '';
            $lines[] = $sinceTag === null
                ? 'No merge requests have been merged in this project.'
                : sprintf('No merge requests have been merged since tag %s.', $sinceTag->name);

            return implode("\n", $lines);
        }

        $bot = [];
        $other = [];
        foreach ($merged as $mr) {
            if ($this->botPattern->matchesAuthor($mr->authorUsername, $mr->authorId)) {
                $bot[] = $mr;
            } else {
                $other[] = $mr;
            }
        }

        foreach ([['Compatibility updates', $bot], ['Changes', $other]] as [$title, $group]) {
            if ($group === []) {
                continue;
            }
            $lines[] = '';
            $lines[] = '### ' . $title;
            $lines[] = '';
            foreach ($group as $mr) {
                $lines[] = self::entry($mr);
            }
        }

        return implode("\n", $lines);
    }

    private function heading(string $module, ?Tag $sinceTag): string
    {
        if ($sinceTag === null) {
            return sprintf('## %s — full merged history (no previous tag)', $module);
        }

        return sprintf(
            '## %s — since %s (%s)',
            $module,
            $sinceTag->name,
            $sinceTag->createdAt?->format('Y-m-d') ?? 'date unknown',
        );
    }

    private static function entry(MergeRequest $mr): string
    {
        return sprintf('- %s ([!%d](%s) by %s)', $mr->title, $mr->iid, $mr->webUrl, $mr->authorUsername);
    }
}

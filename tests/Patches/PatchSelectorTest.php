<?php

declare(strict_types=1);

namespace Upkeep\Tests\Patches;

use PHPUnit\Framework\TestCase;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueFile;
use Upkeep\Drupal\IssueStatus;
use Upkeep\Patches\PatchSelector;

/**
 * Which patch on an issue the operator meant.
 *
 * Getting this wrong is expensive in a way most selection bugs are not: the
 * command then builds an environment, applies something nobody named, and
 * reports a verdict on it. So an unmatched name is a refusal, never a
 * fallback, and "several, none named" is a question rather than a guess.
 */
final class PatchSelectorTest extends TestCase
{
    private static function file(string $name, int $timestamp = 1700000000, int $size = 2048): IssueFile
    {
        return new IssueFile($name, 'https://www.drupal.org/files/issues/' . $name, $size, $timestamp);
    }

    /** @param list<IssueFile> $files */
    private static function issue(array $files, int $nid = 3597808): Issue
    {
        return new Issue(
            nid: $nid,
            title: 'Automated Drupal 12 compatibility fixes',
            status: IssueStatus::NeedsReview,
            url: 'https://www.drupal.org/node/' . $nid,
            project: 'widget',
            priority: 200,
            version: '1.0.x-dev',
            component: 'Code',
            category: 'Task',
            files: $files,
        );
    }

    public function testASinglePatchSettlesItselfWithoutAsking(): void
    {
        $selection = PatchSelector::select(self::issue([self::file('3597808-2-fix.patch')]), null, false);

        self::assertFalse($selection->ambiguous);
        self::assertSame('3597808-2-fix.patch', $selection->chosen?->name);
    }

    /**
     * Several patches and none named is neither an answer nor an error — the
     * candidates come back so the command can ask.
     */
    public function testSeveralPatchesWithNoneNamedIsAmbiguousRatherThanGuessed(): void
    {
        $selection = PatchSelector::select(
            self::issue([self::file('3597808-2-fix.patch'), self::file('3597808-9-fix.patch')]),
            null,
            false,
        );

        self::assertTrue($selection->ambiguous);
        self::assertNull($selection->chosen);
        self::assertCount(2, $selection->candidates);
        self::assertSame('3597808-9-fix.patch', $selection->candidates[0]->name, 'newest first');
    }

    public function testLatestTakesTheNewestWithoutAsking(): void
    {
        $selection = PatchSelector::select(
            self::issue([self::file('3597808-2-fix.patch'), self::file('3597808-9-fix.patch')]),
            null,
            true,
        );

        self::assertFalse($selection->ambiguous);
        self::assertSame('3597808-9-fix.patch', $selection->chosen?->name);
    }

    public function testANamedFileWinsOverEveryOtherRule(): void
    {
        $selection = PatchSelector::select(
            self::issue([self::file('3597808-2-fix.patch'), self::file('3597808-9-fix.patch')]),
            '3597808-2-fix.patch',
            true,
        );

        self::assertSame('3597808-2-fix.patch', $selection->chosen?->name, '--file beats --latest');
    }

    public function testANamedFileIsMatchedCaseInsensitively(): void
    {
        $selection = PatchSelector::select(
            self::issue([self::file('3597808-9-D11.patch')]),
            '3597808-9-d11.patch',
            false,
        );

        self::assertSame('3597808-9-D11.patch', $selection->chosen?->name);
    }

    /**
     * An unmatched --file must never fall back to something else: applying a
     * different patch than the one named would report a verdict on code the
     * operator did not ask about.
     */
    public function testAnUnmatchedFileNameIsRefusedAndListsWhatIsThere(): void
    {
        $selection = PatchSelector::select(
            self::issue([self::file('3597808-2-fix.patch'), self::file('3597808-9-fix.patch')]),
            'nope.patch',
            false,
        );

        self::assertNull($selection->chosen);
        self::assertFalse($selection->ambiguous);
        self::assertStringContainsString('no patch named "nope.patch"', (string) $selection->problem);
        self::assertStringContainsString('3597808-2-fix.patch', (string) $selection->problem);
        self::assertStringContainsString('3597808-9-fix.patch', (string) $selection->problem);
    }

    /**
     * An issue can carry attachments without carrying a patch — a screenshot,
     * an interdiff named .txt, a profiling dump. The count is reported so the
     * message does not read as "no attachments" when there plainly are some.
     */
    public function testAnIssueWithAttachmentsButNoPatchesSaysSo(): void
    {
        $selection = PatchSelector::select(
            self::issue([self::file('before.png'), self::file('notes.txt')]),
            null,
            false,
        );

        self::assertNull($selection->chosen);
        self::assertStringContainsString('no patch files attached', (string) $selection->problem);
        self::assertStringContainsString('2 attachment(s)', (string) $selection->problem);
    }

    public function testAnIssueWithNoAttachmentsAtAllSaysSo(): void
    {
        $selection = PatchSelector::select(self::issue([]), null, false);

        self::assertStringContainsString('no patch files attached', (string) $selection->problem);
    }

    /**
     * Ordering is the one `upkeep patches` prints: the drupal.org comment
     * number when the filename carries one, so a picker and the table agree
     * on which patch is newest.
     */
    public function testCandidatesAreOrderedByCommentNumberThenTimestamp(): void
    {
        $candidates = PatchSelector::candidates(self::issue([
            self::file('3597808-4-fix.patch'),
            self::file('3597808-12-fix.patch'),
            self::file('3597808-9-fix.patch'),
        ]));

        self::assertSame(
            ['3597808-12-fix.patch', '3597808-9-fix.patch', '3597808-4-fix.patch'],
            array_map(static fn (IssueFile $f): string => $f->name, $candidates),
        );
    }

    public function testCandidatesWithoutCommentNumbersFallBackToUploadTime(): void
    {
        $candidates = PatchSelector::candidates(self::issue([
            self::file('widget.1.0.1.rector.patch', 1700000000),
            self::file('widget-fix.patch', 1800000000),
        ]));

        self::assertSame('widget-fix.patch', $candidates[0]->name);
    }

    /** @return iterable<string, array{IssueFile, string}> */
    public static function descriptionCases(): iterable
    {
        yield 'comment number, size and date' => [
            self::file('3597808-9-fix.patch', 1705400000, 4300),
            '3597808-9-fix.patch (comment 9, 4.2 KB, 2024-01-16)',
        ];

        yield 'no comment number in the filename' => [
            self::file('widget.1.0.1.rector.patch', 1705400000, 900),
            'widget.1.0.1.rector.patch (900 B, 2024-01-16)',
        ];

        yield 'a large patch' => [
            self::file('3597808-9-fix.patch', 1705400000, 3 * 1024 * 1024),
            '3597808-9-fix.patch (comment 9, 3.0 MB, 2024-01-16)',
        ];

        // A patch reached through --url has neither size nor timestamp: the
        // description degrades to the bare name rather than claiming "0 B".
        yield 'nothing but a name' => [
            new IssueFile('handed-to-us.patch', 'https://example.test/handed-to-us.patch', 0, 0),
            'handed-to-us.patch',
        ];
    }

    /**
     * @param IssueFile $patch
     */
    #[\PHPUnit\Framework\Attributes\DataProvider('descriptionCases')]
    public function testDescriptionIsWhatAPickerOffers(IssueFile $patch, string $expected): void
    {
        self::assertSame($expected, PatchSelector::describe($patch));
    }

    /**
     * The shape a real drupal.org issue has: the Project Update Bot uploads
     * its patch under the *same* filename on every run, so the issue carries
     * several attachments differing only by date and URL. Taken verbatim from
     * project/entity_type_access_conditions issue #3597857, which carries four
     * of them.
     *
     * A picker offering four identical lines cannot be answered, and a prompt
     * matched back by label would resolve every one of them to the first.
     */
    public function testRepeatedBotFilenamesAreStillDistinguishable(): void
    {
        $name = 'entity_type_access_conditions.1.0.1.rector.patch';
        $labels = PatchSelector::labels([
            self::file($name, 1783803674, 0),
            self::file($name, 1782410474, 0),
            self::file($name, 1781607698, 0),
            self::file($name, 1781212289, 0),
        ]);

        self::assertSame($labels, array_unique($labels));
        self::assertSame([
            $name . ' (2026-07-11)',
            $name . ' (2026-06-25)',
            $name . ' (2026-06-16)',
            $name . ' (2026-06-11)',
        ], $labels);
    }

    /**
     * Two uploads on the same day under the same name: the date no longer
     * separates them, so an ordinal does.
     */
    public function testLabelsStayDistinctWhenEvenTheDateCollides(): void
    {
        $labels = PatchSelector::labels([
            self::file('bot.patch', 1783803674, 0),
            self::file('bot.patch', 1783803674, 0),
            self::file('bot.patch', 1783803674, 0),
        ]);

        self::assertSame($labels, array_unique($labels));
        self::assertSame([
            'bot.patch (2026-07-11)',
            'bot.patch (2026-07-11) (#2)',
            'bot.patch (2026-07-11) (#3)',
        ], $labels);
    }

    /**
     * A --file that several attachments answer to settles on the newest of
     * them; --url is how an older upload under the same name is reached,
     * because drupal.org keeps the names identical but the URLs distinct.
     */
    public function testANameSharedBySeveralUploadsSettlesOnTheNewest(): void
    {
        $newest = new IssueFile('bot.patch', 'https://example.test/2026-07-11/bot.patch', 0, 1783803674);
        $older = new IssueFile('bot.patch', 'https://example.test/2026-06-25/bot.patch', 0, 1782410474);

        $selection = PatchSelector::select(self::issue([$older, $newest]), 'bot.patch', false);

        self::assertSame('https://example.test/2026-07-11/bot.patch', $selection->chosen?->url);
    }

    /**
     * The listing an unmatched --file prints has to be usable, which means
     * distinguishable — four identical filenames would be no guidance at all.
     */
    public function testTheUnmatchedFileListingDistinguishesRepeatedNames(): void
    {
        $selection = PatchSelector::select(
            self::issue([
                self::file('bot.patch', 1783803674, 0),
                self::file('bot.patch', 1782410474, 0),
            ]),
            'nope.patch',
            false,
        );

        self::assertStringContainsString('bot.patch (2026-07-11)', (string) $selection->problem);
        self::assertStringContainsString('bot.patch (2026-06-25)', (string) $selection->problem);
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Notes;

use PHPUnit\Framework\TestCase;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Tag;
use Upkeep\Notes\NotesGenerator;

final class NotesGeneratorTest extends TestCase
{
    private static function mr(
        int $iid,
        string $title,
        string $author = 'owenbush',
        ?int $authorId = 12345,
        string $sourceBranch = 'some-branch',
    ): MergeRequest {
        return new MergeRequest(
            iid: $iid,
            title: $title,
            state: 'merged',
            authorUsername: $author,
            authorId: $authorId,
            sourceBranch: $sourceBranch,
            targetBranch: '1.0.x',
            draft: false,
            detailedMergeStatus: null,
            headSha: null,
            webUrl: 'https://git.drupalcode.org/project/conditions_helper/-/merge_requests/' . $iid,
        );
    }

    private static function tag(string $name, ?string $date): Tag
    {
        return new Tag($name, 'abc123', $date === null ? null : new \DateTimeImmutable($date));
    }

    // --- latest tag selection -------------------------------------------

    public function testLatestTagPicksTheNewestByCreationDateRegardlessOfOrder(): void
    {
        $latest = NotesGenerator::latestTag([
            self::tag('1.0.0', '2025-01-10T10:00:00+00:00'),
            self::tag('1.0.1', '2025-06-01T10:00:00+00:00'),
        ]);

        self::assertNotNull($latest);
        self::assertSame('1.0.1', $latest->name);
    }

    public function testLatestTagIgnoresTagsWithoutADate(): void
    {
        $latest = NotesGenerator::latestTag([
            self::tag('broken', null),
            self::tag('1.0.0', '2025-01-10T10:00:00+00:00'),
        ]);

        self::assertNotNull($latest);
        self::assertSame('1.0.0', $latest->name);
    }

    public function testLatestTagOfNothingIsNull(): void
    {
        self::assertNull(NotesGenerator::latestTag([]));
        // All tags undated: none is safely selectable as a "since" boundary.
        self::assertNull(NotesGenerator::latestTag([self::tag('broken', null)]));
    }

    // --- grouping and formatting ----------------------------------------

    public function testBotMrsGroupUnderCompatibilityUpdatesAndOthersUnderChanges(): void
    {
        $generator = new NotesGenerator();
        $markdown = $generator->generate('conditions_helper', self::tag('1.0.1', '2025-06-01T10:00:00+00:00'), [
            self::mr(1, 'Automated Project Update Bot fixes', 'Project-Update-Bot', 66574, 'project-update-bot-only'),
            self::mr(3, 'Fix conditions being ignored on cached pages'),
        ]);

        self::assertSame(<<<'MD'
            ## conditions_helper — since 1.0.1 (2025-06-01)

            ### Compatibility updates

            - Automated Project Update Bot fixes ([!1](https://git.drupalcode.org/project/conditions_helper/-/merge_requests/1) by Project-Update-Bot)

            ### Changes

            - Fix conditions being ignored on cached pages ([!3](https://git.drupalcode.org/project/conditions_helper/-/merge_requests/3) by owenbush)
            MD, $markdown);
    }

    public function testBotAuthorIsRecognizedByIdWhenTheUsernameDiffers(): void
    {
        $generator = new NotesGenerator();
        $markdown = $generator->generate('conditions_helper', self::tag('1.0.1', '2025-06-01T10:00:00+00:00'), [
            self::mr(1, 'Automated Project Update Bot fixes', 'Renamed-Bot', 66574),
        ]);

        self::assertStringContainsString('### Compatibility updates', $markdown);
        self::assertStringNotContainsString('### Changes', $markdown);
    }

    public function testOnlyHumanMrsProducesNoCompatibilityHeading(): void
    {
        $generator = new NotesGenerator();
        $markdown = $generator->generate('conditions_helper', self::tag('1.0.1', '2025-06-01T10:00:00+00:00'), [
            self::mr(4, 'Add settings form'),
        ]);

        self::assertStringNotContainsString('### Compatibility updates', $markdown);
        self::assertStringContainsString('### Changes', $markdown);
    }

    // --- edge case: no merges since the last tag -------------------------

    public function testNoMergesSinceTheLastTagReportsExactlyThat(): void
    {
        $generator = new NotesGenerator();
        $markdown = $generator->generate('conditions_helper', self::tag('1.0.1', '2025-06-01T10:00:00+00:00'), []);

        self::assertSame(<<<'MD'
            ## conditions_helper — since 1.0.1 (2025-06-01)

            No merge requests have been merged since tag 1.0.1.
            MD, $markdown);
    }

    // --- edge case: no tags at all ---------------------------------------

    public function testNoPreviousTagListsFullHistoryWithAClearNote(): void
    {
        $generator = new NotesGenerator();
        $markdown = $generator->generate('conditions_helper', null, [
            self::mr(1, 'Automated Project Update Bot fixes', 'Project-Update-Bot', 66574, 'project-update-bot-only'),
            self::mr(2, 'Initial feature work'),
        ]);

        self::assertSame(<<<'MD'
            ## conditions_helper — full merged history (no previous tag)

            No previous tag exists; the list below covers every merged merge request.

            ### Compatibility updates

            - Automated Project Update Bot fixes ([!1](https://git.drupalcode.org/project/conditions_helper/-/merge_requests/1) by Project-Update-Bot)

            ### Changes

            - Initial feature work ([!2](https://git.drupalcode.org/project/conditions_helper/-/merge_requests/2) by owenbush)
            MD, $markdown);
    }

    public function testNoTagsAndNoMergesStillExitsCleanlyWithAMessage(): void
    {
        $generator = new NotesGenerator();
        $markdown = $generator->generate('conditions_helper', null, []);

        self::assertSame(<<<'MD'
            ## conditions_helper — full merged history (no previous tag)

            No previous tag exists; the list below covers every merged merge request.

            No merge requests have been merged in this project.
            MD, $markdown);
    }
}

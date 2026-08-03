<?php

declare(strict_types=1);

namespace Upkeep\Tests\Drupal;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueFile;
use Upkeep\Drupal\IssueStatus;

/**
 * The drupal.org issue model. Two things carry weight here: what makes a
 * payload unusable (an issue upkeep cannot reason about must not become a
 * half-built model), and the API-array round trip — the dashboard cache
 * serialises issues with toApiArray() and reads them back through fromApi(),
 * so the two have to be exact inverses or cached issues silently change.
 */
final class IssueTest extends TestCase
{
    /**
     * @param array<array-key, mixed> $overrides
     *
     * @return array<array-key, mixed>
     */
    private static function payload(array $overrides = []): array
    {
        return $overrides + [
            'nid' => 3467675,
            'title' => 'Make URL field required by default',
            'url' => 'https://www.drupal.org/project/widget/issues/3467675',
            'field_issue_status' => '8',
            'field_issue_priority' => '300',
            'field_issue_version' => '2.0.x-dev',
            'field_issue_component' => 'Code',
            'field_issue_category' => '1',
            'field_project' => ['machine_name' => 'widget'],
        ];
    }

    /**
     * @return iterable<string, array{array<array-key, mixed>}>
     */
    public static function unusablePayloads(): iterable
    {
        yield 'no node id' => [['title' => 'orphan', 'field_issue_status' => '8']];
        yield 'node id is not a number' => [['nid' => 'abc', 'field_issue_status' => '8']];
        yield 'no status' => [['nid' => 1, 'title' => 'statusless']];
        yield 'a status upkeep does not know' => [['nid' => 1, 'field_issue_status' => '999']];
    }

    /**
     * @param array<array-key, mixed> $payload
     */
    #[DataProvider('unusablePayloads')]
    public function testAnIssueWithoutAUsableIdentityOrStatusIsNoIssueAtAll(array $payload): void
    {
        self::assertNull(Issue::fromApi($payload));
    }

    public function testEveryFieldSurvivesTheApiArrayRoundTripTheDashboardCacheDependsOn(): void
    {
        $original = Issue::fromApi(self::payload([
            'field_issue_files' => [
                ['file' => [
                    'filename' => '3467675-12.patch',
                    'url' => 'https://www.drupal.org/files/3467675-12.patch',
                    'filesize' => '2048',
                    'timestamp' => '1750000000',
                ]],
            ],
        ]));
        self::assertNotNull($original);

        $restored = Issue::fromApi($original->toApiArray());

        self::assertNotNull($restored);
        self::assertSame($original->nid, $restored->nid);
        self::assertSame($original->title, $restored->title);
        self::assertSame($original->status, $restored->status);
        self::assertSame($original->url, $restored->url);
        self::assertSame($original->project, $restored->project);
        self::assertSame($original->priority, $restored->priority);
        self::assertSame($original->version, $restored->version);
        self::assertSame($original->component, $restored->component);
        self::assertSame('Bug report', $restored->category);
        self::assertSame(1, $restored->patchCount());
        self::assertSame('3467675-12.patch', $restored->latestPatch()?->name);
    }

    public function testAnIssueWithNoProjectOrOptionalFieldsRoundTripsAsNulls(): void
    {
        $original = Issue::fromApi([
            'nid' => 42,
            'field_issue_status' => '8',
        ]);
        self::assertNotNull($original);

        $restored = Issue::fromApi($original->toApiArray());

        self::assertNotNull($restored);
        self::assertNull($restored->project);
        self::assertNull($restored->priority);
        self::assertNull($restored->category);
        self::assertNull($restored->priorityLabel());
        self::assertSame('https://www.drupal.org/node/42', $restored->url, 'A missing url is derived from the nid.');
    }

    public function testEveryCategoryAndPriorityIdHasAStableLabelAndSurvivesTheRoundTrip(): void
    {
        // The labels are what `upkeep issue` prints; the ids are what the cache
        // stores. An arm missing from either map would silently drop the field
        // on the way through the cache.
        $categories = [1 => 'Bug report', 2 => 'Task', 3 => 'Feature request', 4 => 'Support request', 5 => 'Plan'];
        foreach ($categories as $id => $label) {
            $issue = Issue::fromApi(self::payload(['field_issue_category' => (string) $id]));
            self::assertNotNull($issue);
            self::assertSame($label, $issue->category);

            $restored = Issue::fromApi($issue->toApiArray());
            self::assertNotNull($restored);
            self::assertSame($label, $restored->category);
        }

        $unknownCategory = Issue::fromApi(self::payload(['field_issue_category' => '99']));
        self::assertNotNull($unknownCategory);
        self::assertNull($unknownCategory->category);

        $priorities = [400 => 'Critical', 300 => 'Major', 200 => 'Normal', 100 => 'Minor', 250 => null];
        foreach ($priorities as $id => $label) {
            $issue = Issue::fromApi(self::payload(['field_issue_priority' => (string) $id]));
            self::assertNotNull($issue);
            self::assertSame($label, $issue->priorityLabel());
        }
    }

    public function testAttachmentsAreReadFromEitherApiShapeAndUnusableEntriesAreDropped(): void
    {
        // Depending on the endpoint the API returns attachments as a plain list
        // or wrapped in the Drupal 7 field-language envelope.
        $wrapped = Issue::fromApi(self::payload([
            'field_issue_files' => ['und' => [
                self::filePayload('3467675-2.patch'),
                'not-an-attachment',
                self::filePayload('screenshot.png'),
            ]],
        ]));
        $flat = Issue::fromApi(self::payload([
            'field_issue_files' => [self::filePayload('3467675-2.patch'), self::filePayload('screenshot.png')],
        ]));

        self::assertNotNull($wrapped);
        self::assertNotNull($flat);
        self::assertCount(2, $wrapped->files);
        self::assertSame(1, $wrapped->patchCount(), 'A .png attachment is not a patch.');
        self::assertSame(1, $flat->patchCount());
        $noAttachments = Issue::fromApi(self::payload());
        self::assertNotNull($noAttachments);
        self::assertSame(0, $noAttachments->patchCount());
        self::assertNull($noAttachments->latestPatch());
    }

    public function testTheLatestPatchIsTheHighestCommentNumberFallingBackToUploadTime(): void
    {
        // Drupal.org names patches {nid}-{comment#}.patch, and the comment
        // number — not the upload order — says which revision is newest. A
        // patch without one can only be ordered by when it was uploaded.
        $numbered = new Issue(
            1,
            'numbered',
            IssueStatus::NeedsReview,
            'https://www.drupal.org/node/1',
            null,
            null,
            null,
            null,
            null,
            [
                new IssueFile('1-14.patch', 'https://example.org/a', 10, 1_000),
                new IssueFile('1-27.patch', 'https://example.org/b', 10, 500),
            ],
        );
        $unnumbered = new Issue(
            2,
            'unnumbered',
            IssueStatus::NeedsReview,
            'https://www.drupal.org/node/2',
            null,
            null,
            null,
            null,
            null,
            [
                new IssueFile('fix.patch', 'https://example.org/c', 10, 500),
                new IssueFile('other-fix.patch', 'https://example.org/d', 10, 2_000),
            ],
        );

        self::assertSame('1-27.patch', $numbered->latestPatch()?->name);
        self::assertSame('other-fix.patch', $unnumbered->latestPatch()?->name);
    }

    /**
     * @return array<string, mixed>
     */
    private static function filePayload(string $name): array
    {
        return ['file' => [
            'filename' => $name,
            'url' => 'https://www.drupal.org/files/' . $name,
            'filesize' => '2048',
            'timestamp' => '1750000000',
        ]];
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Dashboard\ModuleSnapshot;

final class ModuleSnapshotTest extends TestCase
{
    /** @return array<array-key, mixed> */
    private static function projectPayload(): array
    {
        return [
            'id' => 4242,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'name' => 'Widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ];
    }

    /** @return array<array-key, mixed> */
    private static function mrPayload(): array
    {
        return [
            'iid' => 5,
            'title' => 'Issue #3467675: Fix widget',
            'state' => 'opened',
            'draft' => false,
            'author' => ['username' => 'alice', 'id' => 100],
            'source_branch' => '3467675-fix-widget',
            'target_branch' => '2.0.x',
            'detailed_merge_status' => 'mergeable',
            'sha' => 'abc123',
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/5',
            'description' => 'Fixes https://www.drupal.org/node/3467675',
            'head_pipeline' => [
                'id' => 77,
                'status' => 'success',
                'sha' => 'abc123',
                'web_url' => 'https://git.drupalcode.org/project/widget/-/pipelines/77',
            ],
        ];
    }

    /** @return array<array-key, mixed> */
    private static function issuePayload(): array
    {
        return [
            'nid' => 3467675,
            'title' => 'Widget field required',
            'field_issue_status' => '8',
            'url' => 'https://www.drupal.org/node/3467675',
            'field_project' => ['machine_name' => 'widget'],
            'field_issue_priority' => 200,
            'field_issue_version' => '2.0.x-dev',
            'field_issue_component' => 'Code',
            'field_issue_category' => 1,
        ];
    }

    public function testJsonRoundTrip(): void
    {
        $original = new ModuleSnapshot(
            new \DateTimeImmutable('2026-07-31T10:00:00+00:00'),
            self::projectPayload(),
            [self::mrPayload()],
            [3467675 => self::issuePayload()],
        );

        $restored = ModuleSnapshot::fromJson($original->toJson());

        self::assertNotNull($restored);
        self::assertSame(
            $original->fetchedAt->getTimestamp(),
            $restored->fetchedAt->getTimestamp(),
        );
        self::assertSame($original->projectData, $restored->projectData);
        self::assertSame($original->mrData, $restored->mrData);
        self::assertSame($original->issueData, $restored->issueData);
    }

    public function testProjectReconstruction(): void
    {
        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [],
            [],
        );

        $project = $snapshot->project();

        self::assertSame(4242, $project->id);
        self::assertSame('project/widget', $project->pathWithNamespace);
    }

    public function testMergeRequestReconstruction(): void
    {
        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [self::mrPayload()],
            [],
        );

        $mrs = $snapshot->mergeRequests();

        self::assertCount(1, $mrs);
        self::assertSame(5, $mrs[0]->iid);
        self::assertSame('Issue #3467675: Fix widget', $mrs[0]->title);
        self::assertNotNull($mrs[0]->headPipeline);
        self::assertSame('success', $mrs[0]->headPipeline->rawStatus);
        self::assertSame('Fixes https://www.drupal.org/node/3467675', $mrs[0]->description);
    }

    public function testIssueReconstruction(): void
    {
        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [],
            [3467675 => self::issuePayload()],
        );

        $issue = $snapshot->issue(3467675);

        self::assertNotNull($issue);
        self::assertSame(3467675, $issue->nid);
        self::assertSame('Needs review', $issue->status->label());
        self::assertSame('Normal', $issue->priorityLabel());
        self::assertSame('Bug report', $issue->category);
    }

    public function testIssueMissingFromSnapshotReturnsNull(): void
    {
        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [],
            [],
        );

        self::assertNull($snapshot->issue(9999));
    }

    public function testNullIssueDataPreservedInRoundTrip(): void
    {
        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [],
            [3467675 => null],
        );

        $restored = ModuleSnapshot::fromJson($snapshot->toJson());

        self::assertNotNull($restored);
        self::assertNull($restored->issue(3467675));
    }

    public function testFromJsonRejectsInvalidJson(): void
    {
        self::assertNull(ModuleSnapshot::fromJson('not json'));
    }

    public function testFromJsonRejectsMissingFields(): void
    {
        self::assertNull(ModuleSnapshot::fromJson('{"fetched_at": "2026-01-01T00:00:00+00:00"}'));
    }

    /**
     * A cache file is untrusted input: it is on disk, it outlives the format
     * that wrote it, and anything half-shaped must read as "no cache" so the
     * caller re-fetches — never as a partial model reaching the dashboard.
     *
     * @return iterable<string, array{string}>
     */
    public static function unusableCacheFiles(): iterable
    {
        yield 'not JSON at all' => ['not json'];
        yield 'JSON, but not an object' => ['[1, 2, 3]'];
        yield 'JSON scalar' => ['42'];
        yield 'missing project and merge_requests' => ['{"fetched_at": "2026-01-01T00:00:00+00:00"}'];
        yield 'fetched_at is not a string' => ['{"fetched_at": 1767225600, "project": {}, "merge_requests": []}'];
        yield 'fetched_at is not a date' => ['{"fetched_at": "whenever", "project": {}, "merge_requests": []}'];
        yield 'project is a scalar' => ['{"fetched_at": "2026-01-01T00:00:00+00:00", "project": 1,'
            . ' "merge_requests": []}'];
    }

    #[DataProvider('unusableCacheFiles')]
    public function testAnyHalfShapedCacheFileReadsAsNoCache(string $json): void
    {
        self::assertNull(ModuleSnapshot::fromJson($json));
    }

    public function testUnusableEntriesInsideAReadableCacheFileAreDroppedNotPropagated(): void
    {
        // The envelope is fine, so the snapshot loads — but the individual
        // merge-request and issue entries are still validated one by one. A
        // null issue entry is meaningful (the lookup was made and failed) and
        // must survive; a scalar payload or an unusable key must not.
        $json = json_encode([
            'fetched_at' => '2026-07-31T10:00:00+00:00',
            'project' => self::projectPayload(),
            'merge_requests' => [self::mrPayload(), 'not-a-payload'],
            'issues' => [
                '3467675' => self::issuePayload(),
                'not-a-nid' => self::issuePayload(),
                '3467676' => null,
                '3467677' => 'not-a-payload',
            ],
        ], \JSON_THROW_ON_ERROR);

        $snapshot = ModuleSnapshot::fromJson($json);

        self::assertNotNull($snapshot);
        self::assertCount(1, $snapshot->mergeRequests());
        self::assertNotNull($snapshot->issue(3467675));
        self::assertNull($snapshot->issue(3467676));
        self::assertNull($snapshot->issue(3467677));
    }

    public function testAnIssuesSectionThatIsNotAMappingLeavesTheSnapshotWithNoIssues(): void
    {
        $json = json_encode([
            'fetched_at' => '2026-07-31T10:00:00+00:00',
            'project' => self::projectPayload(),
            'merge_requests' => [],
            'issues' => 'nonsense',
        ], \JSON_THROW_ON_ERROR);

        $snapshot = ModuleSnapshot::fromJson($json);

        self::assertNotNull($snapshot);
        self::assertNull($snapshot->issue(3467675));
    }

    public function testAgeLabelJustNow(): void
    {
        $snapshot = new ModuleSnapshot(new \DateTimeImmutable(), [], [], []);
        self::assertSame('just now', $snapshot->ageLabel(new \DateTimeImmutable()));
    }

    public function testAgeLabelMinutes(): void
    {
        $snapshot = new ModuleSnapshot(new \DateTimeImmutable('-15 minutes'), [], [], []);
        self::assertSame('15m ago', $snapshot->ageLabel(new \DateTimeImmutable()));
    }

    public function testAgeLabelHours(): void
    {
        $snapshot = new ModuleSnapshot(new \DateTimeImmutable('-3 hours'), [], [], []);
        self::assertSame('3h ago', $snapshot->ageLabel(new \DateTimeImmutable()));
    }

    public function testAgeLabelDays(): void
    {
        $snapshot = new ModuleSnapshot(new \DateTimeImmutable('-2 days'), [], [], []);
        self::assertSame('2d ago', $snapshot->ageLabel(new \DateTimeImmutable()));
    }
}

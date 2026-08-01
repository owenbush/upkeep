<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\TestCase;
use Upkeep\Dashboard\ModuleSnapshot;

final class ModuleSnapshotTest extends TestCase
{
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

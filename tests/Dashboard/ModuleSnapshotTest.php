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
            [self::issuePayload()],
        );

        $restored = ModuleSnapshot::fromJson($original->toJson());

        self::assertNotNull($restored);
        self::assertSame(
            $original->fetchedAt->getTimestamp(),
            $restored->fetchedAt->getTimestamp(),
        );
        self::assertSame($original->projectData, $restored->projectData);
        self::assertSame($original->mrData, $restored->mrData);
        self::assertSame($original->patchIssueData, $restored->patchIssueData);
    }

    /**
     * Landings survive the cache, so a dashboard read from disk knows what
     * merged just as a fresh fetch does.
     */
    public function testLandingsAndTheForkMapRoundTrip(): void
    {
        $original = new ModuleSnapshot(
            new \DateTimeImmutable('2026-09-03T10:00:00+00:00'),
            self::projectPayload(),
            [],
            [],
            [self::mrPayload() + ['state' => 'merged', 'merged_at' => '2026-09-03T10:00:00Z']],
            [218528 => 3598272],
        );

        $restored = ModuleSnapshot::fromJson($original->toJson());

        self::assertNotNull($restored);
        self::assertSame([218528 => 3598272], $restored->forkNids);
        self::assertCount(1, $restored->mergedMergeRequests());
        self::assertSame('2026-09-03T10:00:00Z', $restored->mergedMergeRequests()[0]->mergedAt);
    }

    /**
     * A snapshot written before landings were tracked reads as "nothing known
     * to have merged" — the previous behaviour, not a wrong claim — and junk
     * in the fork map is dropped rather than reaching the models as mixed.
     */
    public function testAnOlderOrMalformedSnapshotDegradesRatherThanLying(): void
    {
        $old = ModuleSnapshot::fromJson((string) json_encode([
            'fetched_at' => '2026-01-01T00:00:00+00:00',
            'project' => self::projectPayload(),
            'merge_requests' => [],
        ]));

        self::assertNotNull($old);
        self::assertSame([], $old->forkNids);
        self::assertSame([], $old->mergedMergeRequests());

        $junk = ModuleSnapshot::fromJson((string) json_encode([
            'fetched_at' => '2026-01-01T00:00:00+00:00',
            'project' => self::projectPayload(),
            'merge_requests' => [],
            'fork_nids' => ['not-a-number' => 3598272, '218528' => 'not-a-nid', '999' => 3598272],
            'merged_merge_requests' => 'not a list',
        ]));

        self::assertNotNull($junk);
        self::assertSame([999 => 3598272], $junk->forkNids);
        self::assertSame([], $junk->mergedMergeRequests());
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
        // entries are still validated one by one. A scalar where a payload
        // belongs, or a constraint that is not a string, must not reach the
        // models as mixed.
        $json = json_encode([
            'fetched_at' => '2026-07-31T10:00:00+00:00',
            'project' => self::projectPayload(),
            'merge_requests' => [self::mrPayload(), 'not-a-payload'],
            'patch_issues' => [self::issuePayload(), 42],
            'core_constraints' => ['2.0.x' => '^10 || ^11', '1.0.x' => ['not', 'a', 'string'], '3.0.x' => ''],
        ], \JSON_THROW_ON_ERROR);

        $snapshot = ModuleSnapshot::fromJson($json);

        self::assertNotNull($snapshot);
        self::assertCount(1, $snapshot->mergeRequests());
        self::assertCount(1, $snapshot->patchIssues());
        self::assertSame(['2.0.x' => '^10 || ^11'], $snapshot->coreConstraints);
    }

    /**
     * What each branch declares about core survives the cache, so a dashboard
     * read from disk narrows the same way a fresh fetch does.
     */
    public function testCoreConstraintsRoundTrip(): void
    {
        $original = new ModuleSnapshot(
            new \DateTimeImmutable('2026-09-04T10:00:00+00:00'),
            self::projectPayload(),
            [],
            [],
            [],
            [],
            // Read live from git.drupalcode.org on 2026-09-04.
            ['1.0.x' => '^10 || ^11', '2.0.x' => '^10.1 || ^11 || ^12'],
        );

        $restored = ModuleSnapshot::fromJson($original->toJson());

        self::assertNotNull($restored);
        self::assertSame(
            ['1.0.x' => '^10 || ^11', '2.0.x' => '^10.1 || ^11 || ^12'],
            $restored->coreConstraints,
        );
    }

    /**
     * A snapshot written before branches declared anything, or one whose
     * constraints section is junk, reads as "cannot tell" — which every caller
     * turns back into the tracked core set. Never "supports nothing": a module
     * vanishing from the dashboard is the worst failure this tool has.
     */
    public function testAnAbsentOrUnusableConstraintsSectionReadsAsCannotTell(): void
    {
        foreach (['nonsense', 42, null] as $junk) {
            $json = json_encode([
                'fetched_at' => '2026-07-31T10:00:00+00:00',
                'project' => self::projectPayload(),
                'merge_requests' => [],
                'core_constraints' => $junk,
            ], \JSON_THROW_ON_ERROR);

            $snapshot = ModuleSnapshot::fromJson($json);

            self::assertNotNull($snapshot);
            self::assertSame([], $snapshot->coreConstraints);
        }

        // And a cache written before the field existed at all.
        $older = ModuleSnapshot::fromJson((string) json_encode([
            'fetched_at' => '2026-07-31T10:00:00+00:00',
            'project' => self::projectPayload(),
            'merge_requests' => [],
        ], \JSON_THROW_ON_ERROR));

        self::assertNotNull($older);
        self::assertSame([], $older->coreConstraints);
    }

    /**
     * The revision each merge request's evidence is about survives the cache,
     * so a dashboard read from disk tells fresh from stale the same way a
     * fresh fetch does.
     */
    public function testMergeRefShasRoundTrip(): void
    {
        $original = new ModuleSnapshot(
            new \DateTimeImmutable('2026-09-05T10:00:00+00:00'),
            self::projectPayload(),
            [],
            [],
            [],
            [],
            [],
            [7 => str_repeat('a', 40), 12 => str_repeat('b', 40)],
        );

        $restored = ModuleSnapshot::fromJson($original->toJson());

        self::assertNotNull($restored);
        self::assertSame([7 => str_repeat('a', 40), 12 => str_repeat('b', 40)], $restored->mergeRefShas);
    }

    /**
     * Anything that is not an iid pointing at something SHA-shaped is dropped.
     *
     * A cache file is untrusted input, and this value becomes the *revision* a
     * result is filed under — junk here would key evidence to a string nothing
     * can ever match, which reads as "never checked" forever.
     */
    public function testUnusableMergeRefShasAreDropped(): void
    {
        $restored = ModuleSnapshot::fromJson((string) json_encode([
            'fetched_at' => '2026-09-05T10:00:00+00:00',
            'project' => self::projectPayload(),
            'merge_requests' => [],
            'merge_ref_shas' => [
                '7' => str_repeat('a', 40),
                '8' => 'not-a-sha',
                '9' => 42,
                'not-an-iid' => str_repeat('c', 40),
                '10' => str_repeat('Z', 40),
            ],
        ], \JSON_THROW_ON_ERROR));

        self::assertNotNull($restored);
        self::assertSame([7 => str_repeat('a', 40)], $restored->mergeRefShas);
    }

    /**
     * A snapshot written before the field existed, or with junk in its place,
     * reads as "no merge refs known" — every merge request then falls back to
     * its head SHA, which is the behaviour that release had.
     */
    public function testAnAbsentOrUnusableMergeRefSectionReadsAsNoneKnown(): void
    {
        foreach ([null, 'nonsense', 42] as $junk) {
            $payload = [
                'fetched_at' => '2026-09-05T10:00:00+00:00',
                'project' => self::projectPayload(),
                'merge_requests' => [],
            ];
            if ($junk !== null) {
                $payload['merge_ref_shas'] = $junk;
            }

            $restored = ModuleSnapshot::fromJson((string) json_encode($payload, \JSON_THROW_ON_ERROR));

            self::assertNotNull($restored);
            self::assertSame([], $restored->mergeRefShas);
        }
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

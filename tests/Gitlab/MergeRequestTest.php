<?php

declare(strict_types=1);

namespace Upkeep\Tests\Gitlab;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Gitlab\MergeRequest;

/**
 * The emptiness signal, and its survival through the dashboard cache.
 *
 * An MR that exists and carries nothing is the shape that made `upkeep
 * patches` hide the patches it was written to surface, so what the model does
 * with a payload that cannot answer the question matters as much as what it
 * does with one that can.
 */
final class MergeRequestTest extends TestCase
{
    /**
     * @param array<string, mixed> $overrides
     *
     * @return array<string, mixed>
     */
    private static function payload(array $overrides = []): array
    {
        return $overrides + [
            'iid' => 7,
            'title' => 'Issue #3467675: Make URL field required',
            'state' => 'opened',
            'author' => ['username' => 'alice', 'id' => 100],
            'source_branch' => '3467675-make-url-required',
            'target_branch' => '2.0.x',
            'draft' => false,
            'sha' => 'head222',
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/7',
        ];
    }

    /** @return iterable<string, array{mixed, ?bool}> */
    public static function diffRefCases(): iterable
    {
        yield 'differing base and head carry changes' => [
            ['base_sha' => 'base111', 'head_sha' => 'head222'], true,
        ];

        // The real shape of an empty Project Update Bot draft on
        // git.drupalcode.org: every ref the same commit.
        yield 'identical base and head carry nothing' => [
            ['base_sha' => 'db6fb3a6', 'head_sha' => 'db6fb3a6', 'start_sha' => 'db6fb3a6'], false,
        ];

        // The list endpoint omits diff_refs; unknown must never read as false,
        // or every listed MR would look empty.
        yield 'absent diff refs are unknown' => [null, null];
        yield 'a half-populated diff ref is unknown' => [['base_sha' => 'base111'], null];
        yield 'a diff ref with no head is unknown' => [['head_sha' => 'head222'], null];
        yield 'a diff ref that is not an object is unknown' => ['nonsense', null];
    }

    #[DataProvider('diffRefCases')]
    public function testCarriesChangesReadsTheDiffRefsAndSaysSoWhenItCannotTell(
        mixed $diffRefs,
        ?bool $expected,
    ): void {
        $mr = MergeRequest::fromApi(self::payload(['diff_refs' => $diffRefs]));

        self::assertSame($expected, $mr->carriesChanges());
    }

    /**
     * `changes_count` is null on an empty MR — indistinguishable from "the
     * diff has not been generated yet" — and `detailed_merge_status` reports
     * `draft_status` for a draft, masking the emptiness underneath. Neither
     * may override the diff refs.
     */
    public function testNeitherChangesCountNorMergeStatusOverridesTheDiffRefs(): void
    {
        $mr = MergeRequest::fromApi(self::payload([
            'title' => 'Draft: Automated Project Update Bot fixes',
            'draft' => true,
            'changes_count' => null,
            'detailed_merge_status' => 'draft_status',
            'diff_refs' => ['base_sha' => 'db6fb3a6', 'head_sha' => 'db6fb3a6'],
        ]));

        self::assertTrue($mr->draft);
        self::assertSame('draft_status', $mr->detailedMergeStatus);
        self::assertFalse($mr->carriesChanges());
    }

    /**
     * Dashboard snapshots store raw payloads and read them back through
     * fromApi(), so a cached MR must answer the emptiness question exactly as
     * the live one did — otherwise `patches` would have to refetch every MR
     * the cache already knows about.
     */
    #[DataProvider('diffRefCases')]
    public function testEmptinessSurvivesTheCacheRoundTrip(mixed $diffRefs, ?bool $expected): void
    {
        $original = MergeRequest::fromApi(self::payload(['diff_refs' => $diffRefs]));
        $restored = MergeRequest::fromApi($original->toApiArray());

        self::assertSame($expected, $restored->carriesChanges());
        self::assertSame($original->iid, $restored->iid);
    }

    /**
     * A snapshot written before diff refs were cached has no such key at all.
     * It reads back as unknown, not as empty.
     */
    public function testASnapshotWithoutDiffRefsReadsBackAsUnknown(): void
    {
        $legacy = MergeRequest::fromApi(self::payload())->toApiArray();
        unset($legacy['diff_refs']);

        self::assertNull(MergeRequest::fromApi($legacy)->carriesChanges());
    }
}

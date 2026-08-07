<?php

declare(strict_types=1);

namespace Upkeep\Tests\Results;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;

/**
 * The namespace separation between merge-request and patch results.
 *
 * Both subjects are identified by a number and nothing keeps the ranges apart
 * — a module can plausibly have MR !3597808 while an issue carries node id
 * 3597808. The separation is therefore structural, and it is load-bearing: the
 * fast-lane gate reads merge-request entries, and a patch verdict surfacing
 * there would be evidence for merging a branch nobody checked.
 */
final class ResultKeyTest extends TestCase
{
    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/upkeep-resultkey-' . bin2hex(random_bytes(4));
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    public function testTheTwoSubjectsOccupyDifferentSegments(): void
    {
        self::assertSame('7', ResultKey::mergeRequest(7)->segment);
        self::assertSame('patch-7', ResultKey::patch(7)->segment);
        self::assertFalse(ResultKey::mergeRequest(7)->isPatch);
        self::assertTrue(ResultKey::patch(7)->isPatch);
    }

    /**
     * The same number under both subjects must resolve to two independent
     * entries — the collision this type exists to prevent.
     */
    public function testTheSameNumberUnderBothSubjectsDoesNotCollide(): void
    {
        $cache = new ResultsCache($this->dir);
        $mrSha = str_repeat('a', 40);
        $patchSha = str_repeat('b', 40);

        $cache->store('widget', ResultKey::mergeRequest(3597808), '11', $mrSha, new CheckRunResult([]));
        $cache->store('widget', ResultKey::patch(3597808), '11', $patchSha, new CheckRunResult([]));

        self::assertSame($mrSha, $cache->latest('widget', ResultKey::mergeRequest(3597808), '11')?->sha);
        self::assertSame($patchSha, $cache->latest('widget', ResultKey::patch(3597808), '11')?->sha);
        self::assertNull($cache->find('widget', ResultKey::mergeRequest(3597808), '11', $patchSha));
        self::assertNull($cache->find('widget', ResultKey::patch(3597808), '11', $mrSha));
    }

    /** @return iterable<string, array{int}> */
    public static function impossibleNumbers(): iterable
    {
        yield 'zero' => [0];
        yield 'negative' => [-3];
    }

    /**
     * Neither `!0` nor node 0 exists, so a key for one is refused rather than
     * becoming a directory that quietly collects results nothing can read.
     */
    #[DataProvider('impossibleNumbers')]
    public function testAnImpossibleIdentityIsRefused(int $number): void
    {
        $this->expectException(\InvalidArgumentException::class);
        ResultKey::mergeRequest($number);
    }

    #[DataProvider('impossibleNumbers')]
    public function testAnImpossibleIssueIdentityIsRefusedToo(int $number): void
    {
        $this->expectException(\InvalidArgumentException::class);
        $this->expectExceptionMessage('issue node id');
        ResultKey::patch($number);
    }
}

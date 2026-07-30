<?php

declare(strict_types=1);

namespace Upkeep\Tests\Results;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Results\ResultsCache;

final class ResultsCacheTest extends TestCase
{
    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/upkeep-results-' . bin2hex(random_bytes(4));
    }

    protected function tearDown(): void
    {
        if (is_dir($this->dir)) {
            $files = new \RecursiveIteratorIterator(
                new \RecursiveDirectoryIterator($this->dir, \FilesystemIterator::SKIP_DOTS),
                \RecursiveIteratorIterator::CHILD_FIRST,
            );
            foreach ($files as $file) {
                $file->isDir() ? rmdir($file->getPathname()) : unlink($file->getPathname());
            }
            rmdir($this->dir);
        }
    }

    public function testRoundTripPreservesEveryCheckField(): void
    {
        $cache = new ResultsCache($this->dir);
        $run = new CheckRunResult([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 1, "1 failure\n", 6.9),
            new CheckResult(CheckType::PhpStan, CheckStatus::Passed, 0, '[OK]', 2.1),
            new CheckResult(CheckType::Deprecation, CheckStatus::Unavailable, null, 'engine provides none', 0.0),
        ]);

        $cache->store('field_visibility_conditions', 2, '11', 'abc123', $run);
        $cached = $cache->find('field_visibility_conditions', 2, '11', 'abc123');

        self::assertNotNull($cached);
        self::assertSame('abc123', $cached->sha);
        self::assertFalse($cached->result->allPassed());
        self::assertCount(3, $cached->result->results);
        self::assertSame(CheckStatus::Failed, $cached->result->results[0]->status);
        self::assertSame(1, $cached->result->results[0]->exitCode);
        self::assertSame("1 failure\n", $cached->result->results[0]->output);
        self::assertSame(6.9, $cached->result->results[0]->durationSeconds);
        self::assertNull($cached->result->results[2]->exitCode);
    }

    public function testLatestPicksTheMostRecentRecordingAcrossShas(): void
    {
        $cache = new ResultsCache($this->dir);
        $green = new CheckRunResult([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.0)]);
        $red = new CheckRunResult([new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 1, 'FAIL', 1.0)]);

        $cache->store('m', 1, '11', 'older', $red, new \DateTimeImmutable('2026-07-01T00:00:00Z'));
        $cache->store('m', 1, '11', 'newer', $green, new \DateTimeImmutable('2026-07-02T00:00:00Z'));

        $latest = $cache->latest('m', 1, '11');
        self::assertNotNull($latest);
        self::assertSame('newer', $latest->sha);
        self::assertTrue($latest->result->allPassed());
    }

    public function testMissesAndMalformedFilesReadAsNull(): void
    {
        $cache = new ResultsCache($this->dir);
        self::assertNull($cache->find('m', 1, '11', 'nope'));
        self::assertNull($cache->latest('m', 1, '11'));

        mkdir($this->dir . '/m/1/11', 0o755, true);
        file_put_contents($this->dir . '/m/1/11/bad.json', '{not json');
        self::assertNull($cache->find('m', 1, '11', 'bad'));
        self::assertNull($cache->latest('m', 1, '11'));
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Results;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Filesystem\FilesystemException;
use Upkeep\Results\ResultsCache;

final class ResultsCacheTest extends TestCase
{
    private const SHA = '4b825dc642cb6eb9a060e54bf8d69288fbee4904';
    private const OTHER_SHA = 'e69de29bb2d1d6434b8b29ae775ad8c2e48c5391';

    private string $dir;

    protected function setUp(): void
    {
        $this->dir = (string) realpath(sys_get_temp_dir()) . '/upkeep-results-' . bin2hex(random_bytes(4));
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    public function testRoundTripPreservesEveryCheckField(): void
    {
        $cache = new ResultsCache($this->dir);
        $run = new CheckRunResult([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 1, "1 failure\n", 6.9),
            new CheckResult(CheckType::PhpStan, CheckStatus::Passed, 0, '[OK]', 2.1),
            new CheckResult(CheckType::Deprecation, CheckStatus::Unavailable, null, 'engine provides none', 0.0),
        ]);

        $cache->store('field_visibility_conditions', 2, '11', self::SHA, $run);
        $cached = $cache->find('field_visibility_conditions', 2, '11', self::SHA);

        self::assertNotNull($cached);
        self::assertSame(self::SHA, $cached->sha);
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

        $cache->store('m', 1, '11', self::SHA, $red, new \DateTimeImmutable('2026-07-01T00:00:00Z'));
        $cache->store('m', 1, '11', self::OTHER_SHA, $green, new \DateTimeImmutable('2026-07-02T00:00:00Z'));

        $latest = $cache->latest('m', 1, '11');
        self::assertNotNull($latest);
        self::assertSame(self::OTHER_SHA, $latest->sha);
        self::assertTrue($latest->result->allPassed());
    }

    public function testMissesAndMalformedFilesReadAsNull(): void
    {
        $cache = new ResultsCache($this->dir);
        self::assertNull($cache->find('m', 1, '11', self::SHA));
        self::assertNull($cache->latest('m', 1, '11'));

        mkdir($this->dir . '/m/1/11', 0o700, true);
        file_put_contents($this->dir . '/m/1/11/' . self::SHA . '.json', '{not json');
        self::assertNull($cache->find('m', 1, '11', self::SHA));
        self::assertNull($cache->latest('m', 1, '11'));
    }

    /**
     * The stored payload carries 4000 bytes of raw check output per check —
     * host paths, source fragments, stack traces — and is reachable by any
     * local account when world-readable.
     */
    public function testStoredResultsAreOwnerOnlyOnDiskBecauseTheyCarryRawCheckOutput(): void
    {
        $cache = new ResultsCache($this->dir);
        $cache->store('m', 1, '11', self::SHA, new CheckRunResult([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.0),
        ]));

        $file = $this->dir . '/m/1/11/' . self::SHA . '.json';
        self::assertSame(0o600, fileperms($file) & 0o777);
        self::assertSame(0o700, fileperms($this->dir . '/m/1/11') & 0o777);
        self::assertSame(0o700, fileperms($this->dir) & 0o777);
    }

    public function testAStoreIsAtomicSoAReaderNeverSeesAPartialResultFile(): void
    {
        $cache = new ResultsCache($this->dir);
        $first = new CheckRunResult([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.0)]);
        $cache->store('m', 1, '11', self::SHA, $first);

        $file = $this->dir . '/m/1/11/' . self::SHA . '.json';
        $handle = fopen($file, 'r');
        self::assertNotFalse($handle);

        $cache->store('m', 1, '11', self::SHA, new CheckRunResult([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 1, str_repeat('x', 3999), 2.0),
        ]));

        // The path never stops existing and the pre-store reader still sees a
        // complete, parseable file rather than a truncated one.
        self::assertTrue(is_file($file));
        $observed = stream_get_contents($handle);
        fclose($handle);
        self::assertIsArray(json_decode((string) $observed, true));

        self::assertSame(0o600, fileperms($file) & 0o777, 'The replacement must not widen the mode.');
        // No temp sibling survives the store.
        self::assertSame(
            [self::SHA . '.json'],
            array_values(array_diff((array) scandir($this->dir . '/m/1/11'), ['.', '..'])),
        );
    }

    public function testAFailedStoreIsReportedRatherThanLettingTheCallerClaimResultsWereCached(): void
    {
        $cache = new ResultsCache($this->dir);
        mkdir($this->dir . '/m/1/11', 0o700, true);
        chmod($this->dir . '/m/1/11', 0o500);

        try {
            $this->expectException(FilesystemException::class);
            $cache->store('m', 1, '11', self::SHA, new CheckRunResult([]));
        } finally {
            chmod($this->dir . '/m/1/11', 0o700);
        }
    }

    /**
     * The head SHA is an unvalidated remote value from the GitLab API and it
     * becomes a filename directly.
     */
    #[DataProvider('rejectedShas')]
    public function testAShaThatIsNotAShaIsRefusedBeforeItBecomesAFilename(string $sha): void
    {
        $cache = new ResultsCache($this->dir);

        $this->expectException(\InvalidArgumentException::class);
        $cache->store('m', 1, '11', $sha, new CheckRunResult([]));
    }

    /**
     * @return iterable<string, array{string}>
     */
    public static function rejectedShas(): iterable
    {
        yield 'traversal' => ['../../../../etc/passwd'];
        yield 'a path separator' => ['abcdef0/nested'];
        yield 'not hex' => ['not-a-sha'];
        yield 'too short' => ['abc123'];
        yield 'too long' => [str_repeat('a', 65)];
        yield 'empty' => [''];
    }

    public function testAReadWithAnInvalidShaIsAMissRatherThanACrash(): void
    {
        $cache = new ResultsCache($this->dir);

        self::assertNull($cache->find('m', 1, '11', '../../etc/passwd'));
    }

    public function testAModuleNameThatIsNotAMachineNameNeverBecomesAPathSegment(): void
    {
        $cache = new ResultsCache($this->dir);

        $this->expectException(\InvalidArgumentException::class);
        $cache->store('../../escape', 1, '11', self::SHA, new CheckRunResult([]));
    }

    public function testAnUnreadableResultsDirectoryIsReportedRatherThanReadAsNeverChecked(): void
    {
        $cache = new ResultsCache($this->dir);
        $cache->store('m', 1, '11', self::SHA, new CheckRunResult([]));
        chmod($this->dir . '/m/1/11', 0o000);

        try {
            $this->expectException(FilesystemException::class);
            $this->expectExceptionMessageMatches('#' . preg_quote($this->dir . '/m/1/11', '#') . '#');
            $cache->latest('m', 1, '11');
        } finally {
            chmod($this->dir . '/m/1/11', 0o700);
        }
    }

    public function testAResultFileWhoseShapeIsWrongDegradesToAMiss(): void
    {
        $cache = new ResultsCache($this->dir);
        mkdir($this->dir . '/m/1/11', 0o700, true);
        $file = $this->dir . '/m/1/11/' . self::SHA . '.json';

        // "results" is present but not a list of check mappings: the runtime
        // guard, not a docblock, is what has to catch this.
        file_put_contents($file, json_encode([
            'sha' => self::SHA,
            'recorded_at' => '2026-07-01T00:00:00+00:00',
            'results' => 'not-a-list',
        ]));
        self::assertNull($cache->find('m', 1, '11', self::SHA));

        file_put_contents($file, json_encode([
            'sha' => self::SHA,
            'recorded_at' => '2026-07-01T00:00:00+00:00',
            'results' => [['type' => 'nonsense', 'status' => 'passed']],
        ]));
        self::assertNull($cache->find('m', 1, '11', self::SHA));

        file_put_contents($file, json_encode([
            'sha' => 12345,
            'recorded_at' => '2026-07-01T00:00:00+00:00',
            'results' => [],
        ]));
        self::assertNull($cache->find('m', 1, '11', self::SHA));
    }
}

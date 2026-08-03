<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\TestCase;
use Upkeep\Dashboard\DashboardCache;
use Upkeep\Dashboard\ModuleSnapshot;
use Upkeep\Filesystem\FilesystemException;

final class DashboardCacheTest extends TestCase
{
    private string $cacheDir;

    protected function setUp(): void
    {
        $this->cacheDir = sys_get_temp_dir() . '/upkeep-cache-test-' . bin2hex(random_bytes(4));
    }

    protected function tearDown(): void
    {
        if (is_dir($this->cacheDir)) {
            exec('rm -rf ' . escapeshellarg($this->cacheDir));
        }
    }

    private function snapshot(\DateTimeImmutable $at = new \DateTimeImmutable()): ModuleSnapshot
    {
        return new ModuleSnapshot(
            $at,
            [
                'id' => 42,
                'path' => 'widget',
                'path_with_namespace' => 'project/widget',
                'name' => 'Widget',
                'web_url' => 'https://example.com',
            ],
            [
                [
                    'iid' => 1,
                    'title' => 'Fix',
                    'state' => 'opened',
                    'draft' => false,
                    'author' => ['username' => 'a', 'id' => 1],
                    'source_branch' => 'fix',
                    'target_branch' => '1.x',
                    'sha' => 'abc',
                    'web_url' => 'https://example.com/mr/1',
                ],
            ],
            [],
        );
    }

    public function testLoadReturnsNullWhenNoCacheExists(): void
    {
        $cache = new DashboardCache($this->cacheDir);

        self::assertNull($cache->load('widget'));
    }

    public function testSaveAndLoadRoundTrip(): void
    {
        $cache = new DashboardCache($this->cacheDir);
        $snapshot = $this->snapshot();

        $cache->save('widget', $snapshot);
        $loaded = $cache->load('widget');

        self::assertNotNull($loaded);
        self::assertSame($snapshot->fetchedAt->getTimestamp(), $loaded->fetchedAt->getTimestamp());
        self::assertSame(42, $loaded->project()->id);
        self::assertCount(1, $loaded->mergeRequests());
    }

    public function testSaveCreatesDirectoryIfMissing(): void
    {
        $cache = new DashboardCache($this->cacheDir . '/deep/nested');
        $cache->save('widget', $this->snapshot());

        self::assertNotNull($cache->load('widget'));
    }

    public function testModulesAreCachedIndependently(): void
    {
        $cache = new DashboardCache($this->cacheDir);
        $cache->save('alpha', $this->snapshot());
        $cache->save('beta', $this->snapshot());

        self::assertNotNull($cache->load('alpha'));
        self::assertNotNull($cache->load('beta'));
        self::assertNull($cache->load('gamma'));
    }

    public function testOldestFetchedAtFindsOldestAcrossModules(): void
    {
        $cache = new DashboardCache($this->cacheDir);

        $old = new \DateTimeImmutable('-3 hours');
        $recent = new \DateTimeImmutable('-10 minutes');

        $cache->save('alpha', $this->snapshot($old));
        $cache->save('beta', $this->snapshot($recent));

        $oldest = $cache->oldestFetchedAt(['alpha', 'beta']);
        self::assertNotNull($oldest);
        self::assertSame($old->getTimestamp(), $oldest->getTimestamp());
    }

    public function testOldestFetchedAtReturnsNullWhenNoCachesExist(): void
    {
        $cache = new DashboardCache($this->cacheDir);

        self::assertNull($cache->oldestFetchedAt(['alpha', 'beta']));
    }

    public function testSaveOverwritesExisting(): void
    {
        $cache = new DashboardCache($this->cacheDir);
        $cache->save('widget', $this->snapshot(new \DateTimeImmutable('-1 hour')));
        $cache->save('widget', $this->snapshot(new \DateTimeImmutable()));

        $loaded = $cache->load('widget');
        self::assertNotNull($loaded);
        self::assertSame('just now', $loaded->ageLabel(new \DateTimeImmutable()));
    }

    /**
     * The snapshot serialises complete upstream GitLab payloads fetched with
     * the maintainer's PAT. For a limited-visibility project that is
     * token-scoped data, and it must not land world-readable.
     */
    public function testCachedSnapshotsAreOwnerOnlyBecauseTheyCarryTokenScopedRemoteData(): void
    {
        $cache = new DashboardCache($this->cacheDir);
        $cache->save('widget', $this->snapshot());

        self::assertSame(0o600, fileperms($this->cacheDir . '/widget.json') & 0o777);
        self::assertSame(0o700, fileperms($this->cacheDir) & 0o777);
    }

    public function testASaveIsAtomicSoAReaderNeverSeesAPartialSnapshot(): void
    {
        $cache = new DashboardCache($this->cacheDir);
        $cache->save('widget', $this->snapshot(new \DateTimeImmutable('-1 hour')));

        $file = $this->cacheDir . '/widget.json';
        $handle = fopen($file, 'r');
        self::assertNotFalse($handle);

        $cache->save('widget', $this->snapshot());

        self::assertTrue(is_file($file));
        self::assertIsArray(json_decode((string) stream_get_contents($handle), true));
        fclose($handle);

        self::assertSame(
            ['widget.json'],
            array_values(array_diff((array) scandir($this->cacheDir), ['.', '..'])),
        );
    }

    public function testATornCacheFileDegradesToAMissRatherThanAnUncaughtException(): void
    {
        $cache = new DashboardCache($this->cacheDir);
        $cache->save('widget', $this->snapshot());
        // A torn write from before this cache became atomic, or a truncated
        // file from any other cause: the dashboard must re-fetch, not crash
        // and demand a manual rm.
        file_put_contents($this->cacheDir . '/widget.json', '{"fetched_at": "2026-0');

        self::assertNull($cache->load('widget'));
    }

    public function testAFailedSaveIsReportedRatherThanSilentlyDiscarded(): void
    {
        $cache = new DashboardCache($this->cacheDir);
        $cache->save('widget', $this->snapshot());
        chmod($this->cacheDir, 0o500);

        try {
            $this->expectException(FilesystemException::class);
            $cache->save('widget', $this->snapshot());
        } finally {
            chmod($this->cacheDir, 0o700);
        }
    }

    public function testAModuleNameThatIsNotAMachineNameNeverBecomesAFilename(): void
    {
        $cache = new DashboardCache($this->cacheDir);

        $this->expectException(\InvalidArgumentException::class);
        $cache->save('../../escape', $this->snapshot());
    }
}

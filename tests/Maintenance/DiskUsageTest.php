<?php

declare(strict_types=1);

namespace Upkeep\Tests\Maintenance;

use PHPUnit\Framework\TestCase;
use Upkeep\Maintenance\DiskUsage;

/**
 * `du -sk` is what an operator would run, so the inventory reports the same
 * number. What matters here is the degradation contract: a path that cannot be
 * measured contributes zero rather than aborting the whole scan.
 */
final class DiskUsageTest extends TestCase
{
    private string $world;

    protected function setUp(): void
    {
        $this->world = sys_get_temp_dir() . '/upkeep-diskusage-' . bin2hex(random_bytes(4));
        mkdir($this->world, 0o700, true);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->world));
    }

    public function testMeasuresRealBytesOnDiskInWholeKibibytes(): void
    {
        file_put_contents($this->world . '/payload.bin', str_repeat('x', 100_000));

        $bytes = DiskUsage::bytes($this->world);

        self::assertGreaterThanOrEqual(100_000, $bytes);
        self::assertSame(0, $bytes % 1024, 'du -sk reports whole kibibytes.');
    }

    public function testAPathThatCannotBeMeasuredContributesZeroRatherThanFailingTheScan(): void
    {
        // Two ways du yields nothing usable: the path is gone, or it is there
        // but cannot be walked. Neither may abort an inventory — under-reporting
        // fails safe for prune, a raised exception does not.
        $unreadable = $this->world . '/unreadable';
        mkdir($unreadable . '/inner', 0o700, true);
        file_put_contents($unreadable . '/inner/data', str_repeat('y', 20_000));
        chmod($unreadable, 0o000);

        try {
            self::assertSame(0, DiskUsage::bytes($this->world . '/does-not-exist'));
            self::assertSame(0, DiskUsage::bytes($unreadable));
        } finally {
            chmod($unreadable, 0o700);
        }
    }
}

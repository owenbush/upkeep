<?php

declare(strict_types=1);

namespace Upkeep\Tests\BaseArtifact;

use PHPUnit\Framework\TestCase;
use Upkeep\BaseArtifact\ArtifactLayout;

final class ArtifactLayoutTest extends TestCase
{
    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/upkeep-layout-' . bin2hex(random_bytes(4));
        mkdir($this->dir, 0755, true);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    public function testResolvesCanonicalPathsForAVersion(): void
    {
        $layout = new ArtifactLayout($this->dir);

        self::assertSame($this->dir . '/11', $layout->versionDir('11'));
        self::assertSame($this->dir . '/11/tree', $layout->treePath('11'));
        self::assertSame($this->dir . '/11/clean-install.sql.gz', $layout->dumpPath('11'));
        self::assertSame($this->dir . '/11/meta.yml', $layout->metaPath('11'));
        self::assertSame($this->dir . '/11/canonical', $layout->canonicalMarkerPath('11'));
    }

    public function testRejectsNonNumericVersion(): void
    {
        $layout = new ArtifactLayout($this->dir);

        $this->expectException(\InvalidArgumentException::class);
        $layout->versionDir('../evil');
    }

    public function testRejectsEmptyVersion(): void
    {
        $layout = new ArtifactLayout($this->dir);

        $this->expectException(\InvalidArgumentException::class);
        $layout->versionDir('');
    }

    public function testVersionsOnDiskListsOnlyNumericDirectoriesSorted(): void
    {
        mkdir($this->dir . '/11');
        mkdir($this->dir . '/10');
        mkdir($this->dir . '/9');
        mkdir($this->dir . '/not-a-version');
        touch($this->dir . '/12'); // a file, not a directory
        touch($this->dir . '/.gitkeep');

        $layout = new ArtifactLayout($this->dir);

        self::assertSame(['9', '10', '11'], $layout->versionsOnDisk());
    }

    public function testVersionsOnDiskIsEmptyWhenBaseDirMissing(): void
    {
        $layout = new ArtifactLayout($this->dir . '/nope');

        self::assertSame([], $layout->versionsOnDisk());
    }
}

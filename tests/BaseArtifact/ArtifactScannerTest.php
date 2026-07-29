<?php

declare(strict_types=1);

namespace Upkeep\Tests\BaseArtifact;

use PHPUnit\Framework\TestCase;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\BaseArtifact\ArtifactScanner;

final class ArtifactScannerTest extends TestCase
{
    private string $dir;
    private ArtifactLayout $layout;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/upkeep-scan-' . bin2hex(random_bytes(4));
        mkdir($this->dir, 0755, true);
        $this->layout = new ArtifactLayout($this->dir);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    private function makeCompleteVersion(string $major, string $metaYaml): void
    {
        mkdir($this->layout->treePath($major) . '/vendor', 0755, true);
        file_put_contents($this->layout->treePath($major) . '/composer.json', '{}');
        file_put_contents($this->layout->treePath($major) . '/vendor/autoload.php', str_repeat('x', 100));
        file_put_contents($this->layout->dumpPath($major), str_repeat('z', 2048));
        file_put_contents($this->layout->metaPath($major), $metaYaml);
        file_put_contents($this->layout->canonicalMarkerPath($major), "canonical\n");
    }

    private const string META_11 = <<<'YAML'
        core_version: 11.4.4
        core_major: '11'
        php_version: 8.3.30
        db_engine: 'mariadb:10.11'
        built_at: '2026-07-29T14:02:11+00:00'

        YAML;

    public function testCompleteArtifactSetIsReportedCompleteWithMetaAndSizes(): void
    {
        $this->makeCompleteVersion('11', self::META_11);

        $records = new ArtifactScanner($this->layout)->scan();

        self::assertCount(1, $records);
        $record = $records[0];
        self::assertSame('11', $record->version);
        self::assertTrue($record->complete);
        self::assertSame([], $record->missing);
        self::assertNotNull($record->meta);
        self::assertSame('11.4.4', $record->meta->coreVersion);
        self::assertSame(102, $record->treeSizeBytes); // 2 + 100 bytes
        self::assertSame(2048, $record->dumpSizeBytes);
    }

    public function testMissingPiecesAreReported(): void
    {
        mkdir($this->layout->treePath('10'), 0755, true);
        // no dump, no meta, no canonical marker

        $records = new ArtifactScanner($this->layout)->scan();

        self::assertCount(1, $records);
        $record = $records[0];
        self::assertFalse($record->complete);
        self::assertNull($record->meta);
        self::assertSame(
            [ArtifactLayout::DUMP_FILENAME, ArtifactLayout::META_FILENAME, ArtifactLayout::CANONICAL_MARKER],
            $record->missing,
        );
    }

    public function testMalformedMetaMarksSetIncomplete(): void
    {
        $this->makeCompleteVersion('11', '{{{ not yaml');

        $record = new ArtifactScanner($this->layout)->scan()[0];

        self::assertFalse($record->complete);
        self::assertNull($record->meta);
        self::assertSame([ArtifactLayout::META_FILENAME . ' (unreadable)'], $record->missing);
    }

    public function testVersionsAreScannedInNumericOrder(): void
    {
        $this->makeCompleteVersion('11', self::META_11);
        mkdir($this->layout->treePath('10'), 0755, true);

        $records = new ArtifactScanner($this->layout)->scan();

        self::assertSame(['10', '11'], array_map(static fn ($r) => $r->version, $records));
    }

    public function testEmptyBaseDirYieldsNoRecords(): void
    {
        self::assertSame([], new ArtifactScanner($this->layout)->scan());
    }
}

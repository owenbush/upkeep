<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\SnapshotLayout;

/**
 * The add-on's snapshot layout, mirrored here as the one place outside the
 * add-on that may know it. The prune surface deletes the sidecar meta file
 * alongside the artifact it belongs to, so the mapping has to refuse anything
 * that is not a materialized snapshot rather than compute a plausible-looking
 * path next to an unrelated file.
 */
final class SnapshotLayoutTest extends TestCase
{
    public function testAMaterializedSnapshotMapsToItsSidecarMetaFile(): void
    {
        self::assertSame(
            '/p/upkeep-widget-d11/.ddev/upkeep/snapshots/baseline.meta',
            SnapshotLayout::metaPathForArtifact('/p/upkeep-widget-d11/.ddev/upkeep/materialized/baseline.sql'),
        );
    }

    public function testAPathThatIsNotAMaterializedSnapshotHasNoMetaFile(): void
    {
        $notSnapshots = [
            '/p/upkeep-widget-d11/.ddev/upkeep/snapshots/baseline.meta',
            '/p/upkeep-widget-d11/web/index.php',
            'baseline.sql',
        ];

        foreach ($notSnapshots as $path) {
            self::assertNull(SnapshotLayout::metaPathForArtifact($path), $path);
        }
    }
}

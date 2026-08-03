<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\VolumeProbe;
use Upkeep\Maintenance\Category;
use Upkeep\Maintenance\InventoryItem;

/**
 * The "collect project trees, then probe volumes" loop was written out
 * verbatim in both the status and prune surfaces. It lives here now.
 */
final class VolumeProbeInventoryTest extends TestCase
{
    public function testOnlyProjectTreesWithAnEngineProjectNameAreProbed(): void
    {
        $probe = self::probe([
            'upkeep-widget-d11-data' => 'ddev-upkeep-widget-d11',
            'unrelated-volume' => 'ddev-someone-elses-project',
        ]);

        $items = $probe->itemsForInventory([
            self::item('/p/upkeep-widget-d11', Category::ProjectTree, 'upkeep-widget-d11'),
            self::item('/p/snapshot.sql', Category::Snapshot, null),
            self::item('/c/base-artifacts/11', Category::BaseArtifact, null),
        ]);

        self::assertCount(1, $items);
        self::assertSame('upkeep-widget-d11-data', $items[0]->path);
        self::assertSame(Category::ProjectVolume, $items[0]->category);
    }

    public function testAnInventoryWithNoProjectTreesProbesNothing(): void
    {
        $probe = self::probe(['upkeep-widget-d11-data' => 'ddev-upkeep-widget-d11']);

        self::assertSame([], $probe->itemsForInventory([
            self::item('/c/base-artifacts/11', Category::BaseArtifact, null),
        ]));
    }

    /** @param array<string, string> $volumes volume name => compose project label */
    private static function probe(array $volumes): VolumeProbe
    {
        $listing = implode("\n", array_map(
            static fn (string $name, string $label): string => $name . "\t" . $label,
            array_keys($volumes),
            array_values($volumes),
        ));

        return new VolumeProbe(static fn (array $command): string => $command[1] === 'volume' ? $listing : '');
    }

    private static function item(string $path, Category $category, ?string $projectName): InventoryItem
    {
        return new InventoryItem(
            path: $path,
            category: $category,
            sizeBytes: 1024,
            module: 'widget',
            coreMajor: '11',
            projectName: $projectName,
        );
    }
}

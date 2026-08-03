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

    /**
     * Built over a CommandRunner, the probe must stay tolerant: docker being
     * unavailable is a normal state (the probe is best-effort), so a failing
     * `docker volume ls` yields no volumes rather than an exception. It must
     * also stay quick — a stuck docker daemon cannot be allowed to hold up a
     * status listing for the runner's hour-long default.
     */
    public function testOverARunnerAFailingDockerProbeYieldsNothingAndStaysTimeboxed(): void
    {
        $runner = new ScriptedCommandRunner(static fn (): ?string => null);

        self::assertSame([], VolumeProbe::withRunner($runner)->items([
            'upkeep-widget-d11' => self::item('/p/upkeep-widget-d11', Category::ProjectTree, 'upkeep-widget-d11'),
        ]));
        self::assertSame(
            ['docker volume ls --format {{.Name}}\t{{.Label "com.docker.compose.project"}}'],
            $runner->commandLines()
        );
        self::assertSame(120, $runner->invocations[0]['timeout']);
    }

    public function testOverARunnerVolumeSizesAreReadFromDockerSystemDf(): void
    {
        $runner = new ScriptedCommandRunner(static function (array $command): string {
            if (($command[1] ?? '') === 'volume') {
                return "upkeep-widget-d11-mariadb\tddev-upkeep-widget-d11\n";
            }

            return implode("\n", [
                'Local Volumes space usage:',
                '',
                'VOLUME NAME                 LINKS     SIZE',
                'upkeep-widget-d11-mariadb   1         2.5GB',
                'unrelated-volume            0         400MB',
            ]);
        });

        $items = VolumeProbe::withRunner($runner)->items([
            'upkeep-widget-d11' => self::item('/p/upkeep-widget-d11', Category::ProjectTree, 'upkeep-widget-d11'),
        ]);

        self::assertCount(1, $items);
        self::assertSame(2500000000, $items[0]->sizeBytes);
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

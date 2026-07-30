<?php

declare(strict_types=1);

namespace Upkeep\Tests\Maintenance;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\VolumeProbe;

final class VolumeProbeTest extends TestCase
{
    public function testParsesVolumeListMappingComposeProjectLabelsToEngineProjects(): void
    {
        // Verified live: the engine's volumes carry
        // com.docker.compose.project=ddev-<projectname> (NOT com.ddev.site-name).
        $output = implode("\n", [
            "upkeep-conditions-helper-d10-mariadb\tddev-upkeep-conditions-helper-d10",
            "ddev-upkeep-conditions-helper-d10-snapshots\tddev-upkeep-conditions-helper-d10",
            "ddev-global-cache\t",
            "unrelated-compose-volume\tsomeapp",
            '',
        ]);

        self::assertSame(
            [
                'upkeep-conditions-helper-d10-mariadb' => 'upkeep-conditions-helper-d10',
                'ddev-upkeep-conditions-helper-d10-snapshots' => 'upkeep-conditions-helper-d10',
            ],
            VolumeProbe::parseVolumeList($output),
        );
    }

    public function testParsesVolumeSizesFromSystemDfVerboseOutput(): void
    {
        $output = <<<'OUT'
            Images space usage:

            REPOSITORY   TAG       IMAGE ID       CREATED       SIZE      SHARED SIZE   UNIQUE SIZE   CONTAINERS
            ddev/ddev-webserver v1  abc123   2 weeks ago   1.204GB   0B            1.204GB       1

            Local Volumes space usage:

            VOLUME NAME                            LINKS     SIZE
            upkeep-conditions-helper-d10-mariadb   1         132.5MB
            ddev-global-cache                      2         56.05kB
            empty-volume                           0         0B

            Build cache usage: 0B
            OUT;

        $sizes = VolumeProbe::parseVolumeSizes($output);

        self::assertSame(132_500_000, $sizes['upkeep-conditions-helper-d10-mariadb']);
        self::assertSame(56_050, $sizes['ddev-global-cache']);
        self::assertSame(0, $sizes['empty-volume']);
        self::assertArrayNotHasKey('ddev/ddev-webserver', $sizes);
    }

    public function testUnavailableDockerYieldsNoItems(): void
    {
        $probe = new VolumeProbe(static fn (array $command): ?string => null);

        self::assertSame([], $probe->items([]));
    }
}

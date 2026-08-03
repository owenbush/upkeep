<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Adapter\VolumeProbe;
use Upkeep\Command\StatusCommand;

final class StatusCommandTest extends TestCase
{
    private string $world;
    private string $cockpit;
    private string $projects;

    private string|false $originalHome;

    protected function setUp(): void
    {
        $this->world = (string) realpath(sys_get_temp_dir()) . '/upkeep-status-cmd-test-' . bin2hex(random_bytes(4));
        // The projects root must resolve under $HOME (Docker providers only
        // mount the home directory). Point $HOME at the temp world so the
        // fixture stays in sys_get_temp_dir() and the real home is untouched.
        $this->originalHome = getenv('HOME');
        putenv('HOME=' . $this->world);
        $this->cockpit = $this->world . '/cockpit';
        $this->projects = $this->world . '/projects';

        mkdir($this->cockpit . '/base-artifacts/11/tree', 0755, true);
        file_put_contents($this->cockpit . '/base-artifacts/11/tree/index.php', str_repeat('x', 8192));
        file_put_contents($this->cockpit . '/base-artifacts/11/canonical', '');
        file_put_contents(
            $this->cockpit . '/registry.yml',
            "modules:\n  conditions_helper:\n    project: project/conditions_helper\n    core_versions: [\"11\"]\n",
        );

        $p = $this->projects . '/upkeep-conditions-helper-d11';
        mkdir($p . '/.ddev/upkeep/materialized', 0755, true);
        file_put_contents(
            $p . '/.upkeep-env.yml',
            "module: conditions_helper\ncore_major: '11'\nseed_core_version: 11.2.5\n"
            . "addon_version: v1.0.0\ncreated_at: '2026-06-01T00:00:00+00:00'\n",
        );
        file_put_contents($p . '/.ddev/upkeep/materialized/base.sql', str_repeat('s', 4096));
    }

    protected function tearDown(): void
    {
        putenv($this->originalHome === false ? 'HOME' : 'HOME=' . $this->originalHome);
        exec('rm -rf ' . escapeshellarg($this->world));
    }

    private function runStatus(array $args): CommandTester
    {
        $tester = new CommandTester(new StatusCommand(new VolumeProbe(static fn (array $c): ?string => null)));
        $tester->execute(['--cockpit' => $this->cockpit, '--projects-root' => $this->projects, ...$args]);

        return $tester;
    }

    public function testDiskTableAttributesRealMeasuredSizesWithTotals(): void
    {
        $tester = $this->runStatus(['--disk' => true]);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();

        self::assertStringContainsString('conditions_helper', $display);
        self::assertStringContainsString('base artifact', $display);
        self::assertStringContainsString('project tree', $display);
        self::assertStringContainsString('materialized snapshot', $display);
        self::assertStringContainsString('total:', $display);

        // Spot-check: the base-artifact row size equals du -sk of the same path.
        $duBytes = (int) exec('du -sk ' . escapeshellarg($this->cockpit . '/base-artifacts/11')) * 1024;
        self::assertStringContainsString(\Upkeep\Maintenance\ByteFormat::human($duBytes), $display);
    }

    public function testWithoutDiskFlagPrintsSummaryOnly(): void
    {
        $tester = $this->runStatus([]);

        $tester->assertCommandIsSuccessful();
        self::assertStringContainsString('Total tracked disk usage', $tester->getDisplay());
        self::assertStringNotContainsString('Category', $tester->getDisplay());
    }
}

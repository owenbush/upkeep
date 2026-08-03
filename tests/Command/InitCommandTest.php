<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\ModuleRegistry;
use Upkeep\Command\InitCommand;
use Upkeep\Workflow\ExitCode;

final class InitCommandTest extends TestCase
{
    private string $world;

    protected function setUp(): void
    {
        $this->world = (string) realpath(sys_get_temp_dir()) . '/upkeep-init-' . bin2hex(random_bytes(4));
        mkdir($this->world, 0o700, true);
    }

    protected function tearDown(): void
    {
        @chmod($this->world, 0o700);
        exec('rm -rf ' . escapeshellarg($this->world));
    }

    private function runInit(string $dir): CommandTester
    {
        $tester = new CommandTester(new InitCommand());
        $tester->execute(['dir' => $dir]);

        return $tester;
    }

    public function testScaffoldsACockpitWithAParseableRegistry(): void
    {
        $cockpit = $this->world . '/cockpit';
        $tester = $this->runInit($cockpit);

        self::assertSame(ExitCode::OK, $tester->getStatusCode());
        self::assertStringContainsString('Cockpit created', $tester->getDisplay());
        self::assertFileExists($cockpit . '/' . Cockpit::REGISTRY_FILENAME);
        self::assertSame([], ModuleRegistry::fromFile($cockpit . '/' . Cockpit::REGISTRY_FILENAME)->modules());
        self::assertDirectoryExists($cockpit . '/' . Cockpit::BASE_ARTIFACTS_DIR);
        self::assertDirectoryExists($cockpit . '/' . Cockpit::FIXTURES_DIR);
        self::assertDirectoryExists($cockpit . '/' . Cockpit::PROJECTS_DIR);
    }

    public function testRefusesToOverwriteAnExistingCockpit(): void
    {
        $cockpit = $this->world . '/cockpit';
        $this->runInit($cockpit);

        $tester = $this->runInit($cockpit);

        self::assertSame(ExitCode::INFRASTRUCTURE, $tester->getStatusCode());
        self::assertStringContainsString('already exists', $tester->getDisplay());
    }

    /**
     * A directory named registry.yml is not a cockpit. The "already exists"
     * guard must not claim it is; the write that follows is what reports the
     * real problem.
     */
    public function testADirectoryNamedRegistryYmlIsNotReportedAsAnExistingCockpit(): void
    {
        $cockpit = $this->world . '/cockpit';
        mkdir($cockpit . '/' . Cockpit::REGISTRY_FILENAME, 0o700, true);

        $tester = $this->runInit($cockpit);

        self::assertSame(ExitCode::INFRASTRUCTURE, $tester->getStatusCode());
        self::assertStringNotContainsString('already exists', $tester->getDisplay());
        self::assertStringNotContainsString('Cockpit created', $tester->getDisplay());
        self::assertStringContainsString(Cockpit::REGISTRY_FILENAME, $tester->getDisplay());
    }

    /**
     * The whole point of SEC-FS-07's highest-consequence case: a registry that
     * was never written must never be reported as a created cockpit, because
     * the next command then says "Module registry not found".
     */
    public function testAFailedRegistryWriteIsReportedInsteadOfClaimingTheCockpitWasCreated(): void
    {
        $cockpit = $this->world . '/cockpit';
        mkdir($cockpit, 0o700, true);
        chmod($cockpit, 0o500);

        try {
            $tester = $this->runInit($cockpit);

            self::assertSame(ExitCode::INFRASTRUCTURE, $tester->getStatusCode());
            self::assertStringNotContainsString('Cockpit created', $tester->getDisplay());
            self::assertFileDoesNotExist($cockpit . '/' . Cockpit::REGISTRY_FILENAME);
        } finally {
            chmod($cockpit, 0o700);
        }
    }
}

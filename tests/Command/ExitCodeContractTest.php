<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\VolumeProbe;
use Upkeep\Command\DevCommand;
use Upkeep\Command\EnvPathCommand;
use Upkeep\Command\ExecCommand;
use Upkeep\Command\InitCommand;
use Upkeep\Command\ModulesCommand;
use Upkeep\Command\PruneCommand;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Tests\Support\StubEngineAdapterFactory;
use Upkeep\Workflow\ExitCode;

/**
 * The CLI-wide exit-code contract, driven through the console.
 *
 * Every command answers with one of exactly three codes: 0 the command did
 * what was asked, 1 the work it supervised reported failure, 2 upkeep could
 * not do the job. These assertions are the contract itself — they are what
 * a script wrapping upkeep is entitled to rely on.
 */
final class ExitCodeContractTest extends TestCase
{
    private string $cockpit;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-exitcode-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit . '/base-artifacts', 0o755, true);
        file_put_contents(
            $this->cockpit . '/registry.yml',
            "modules:\n  token:\n    project: project/token\n    core_versions: ['11']\n",
        );
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->cockpit));
    }

    public function testSuccessfulWorkExitsOk(): void
    {
        $tester = new CommandTester(new ExecCommand($this->adapterFactory(sys_get_temp_dir())));
        $exit = $tester->execute([
            'module' => 'token',
            'cmd' => ['true'],
            '--cockpit' => $this->cockpit,
        ]);

        self::assertSame(ExitCode::OK, $exit);
    }

    /**
     * The behaviour change D2 mandates: `upkeep exec` no longer passes the
     * child's raw code through, so a child exiting 2 can never be mistaken
     * for an upkeep infrastructure failure.
     */
    public function testWrappedCommandFailureCollapsesToTheFailedCode(): void
    {
        foreach ([1, 2, 42, 127] as $childCode) {
            $tester = new CommandTester(new ExecCommand($this->adapterFactory(sys_get_temp_dir())));
            $exit = $tester->execute([
                'module' => 'token',
                'cmd' => ['bash', '-c', 'exit ' . $childCode],
                '--cockpit' => $this->cockpit,
            ]);

            self::assertSame(ExitCode::FAILED, $exit, sprintf('child exit %d', $childCode));
        }
    }

    public function testUnregisteredModuleIsAnInfrastructureFailure(): void
    {
        $tester = new CommandTester(new ExecCommand($this->adapterFactory(sys_get_temp_dir())));
        $exit = $tester->execute([
            'module' => 'nope',
            'cmd' => ['true'],
            '--cockpit' => $this->cockpit,
        ]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('not registered', $tester->getDisplay());
    }

    public function testUntrackedCoreVersionIsAnInfrastructureFailure(): void
    {
        $tester = new CommandTester(new DevCommand($this->adapterFactory(null)));
        $exit = $tester->execute([
            'module' => 'token',
            '--version' => '9',
            '--cockpit' => $this->cockpit,
        ]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
    }

    public function testEngineFailureIsAnInfrastructureFailure(): void
    {
        $adapter = FakeEngineAdapter::failing(new AdapterException('base artifacts missing'));
        $tester = new CommandTester(new DevCommand(new StubEngineAdapterFactory($adapter)));
        $exit = $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('base artifacts missing', $tester->getDisplay());
    }

    public function testMissingEnvironmentIsAnInfrastructureFailure(): void
    {
        $tester = new CommandTester(new EnvPathCommand($this->adapterFactory(null)));
        $exit = $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
    }

    public function testMissingRegistryIsAnInfrastructureFailure(): void
    {
        $empty = $this->cockpit . '/empty';
        mkdir($empty, 0o755, true);

        $tester = new CommandTester(new ModulesCommand());
        $exit = $tester->execute(['--cockpit' => $empty]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('Module registry not found', $tester->getDisplay());
    }

    public function testMalformedRegistryIsAnInfrastructureFailure(): void
    {
        $broken = $this->cockpit . '/broken';
        mkdir($broken, 0o755, true);
        file_put_contents($broken . '/registry.yml', "modules:\n  token: [\n");

        $tester = new CommandTester(new ModulesCommand());
        $exit = $tester->execute(['--cockpit' => $broken]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
    }

    public function testBadUsageIsAnInfrastructureFailure(): void
    {
        $tester = new CommandTester(new PruneCommand(
            $this->adapterFactory(null),
            new VolumeProbe(static fn (array $command): ?string => null),
        ));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('exactly one prune scope', $tester->getDisplay());
    }

    public function testDryRunWithNothingToPruneExitsOk(): void
    {
        $tester = new CommandTester(new PruneCommand(
            $this->adapterFactory(null),
            new VolumeProbe(static fn (array $command): ?string => null),
        ));
        $exit = $tester->execute(['--all' => true, '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::OK, $exit);
    }

    public function testRefusingToOverwriteAnExistingCockpitIsAnInfrastructureFailure(): void
    {
        $tester = new CommandTester(new InitCommand());
        $exit = $tester->execute(['dir' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
    }

    private function adapterFactory(?string $envPath): StubEngineAdapterFactory
    {
        return new StubEngineAdapterFactory(FakeEngineAdapter::withEnvPath($envPath));
    }
}

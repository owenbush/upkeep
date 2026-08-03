<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\ServeResult;
use Upkeep\Adapter\WorkingCopyStatus;
use Upkeep\Cockpit\Module;
use Upkeep\Command\EnvPathCommand;
use Upkeep\Gitlab\MergeRequest;

final class EnvPathCommandTest extends TestCase
{
    private string $cockpit;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-envpath-test-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit . '/base-artifacts', 0755, true);
        mkdir($this->cockpit . '/fixtures', 0755, true);
        file_put_contents($this->cockpit . '/registry.yml', <<<YAML
        modules:
          token:
            project: project/token
            core_versions: ["10", "11"]
        YAML);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->cockpit));
    }

    private function adapter(?string $returnPath): EngineAdapterInterface
    {
        return new class($returnPath) implements EngineAdapterInterface {
            public function __construct(private readonly ?string $returnPath) {}
            public function ensureEnv(Module $module, string $coreMajor): Environment { throw new \BadMethodCallException(); }
            public function applyMr(Environment $environment, MergeRequest $mergeRequest): void {}
            public function loadFixture(Environment $environment, string $fixtureName): void {}
            public function runChecks(Environment $environment, array $checks = []): CheckRunResult { throw new \BadMethodCallException(); }
            public function serve(Environment $environment): ServeResult { throw new \BadMethodCallException(); }
            public function resolveEnvPath(string $moduleName, string $coreMajor): ?string { return $this->returnPath; }
            public function teardown(Module $module, string $coreMajor): void {}
            public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus { return null; }
            public function checkoutBranch(Environment $environment, string $branch): void {}
        };
    }

    public function testPrintsPathWhenEnvironmentExists(): void
    {
        $tester = new CommandTester(new EnvPathCommand($this->adapter('/home/user/.upkeep/projects/upkeep-token-d11')));
        $exit = $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit);
        self::assertSame("/home/user/.upkeep/projects/upkeep-token-d11\n", $tester->getDisplay());
    }

    public function testDefaultsToFirstTrackedCoreVersion(): void
    {
        $calls = [];
        $adapter = new class($calls) implements EngineAdapterInterface {
            /** @param list<array{string, string}> $calls */
            public function __construct(private array &$calls) {}
            public function ensureEnv(Module $module, string $coreMajor): Environment { throw new \BadMethodCallException(); }
            public function applyMr(Environment $environment, MergeRequest $mergeRequest): void {}
            public function loadFixture(Environment $environment, string $fixtureName): void {}
            public function runChecks(Environment $environment, array $checks = []): CheckRunResult { throw new \BadMethodCallException(); }
            public function serve(Environment $environment): ServeResult { throw new \BadMethodCallException(); }
            public function resolveEnvPath(string $moduleName, string $coreMajor): ?string
            {
                $this->calls[] = [$moduleName, $coreMajor];
                return '/some/path';
            }
            public function teardown(Module $module, string $coreMajor): void {}
            public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus { return null; }
            public function checkoutBranch(Environment $environment, string $branch): void {}
        };

        $tester = new CommandTester(new EnvPathCommand($adapter));
        $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame([['token', '10']], $calls, 'should default to the first core version listed (10)');
    }

    public function testRespectsExplicitVersionOption(): void
    {
        $calls = [];
        $adapter = new class($calls) implements EngineAdapterInterface {
            public function __construct(private array &$calls) {}
            public function ensureEnv(Module $module, string $coreMajor): Environment { throw new \BadMethodCallException(); }
            public function applyMr(Environment $environment, MergeRequest $mergeRequest): void {}
            public function loadFixture(Environment $environment, string $fixtureName): void {}
            public function runChecks(Environment $environment, array $checks = []): CheckRunResult { throw new \BadMethodCallException(); }
            public function serve(Environment $environment): ServeResult { throw new \BadMethodCallException(); }
            public function resolveEnvPath(string $moduleName, string $coreMajor): ?string
            {
                $this->calls[] = [$moduleName, $coreMajor];
                return '/some/path';
            }
            public function teardown(Module $module, string $coreMajor): void {}
            public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus { return null; }
            public function checkoutBranch(Environment $environment, string $branch): void {}
        };

        $tester = new CommandTester(new EnvPathCommand($adapter));
        $tester->execute(['module' => 'token', '--version' => '11', '--cockpit' => $this->cockpit]);

        self::assertSame([['token', '11']], $calls);
    }

    public function testFailsForUnregisteredModule(): void
    {
        $tester = new CommandTester(new EnvPathCommand($this->adapter('/x')));
        $exit = $tester->execute(['module' => 'nope', '--cockpit' => $this->cockpit]);

        self::assertSame(1, $exit);
        self::assertStringContainsString('not registered', $tester->getDisplay());
    }

    public function testFailsForUntrackedCoreVersion(): void
    {
        $tester = new CommandTester(new EnvPathCommand($this->adapter('/x')));
        $exit = $tester->execute(['module' => 'token', '--version' => '9', '--cockpit' => $this->cockpit]);

        self::assertSame(1, $exit);
        self::assertStringContainsString('not tracked', $tester->getDisplay());
    }

    public function testFailsWhenEnvironmentDoesNotExist(): void
    {
        $tester = new CommandTester(new EnvPathCommand($this->adapter(null)));
        $exit = $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame(1, $exit);
        self::assertStringContainsString('No provisioned environment', $tester->getDisplay());
    }
}

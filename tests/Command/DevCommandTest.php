<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\ServeResult;
use Upkeep\Adapter\WorkingCopyStatus;
use Upkeep\Cockpit\Module;
use Upkeep\Command\DevCommand;
use Upkeep\Gitlab\MergeRequest;

final class DevCommandTest extends TestCase
{
    private string $cockpit;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-dev-cmd-test-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit, 0o755, true);
        file_put_contents(
            $this->cockpit . '/registry.yml',
            "modules:\n  token:\n    project: project/token\n    core_versions: ['10', '11']\n",
        );
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->cockpit));
    }

    public function testProvisionAndPrintPath(): void
    {
        $env = new Environment(
            'token',
            '11',
            'upkeep-token-d11',
            '/home/.upkeep/projects/upkeep-token-d11',
            'https://upkeep-token-d11.ddev.site',
            false,
        );
        $adapter = $this->adapter($env);

        $tester = new CommandTester(new DevCommand($adapter));
        $exit = $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit);
        $display = $tester->getDisplay();
        self::assertStringContainsString('upkeep-token-d11', $display);
        self::assertStringContainsString('/home/.upkeep/projects/upkeep-token-d11/module', $display);
        self::assertStringContainsString('https://upkeep-token-d11.ddev.site', $display);
    }

    public function testUnknownModuleFails(): void
    {
        $adapter = $this->adapter(null);
        $tester = new CommandTester(new DevCommand($adapter));
        $exit = $tester->execute(['module' => 'nonexistent', '--cockpit' => $this->cockpit]);

        self::assertSame(1, $exit);
        self::assertStringContainsString('not registered', $tester->getDisplay());
    }

    public function testBadCoreVersionFails(): void
    {
        $adapter = $this->adapter(null);
        $tester = new CommandTester(new DevCommand($adapter));
        $exit = $tester->execute(['module' => 'token', '--version' => '9', '--cockpit' => $this->cockpit]);

        self::assertSame(1, $exit);
        self::assertStringContainsString('not tracked', $tester->getDisplay());
    }

    public function testBranchSwitchCallsAdapter(): void
    {
        $env = new Environment(
            'token',
            '11',
            'upkeep-token-d11',
            '/home/.upkeep/projects/upkeep-token-d11',
            'https://upkeep-token-d11.ddev.site',
            false,
        );
        $branchCalls = [];
        $adapter = $this->adapterWithBranchTracking($env, $branchCalls);

        $tester = new CommandTester(new DevCommand($adapter));
        $exit = $tester->execute(['module' => 'token', '--branch' => 'feature/x', '--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit);
        self::assertSame(['feature/x'], $branchCalls);
    }

    public function testEnsureEnvFailureShowsError(): void
    {
        $adapter = $this->failingAdapter('Base artifacts missing');
        $tester = new CommandTester(new DevCommand($adapter));
        $exit = $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame(1, $exit);
        self::assertStringContainsString('Base artifacts missing', $tester->getDisplay());
    }

    public function testBranchSwitchFailureShowsError(): void
    {
        $env = new Environment(
            'token',
            '11',
            'upkeep-token-d11',
            '/home/.upkeep/projects/upkeep-token-d11',
            'https://upkeep-token-d11.ddev.site',
            false,
        );
        $adapter = $this->adapterWithBranchFailure($env, 'uncommitted changes');

        $tester = new CommandTester(new DevCommand($adapter));
        $exit = $tester->execute(['module' => 'token', '--branch' => 'feature/x', '--cockpit' => $this->cockpit]);

        self::assertSame(1, $exit);
        self::assertStringContainsString('uncommitted changes', $tester->getDisplay());
    }

    private function adapter(?Environment $env): EngineAdapterInterface
    {
        return new class ($env) implements EngineAdapterInterface {
            public function __construct(private readonly ?Environment $env)
            {
            }
            public function ensureEnv(Module $module, string $coreMajor): Environment
            {
                return $this->env ?? throw new \BadMethodCallException();
            }
            public function applyMr(Environment $environment, MergeRequest $mergeRequest): void
            {
            }
            public function loadFixture(Environment $environment, string $fixtureName): void
            {
            }
            public function runChecks(Environment $environment, array $checks = []): CheckRunResult
            {
                throw new \BadMethodCallException();
            }
            public function serve(Environment $environment): ServeResult
            {
                throw new \BadMethodCallException();
            }
            public function resolveEnvPath(string $moduleName, string $coreMajor): ?string
            {
                return null;
            }
            public function teardown(Module $module, string $coreMajor): void
            {
            }
            public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus
            {
                return null;
            }
            public function checkoutBranch(Environment $environment, string $branch): void
            {
            }
        };
    }

    /**
     * @param list<string> $branchCalls
     */
    private function adapterWithBranchTracking(Environment $env, array &$branchCalls): EngineAdapterInterface
    {
        return new class ($env, $branchCalls) implements EngineAdapterInterface {
            public function __construct(private readonly Environment $env, private array &$calls)
            {
            }
            public function ensureEnv(Module $module, string $coreMajor): Environment
            {
                return $this->env;
            }
            public function applyMr(Environment $environment, MergeRequest $mergeRequest): void
            {
            }
            public function loadFixture(Environment $environment, string $fixtureName): void
            {
            }
            public function runChecks(Environment $environment, array $checks = []): CheckRunResult
            {
                throw new \BadMethodCallException();
            }
            public function serve(Environment $environment): ServeResult
            {
                throw new \BadMethodCallException();
            }
            public function resolveEnvPath(string $moduleName, string $coreMajor): ?string
            {
                return null;
            }
            public function teardown(Module $module, string $coreMajor): void
            {
            }
            public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus
            {
                return null;
            }
            public function checkoutBranch(Environment $environment, string $branch): void
            {
                $this->calls[] = $branch;
            }
        };
    }

    private function failingAdapter(string $message): EngineAdapterInterface
    {
        return new class ($message) implements EngineAdapterInterface {
            public function __construct(private readonly string $msg)
            {
            }
            public function ensureEnv(Module $module, string $coreMajor): Environment
            {
                throw new AdapterException($this->msg);
            }
            public function applyMr(Environment $environment, MergeRequest $mergeRequest): void
            {
            }
            public function loadFixture(Environment $environment, string $fixtureName): void
            {
            }
            public function runChecks(Environment $environment, array $checks = []): CheckRunResult
            {
                throw new \BadMethodCallException();
            }
            public function serve(Environment $environment): ServeResult
            {
                throw new \BadMethodCallException();
            }
            public function resolveEnvPath(string $moduleName, string $coreMajor): ?string
            {
                return null;
            }
            public function teardown(Module $module, string $coreMajor): void
            {
            }
            public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus
            {
                return null;
            }
            public function checkoutBranch(Environment $environment, string $branch): void
            {
            }
        };
    }

    private function adapterWithBranchFailure(Environment $env, string $message): EngineAdapterInterface
    {
        return new class ($env, $message) implements EngineAdapterInterface {
            public function __construct(private readonly Environment $env, private readonly string $msg)
            {
            }
            public function ensureEnv(Module $module, string $coreMajor): Environment
            {
                return $this->env;
            }
            public function applyMr(Environment $environment, MergeRequest $mergeRequest): void
            {
            }
            public function loadFixture(Environment $environment, string $fixtureName): void
            {
            }
            public function runChecks(Environment $environment, array $checks = []): CheckRunResult
            {
                throw new \BadMethodCallException();
            }
            public function serve(Environment $environment): ServeResult
            {
                throw new \BadMethodCallException();
            }
            public function resolveEnvPath(string $moduleName, string $coreMajor): ?string
            {
                return null;
            }
            public function teardown(Module $module, string $coreMajor): void
            {
            }
            public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus
            {
                return null;
            }
            public function checkoutBranch(Environment $environment, string $branch): void
            {
                throw new AdapterException($this->msg);
            }
        };
    }
}

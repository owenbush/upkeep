<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\BaseRefresh;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\GitRemote;
use Upkeep\Adapter\IssueBranch;
use Upkeep\Adapter\PatchApplication;
use Upkeep\Adapter\PatchPromotion;
use Upkeep\Adapter\ServeResult;
use Upkeep\Adapter\WorkingCopyStatus;
use Upkeep\Cockpit\Module;
use Upkeep\Command\EnvPathCommand;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Tests\Support\StubEngineAdapterFactory;
use Upkeep\Workflow\ExitCode;

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
        return new class ($returnPath) implements EngineAdapterInterface {
            public function __construct(private readonly ?string $returnPath)
            {
            }
            public function ensureEnv(Module $module, string $coreMajor): Environment
            {
                throw new \BadMethodCallException();
            }
            public function applyMr(Environment $environment, MergeRequest $mergeRequest): void
            {
            }

            public function applyPatch(
                Environment $environment,
                PatchApplication $patch,
                BaseRefresh $refresh = BaseRefresh::Update,
            ): void {
            }

            public function startWork(
                Environment $environment,
                IssueBranch $branch,
                ?string $baseBranch = null,
                BaseRefresh $refresh = BaseRefresh::Update,
            ): bool {
                return false;
            }

            public function pushWork(Environment $environment, IssueBranch $branch, GitRemote $remote): string
            {
                return 'abc1234';
            }

            public function recordedBaseBranch(Environment $environment): ?string
            {
                return null;
            }

            public function promotePatch(
                Environment $environment,
                PatchApplication $patch,
                IssueBranch $branch,
                string $commitMessage,
                BaseRefresh $refresh = BaseRefresh::Update,
                bool $allowPartial = false,
            ): PatchPromotion {
                throw new \BadMethodCallException();
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
                return $this->returnPath;
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

    public function testPrintsPathWhenEnvironmentExists(): void
    {
        $tester = new CommandTester(new EnvPathCommand(
            new StubEngineAdapterFactory($this->adapter('/home/user/.upkeep/projects/upkeep-token-d11')),
        ));
        $exit = $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit);
        self::assertSame("/home/user/.upkeep/projects/upkeep-token-d11\n", $tester->getDisplay());
    }

    public function testDefaultsToFirstTrackedCoreVersion(): void
    {
        $calls = [];
        $adapter = new class ($calls) implements EngineAdapterInterface {
            /** @param list<array{string, string}> $calls */
            public function __construct(private array &$calls)
            {
            }
            public function ensureEnv(Module $module, string $coreMajor): Environment
            {
                throw new \BadMethodCallException();
            }
            public function applyMr(Environment $environment, MergeRequest $mergeRequest): void
            {
            }

            public function applyPatch(
                Environment $environment,
                PatchApplication $patch,
                BaseRefresh $refresh = BaseRefresh::Update,
            ): void {
            }

            public function startWork(
                Environment $environment,
                IssueBranch $branch,
                ?string $baseBranch = null,
                BaseRefresh $refresh = BaseRefresh::Update,
            ): bool {
                return false;
            }

            public function pushWork(Environment $environment, IssueBranch $branch, GitRemote $remote): string
            {
                return 'abc1234';
            }

            public function recordedBaseBranch(Environment $environment): ?string
            {
                return null;
            }

            public function promotePatch(
                Environment $environment,
                PatchApplication $patch,
                IssueBranch $branch,
                string $commitMessage,
                BaseRefresh $refresh = BaseRefresh::Update,
                bool $allowPartial = false,
            ): PatchPromotion {
                throw new \BadMethodCallException();
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
            public function resolveEnvPath(string $moduleName, string $coreMajor): string
            {
                // Read-modify-write, not `[] =`: the property is a reference
                // alias to the test's own array, which is where the
                // assertion reads the recording from — so appending in
                // place would look write-only to static analysis.
                $this->calls = [...$this->calls, [$moduleName, $coreMajor]];
                return '/some/path';
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

        $tester = new CommandTester(new EnvPathCommand(new StubEngineAdapterFactory($adapter)));
        $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame([['token', '10']], $calls, 'should default to the first core version listed (10)');
    }

    public function testRespectsExplicitVersionOption(): void
    {
        $calls = [];
        $adapter = new class ($calls) implements EngineAdapterInterface {
            /** @param list<array{string, string}> $calls */
            public function __construct(private array &$calls)
            {
            }
            public function ensureEnv(Module $module, string $coreMajor): Environment
            {
                throw new \BadMethodCallException();
            }
            public function applyMr(Environment $environment, MergeRequest $mergeRequest): void
            {
            }

            public function applyPatch(
                Environment $environment,
                PatchApplication $patch,
                BaseRefresh $refresh = BaseRefresh::Update,
            ): void {
            }

            public function startWork(
                Environment $environment,
                IssueBranch $branch,
                ?string $baseBranch = null,
                BaseRefresh $refresh = BaseRefresh::Update,
            ): bool {
                return false;
            }

            public function pushWork(Environment $environment, IssueBranch $branch, GitRemote $remote): string
            {
                return 'abc1234';
            }

            public function recordedBaseBranch(Environment $environment): ?string
            {
                return null;
            }

            public function promotePatch(
                Environment $environment,
                PatchApplication $patch,
                IssueBranch $branch,
                string $commitMessage,
                BaseRefresh $refresh = BaseRefresh::Update,
                bool $allowPartial = false,
            ): PatchPromotion {
                throw new \BadMethodCallException();
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
            public function resolveEnvPath(string $moduleName, string $coreMajor): string
            {
                // Read-modify-write, not `[] =`: the property is a reference
                // alias to the test's own array, which is where the
                // assertion reads the recording from — so appending in
                // place would look write-only to static analysis.
                $this->calls = [...$this->calls, [$moduleName, $coreMajor]];
                return '/some/path';
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

        $tester = new CommandTester(new EnvPathCommand(new StubEngineAdapterFactory($adapter)));
        $tester->execute(['module' => 'token', '--version' => '11', '--cockpit' => $this->cockpit]);

        self::assertSame([['token', '11']], $calls);
    }

    /**
     * The change this is all for: a module the cockpit does not watch is a
     * module you can still work on.
     *
     * `project/<name>` is drupal.org's convention and the cores this machine
     * can run are the ones base artifacts exist for, so nothing about a
     * registry entry was load-bearing here. See docs/any-module.md.
     */
    public function testAnUnregisteredModuleResolvesFromWhatIsOnDisk(): void
    {
        mkdir($this->cockpit . '/base-artifacts/10', 0o755, true);
        mkdir($this->cockpit . '/base-artifacts/11', 0o755, true);

        $tester = new CommandTester(new EnvPathCommand(new StubEngineAdapterFactory($this->adapter('/x'))));
        $exit = $tester->execute(['module' => 'paragraphs', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::OK, $exit, $tester->getDisplay());
        self::assertStringContainsString('/x', $tester->getDisplay());
    }

    /**
     * And `--version` still picks among them, so an unregistered module is not
     * a less controllable one.
     */
    public function testAnUnregisteredModuleStillHonoursTheVersionFlag(): void
    {
        mkdir($this->cockpit . '/base-artifacts/10', 0o755, true);
        mkdir($this->cockpit . '/base-artifacts/11', 0o755, true);

        $tester = new CommandTester(new EnvPathCommand(new StubEngineAdapterFactory($this->adapter('/x'))));
        $exit = $tester->execute(['module' => 'paragraphs', '--version' => '10', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::OK, $exit, $tester->getDisplay());
    }

    /**
     * A name that cannot be a Drupal machine name is still refused outright:
     * there is no project to go and look for.
     */
    public function testANameThatCannotBeAModuleIsRefused(): void
    {
        mkdir($this->cockpit . '/base-artifacts/11', 0o755, true);

        $tester = new CommandTester(new EnvPathCommand(new StubEngineAdapterFactory($this->adapter('/x'))));
        $exit = $tester->execute(['module' => 'Not-A-Module', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('machine name', $tester->getDisplay());
    }

    public function testFailsForUnregisteredModule(): void
    {
        $tester = new CommandTester(new EnvPathCommand(new StubEngineAdapterFactory($this->adapter('/x'))));
        $exit = $tester->execute(['module' => 'nope', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        // The registry is a watchlist now, so being absent from it is not the
        // refusal — having nothing built to run against is. See
        // docs/any-module.md.
        self::assertStringContainsString('no base artifacts', $tester->getDisplay());
    }

    public function testFailsForUntrackedCoreVersion(): void
    {
        $tester = new CommandTester(new EnvPathCommand(new StubEngineAdapterFactory($this->adapter('/x'))));
        $exit = $tester->execute(['module' => 'token', '--version' => '9', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('does not track core version', $tester->getDisplay());
    }

    public function testFailsWhenEnvironmentDoesNotExist(): void
    {
        $tester = new CommandTester(new EnvPathCommand(new StubEngineAdapterFactory($this->adapter(null))));
        $exit = $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('No provisioned environment', $tester->getDisplay());
    }
}

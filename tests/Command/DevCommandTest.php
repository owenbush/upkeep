<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\BaseRefresh;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\GitRemote;
use Upkeep\Adapter\IssueBranch;
use Upkeep\Adapter\PatchApplication;
use Upkeep\Adapter\ServeResult;
use Upkeep\Adapter\WorkingCopyStatus;
use Upkeep\Cockpit\Module;
use Upkeep\Command\DevCommand;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Tests\Support\StubEngineAdapterFactory;
use Upkeep\Workflow\ExitCode;

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

        $tester = new CommandTester(new DevCommand(new StubEngineAdapterFactory($adapter)));
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
        $tester = new CommandTester(new DevCommand(new StubEngineAdapterFactory($adapter)));
        $exit = $tester->execute(['module' => 'nonexistent', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        // The registry is a watchlist now, so being absent from it is not the
        // refusal — having nothing built to run against is. See
        // docs/any-module.md.
        self::assertStringContainsString('no base artifacts', $tester->getDisplay());
    }

    public function testBadCoreVersionFails(): void
    {
        $adapter = $this->adapter(null);
        $tester = new CommandTester(new DevCommand(new StubEngineAdapterFactory($adapter)));
        $exit = $tester->execute(['module' => 'token', '--version' => '9', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('does not track core version', $tester->getDisplay());
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

        $tester = new CommandTester(new DevCommand(new StubEngineAdapterFactory($adapter)));
        $exit = $tester->execute(['module' => 'token', '--branch' => 'feature/x', '--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit);
        self::assertSame(['feature/x'], $branchCalls);
    }

    public function testEnsureEnvFailureShowsError(): void
    {
        $adapter = $this->failingAdapter('Base artifacts missing');
        $tester = new CommandTester(new DevCommand(new StubEngineAdapterFactory($adapter)));
        $exit = $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
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

        $tester = new CommandTester(new DevCommand(new StubEngineAdapterFactory($adapter)));
        $exit = $tester->execute(['module' => 'token', '--branch' => 'feature/x', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('uncommitted changes', $tester->getDisplay());
    }

    /**
     * `dev` printed the engine's entire process transcript at normal
     * verbosity — every provisioning command's raw output — burying the four
     * lines that are the command's actual payload. The raw channel belongs
     * behind -v, as it is in every other command.
     */
    public function testTheEngineTranscriptIsBehindVerboseAndTheSummaryIsNot(): void
    {
        $env = new Environment(
            'token',
            '11',
            'upkeep-token-d11',
            '/home/.upkeep/projects/upkeep-token-d11',
            'https://upkeep-token-d11.ddev.site',
            false,
        );
        $factory = new StubEngineAdapterFactory(
            $this->adapter($env),
            stageLine: 'Reusing environment upkeep-token-d11',
            processLine: 'Container ddev-upkeep-token-d11-web  Started',
        );

        $tester = new CommandTester(new DevCommand($factory));
        $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);
        $quiet = $tester->getDisplay();

        self::assertStringNotContainsString('Container ddev-', $quiet);
        self::assertStringContainsString('Reusing environment', $quiet, 'the narrative still shows');
        self::assertStringContainsString('https://upkeep-token-d11.ddev.site', $quiet, 'and so does the payload');

        $verbose = new CommandTester(new DevCommand($factory));
        $verbose->execute(
            ['module' => 'token', '--cockpit' => $this->cockpit],
            ['verbosity' => OutputInterface::VERBOSITY_VERBOSE],
        );

        self::assertStringContainsString('Container ddev-', $verbose->getDisplay(), '-v still shows everything');
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
            ): string {
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
            /** @param list<string> $calls */
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
            ): string {
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
                // Read-modify-write, not `[] =`: the property is a reference
                // alias to the test's own array, which is where the
                // assertion reads the recording from — so appending in
                // place would look write-only to static analysis.
                $this->calls = [...$this->calls, $branch];
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
            ): string {
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
            ): string {
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

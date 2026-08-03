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
use Upkeep\Command\ExecCommand;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Tests\Support\StubEngineAdapterFactory;
use Upkeep\Workflow\ExitCode;

final class ExecCommandTest extends TestCase
{
    private string $cockpit;
    private string $envDir;

    protected function setUp(): void
    {
        $base = sys_get_temp_dir() . '/upkeep-exec-test-' . bin2hex(random_bytes(4));
        $this->cockpit = $base . '/cockpit';
        $this->envDir = $base . '/env';
        mkdir($this->cockpit . '/base-artifacts', 0755, true);
        mkdir($this->cockpit . '/fixtures', 0755, true);
        mkdir($this->envDir, 0755, true);
        file_put_contents($this->cockpit . '/registry.yml', <<<YAML
        modules:
          token:
            project: project/token
            core_versions: ["11"]
        YAML);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg(\dirname($this->cockpit)));
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

    public function testRunsCommandInEnvironmentDirectoryAndStreamsOutput(): void
    {
        $tester = new CommandTester(new ExecCommand(new StubEngineAdapterFactory($this->adapter($this->envDir))));
        $exit = $tester->execute([
            'module' => 'token',
            'cmd' => ['echo', 'hello from env'],
            '--cockpit' => $this->cockpit,
        ]);

        self::assertSame(0, $exit);
        self::assertStringContainsString('hello from env', $tester->getDisplay());
    }

    /**
     * The child's raw code is deliberately NOT passed through: 0/1/2 is the
     * CLI-wide contract, so a child exiting 2 cannot be read as an upkeep
     * infrastructure failure.
     */
    public function testCollapsesAnyNonZeroChildExitToTheFailedCode(): void
    {
        $tester = new CommandTester(new ExecCommand(new StubEngineAdapterFactory($this->adapter($this->envDir))));
        $exit = $tester->execute([
            'module' => 'token',
            'cmd' => ['bash', '-c', 'exit 42'],
            '--cockpit' => $this->cockpit,
        ]);

        self::assertSame(ExitCode::FAILED, $exit);
    }

    public function testSetsWorkingDirectoryToEnvironmentPath(): void
    {
        $tester = new CommandTester(new ExecCommand(new StubEngineAdapterFactory($this->adapter($this->envDir))));
        $tester->execute([
            'module' => 'token',
            'cmd' => ['pwd'],
            '--cockpit' => $this->cockpit,
        ]);

        self::assertStringContainsString($this->envDir, $tester->getDisplay());
    }

    public function testFailsForUnregisteredModule(): void
    {
        $tester = new CommandTester(new ExecCommand(new StubEngineAdapterFactory($this->adapter($this->envDir))));
        $exit = $tester->execute([
            'module' => 'nope',
            'cmd' => ['echo', 'hi'],
            '--cockpit' => $this->cockpit,
        ]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('not registered', $tester->getDisplay());
    }

    public function testFailsWhenEnvironmentDoesNotExist(): void
    {
        $tester = new CommandTester(new ExecCommand(new StubEngineAdapterFactory($this->adapter(null))));
        $exit = $tester->execute([
            'module' => 'token',
            'cmd' => ['echo', 'hi'],
            '--cockpit' => $this->cockpit,
        ]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('No provisioned environment', $tester->getDisplay());
    }

    /**
     * `upkeep exec <module> -- printenv` used to print the operator's PAT:
     * the child inherited the whole parent environment.
     */
    public function testDoesNotForwardTheGitlabCredentialToTheChild(): void
    {
        $name = TokenResolver::DEFAULT_ENV_VAR;
        $previous = getenv($name);
        $hadServer = \array_key_exists($name, $_SERVER);
        $hadEnv = \array_key_exists($name, $_ENV);
        putenv($name . '=glpat-EXECSECRETVALUE');
        $_SERVER[$name] = 'glpat-EXECSECRETVALUE';
        $_ENV[$name] = 'glpat-EXECSECRETVALUE';

        try {
            $tester = new CommandTester(new ExecCommand(new StubEngineAdapterFactory($this->adapter($this->envDir))));
            $tester->execute([
                'module' => 'token',
                'cmd' => ['bash', '-c', 'echo "${' . $name . ':-ABSENT}"'],
                '--cockpit' => $this->cockpit,
            ]);

            self::assertStringContainsString('ABSENT', $tester->getDisplay());
            self::assertStringNotContainsString('glpat-EXECSECRETVALUE', $tester->getDisplay());
        } finally {
            if (\is_string($previous)) {
                putenv($name . '=' . $previous);
            } else {
                putenv($name);
            }
            if (!$hadServer) {
                unset($_SERVER[$name]);
            }
            if (!$hadEnv) {
                unset($_ENV[$name]);
            }
        }
    }
}

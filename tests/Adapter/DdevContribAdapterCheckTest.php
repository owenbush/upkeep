<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use Upkeep\Adapter\CapturedProcess;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Adapter\EngineAddOn;
use Upkeep\Adapter\Environment;

/**
 * runChecks(): the toolchain gate, the per-check dispatch, and how each kind
 * of process outcome becomes a CheckResult.
 */
final class DdevContribAdapterCheckTest extends DdevAdapterTestCase
{
    /**
     * @param array<string, string|CapturedProcess|null> $overrides
     * @param list<CheckType>                            $checks
     *
     * @return array<string, CheckStatus> check value => status
     */
    private function statuses(array $checks, array $overrides = [], ?ScriptedCommandRunner $runner = null): array
    {
        $runner ??= $this->captureRunner($overrides);
        $result = $this->adapter($runner)->runChecks($this->environment(), $checks);

        $statuses = [];
        foreach ($result->results as $checkResult) {
            $statuses[$checkResult->type->value] = $checkResult->status;
        }

        return $statuses;
    }

    /**
     * @param array<string, string|CapturedProcess|null> $overrides
     */
    private function captureRunner(array $overrides = []): ScriptedCommandRunner
    {
        return new ScriptedCommandRunner(
            static function (array $command) use ($overrides): string|CapturedProcess|null {
                $line = implode(' ', $command);
                foreach ($overrides as $needle => $outcome) {
                    if (str_contains($line, $needle)) {
                        return $outcome;
                    }
                }

                return '';
            },
        );
    }

    /** Every check binary present, so the toolchain gate is satisfied. */
    private function installToolchain(): void
    {
        mkdir($this->projectPath() . '/vendor/bin', 0o700, true);
        foreach (['phpunit', 'phpstan', 'phpcs'] as $binary) {
            file_put_contents($this->projectPath() . '/vendor/bin/' . $binary, '');
        }
    }

    public function testEachCheckTypeDispatchesToItsOwnEngineCommand(): void
    {
        $this->installToolchain();
        $runner = $this->captureRunner();

        $this->adapter($runner)->runChecks($this->environment(), [
            CheckType::PhpUnit,
            CheckType::PhpCs,
            CheckType::PhpStan,
            CheckType::EsLint,
            CheckType::StyleLint,
            CheckType::ModuleInstall,
        ]);

        // The engine's own phpunit command, scoped to the module's install path.
        self::assertTrue($runner->issued('ddev phpunit web/' . EngineAddOn::PROJECTS_PATH . '/widget'));
        // phpcs and phpstan run the CI-aligned invocations scoped to the
        // module, not the whole projects path.
        self::assertTrue($runner->issued('phpcs -s --report-full'));
        self::assertTrue($runner->issued('"$DDEV_DOCROOT/$DRUPAL_PROJECTS_PATH"/\'widget\''));
        self::assertTrue($runner->issued('phpstan analyze'));
        self::assertTrue($runner->issued('ddev eslint'));
        self::assertTrue($runner->issued('ddev stylelint'));
        self::assertTrue($runner->issued('ddev drush pm:install widget -y'));
    }

    /**
     * The module name is quoted where it is spliced into a shell script body,
     * so a hostile name cannot break out of the phpcs/phpstan invocation.
     */
    public function testTheModuleNameIsQuotedIntoTheShellScriptBody(): void
    {
        $this->installToolchain();
        $runner = $this->captureRunner();

        $environment = new Environment(
            moduleName: "widget'; rm -rf /",
            coreMajor: self::CORE,
            projectName: self::projectName(),
            projectPath: $this->projectPath(),
            primaryUrl: self::PRIMARY_URL,
            reused: false,
        );

        $this->adapter($runner)->runChecks($environment, [CheckType::PhpStan]);

        self::assertTrue($runner->issued("'widget'\\''; rm -rf /'"));
    }

    public function testTheToolchainIsProvisionedOnlyWhenAToolchainCheckIsRequested(): void
    {
        // No binaries in vendor/bin: the gate must install the toolchain.
        $runner = $this->captureRunner();
        $this->adapter($runner)->runChecks($this->environment(), [CheckType::PhpCs]);

        self::assertTrue($runner->issued('composer require --dev --with-all-dependencies'));
        self::assertTrue($runner->issued('drupal/core-dev:^11'));
        self::assertTrue($runner->issued('allow-plugins.dealerdirect/phpcodesniffer-composer-installer true'));

        // A non-toolchain check needs none of it.
        $other = $this->captureRunner();
        $this->adapter($other)->runChecks($this->environment(), [CheckType::ModuleInstall]);
        self::assertFalse($other->issued('composer require --dev'));
    }

    public function testAnAlreadyProvisionedToolchainIsNotReinstalled(): void
    {
        $this->installToolchain();
        $runner = $this->captureRunner();

        $this->adapter($runner)->runChecks($this->environment(), [CheckType::PhpUnit]);

        self::assertFalse($runner->issued('composer require --dev'));
    }

    public function testTheDefaultSuiteIsRunWhenNoChecksAreNamed(): void
    {
        $this->installToolchain();

        $statuses = $this->statuses([]);

        self::assertSame([
            'phpunit',
            'phpstan',
            'phpcs',
            'module_install',
            'functional_smoke',
            'deprecation',
        ], array_keys($statuses));
    }

    public function testANonZeroExitIsAFailedCheckRatherThanAnException(): void
    {
        $this->installToolchain();

        $statuses = $this->statuses(
            [CheckType::PhpCs, CheckType::PhpUnit],
            ['phpcs -s' => new CapturedProcess(2, 'FILE: Widget.php', false, 1.5)],
        );

        self::assertSame(CheckStatus::Failed, $statuses['phpcs']);
        self::assertSame(CheckStatus::Passed, $statuses['phpunit']);
    }

    public function testATimedOutCheckIsAFailureCarryingItsPartialOutput(): void
    {
        $this->installToolchain();
        $runner = $this->captureRunner([
            'ddev phpunit' => new CapturedProcess(null, 'partial output', true, 1800.0),
        ]);

        $result = $this->adapter($runner)->runChecks($this->environment(), [CheckType::PhpUnit]);

        self::assertSame(CheckStatus::Failed, $result->results[0]->status);
        self::assertStringContainsString('Check timed out after 1800s.', $result->results[0]->output);
        self::assertStringContainsString('partial output', $result->results[0]->output);
    }

    public function testASmokeCheckPassesOnlyOnHttp200(): void
    {
        $passed = $this->statuses(
            [CheckType::FunctionalSmoke],
            ['curl' => new CapturedProcess(0, '200', false, 0.2)],
        );
        self::assertSame(CheckStatus::Passed, $passed['functional_smoke']);

        $failed = $this->statuses(
            [CheckType::FunctionalSmoke],
            ['curl' => new CapturedProcess(0, '500', false, 0.2)],
        );
        self::assertSame(CheckStatus::Failed, $failed['functional_smoke']);
    }

    public function testASmokeCheckThatCouldNotRequestAtAllReportsTheRequestFailure(): void
    {
        $runner = $this->captureRunner([
            'curl' => new CapturedProcess(7, 'Failed to connect', false, 0.2),
        ]);

        $result = $this->adapter($runner)->runChecks($this->environment(), [CheckType::FunctionalSmoke]);

        self::assertSame(CheckStatus::Failed, $result->results[0]->status);
        self::assertSame(7, $result->results[0]->exitCode);
        self::assertStringContainsString('Request failed: Failed to connect', $result->results[0]->output);
    }

    public function testASmokeCheckThatTimesOutIsAFailure(): void
    {
        $runner = $this->captureRunner([
            'curl' => new CapturedProcess(null, '', true, 120.0),
        ]);

        $result = $this->adapter($runner)->runChecks($this->environment(), [CheckType::FunctionalSmoke]);

        self::assertSame(CheckStatus::Failed, $result->results[0]->status);
        self::assertStringContainsString('Check timed out after 120s.', $result->results[0]->output);
    }

    /**
     * The pinned engine ships no upgrade-status command, so the deprecation
     * check is explicitly Unavailable — never silently omitted from the suite.
     */
    public function testTheDeprecationCheckIsUnavailableWithoutAnEngineCommand(): void
    {
        $runner = $this->captureRunner();

        $result = $this->adapter($runner)->runChecks($this->environment(), [CheckType::Deprecation]);

        self::assertSame(CheckStatus::Unavailable, $result->results[0]->status);
        self::assertNull($result->results[0]->exitCode);
        self::assertStringContainsString(EngineAddOn::VERSION, $result->results[0]->output);
        self::assertFalse($runner->issued('ddev upgrade-status'));
    }

    public function testTheDeprecationCheckRunsWhenTheEngineProvidesTheCommand(): void
    {
        mkdir($this->projectPath() . '/.ddev/commands/web', 0o700, true);
        file_put_contents($this->projectPath() . '/.ddev/commands/web/upgrade-status', "#!/bin/bash\n");

        $runner = $this->captureRunner([
            'ddev upgrade-status' => new CapturedProcess(1, '2 deprecations found', false, 12.0),
        ]);
        $result = $this->adapter($runner)->runChecks($this->environment(), [CheckType::Deprecation]);

        self::assertSame(CheckStatus::Failed, $result->results[0]->status);
        self::assertSame('2 deprecations found', $result->results[0]->output);
    }

    public function testADeprecationCheckThatTimesOutIsAFailure(): void
    {
        mkdir($this->projectPath() . '/.ddev/commands/web', 0o700, true);
        file_put_contents($this->projectPath() . '/.ddev/commands/web/upgrade-status', "#!/bin/bash\n");

        $runner = $this->captureRunner([
            'ddev upgrade-status' => new CapturedProcess(null, 'still scanning', true, 1800.0),
        ]);
        $result = $this->adapter($runner)->runChecks($this->environment(), [CheckType::Deprecation]);

        self::assertSame(CheckStatus::Failed, $result->results[0]->status);
        self::assertStringContainsString('Check timed out after 1800s.', $result->results[0]->output);
    }
}

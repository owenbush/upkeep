<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Process\Process;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckType;
use Upkeep\Adapter\ProcessRunner;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;
use Upkeep\Security\SecretRedactor;

/**
 * Subprocesses here are `php -r` one-liners: hermetic, no docker, no network.
 */
final class ProcessRunnerTest extends TestCase
{
    private const SENTINEL = 'SENTINEL-TOKEN-VALUE-9f3a1c';

    /** @var list<string> */
    private array $logged = [];

    protected function setUp(): void
    {
        $this->logged = [];
    }

    private function runner(?SecretRedactor $redactor = null): ProcessRunner
    {
        return new ProcessRunner(
            function (string $line): void {
                $this->logged[] = $line;
            },
            $redactor ?? new SecretRedactor(self::SENTINEL),
        );
    }

    /**
     * @param list<string> $lines
     *
     * @return list<string>
     */
    private static function php(array $lines): array
    {
        return [\PHP_BINARY, '-r', implode(' ', $lines)];
    }

    public function testRunReturnsStdoutAndStreamsItToTheLog(): void
    {
        $out = $this->runner()->run(self::php(['echo "hello\n";']), null, 10);

        $this->assertSame("hello\n", $out);
        $this->assertSame(['  hello'], $this->logged);
    }

    /**
     * The core leakage test: the sentinel is in the argv *and* in the child's
     * stdout and stderr. Neither the exception message nor anything the log
     * closure received may contain it.
     */
    public function testSentinelTokenLeaksIntoNeitherTheExceptionMessageNorTheLog(): void
    {
        $command = self::php([
            'fwrite(STDERR, "stderr carries ' . self::SENTINEL . '\n");',
            'echo "stdout carries ' . self::SENTINEL . '\n";',
            'exit(3);',
        ]);

        // Precondition: the sentinel really is in the argument vector.
        $this->assertStringContainsString(self::SENTINEL, implode(' ', $command));

        try {
            $this->runner()->run($command, null, 10);
            $this->fail('Expected AdapterException for the non-zero exit.');
        } catch (AdapterException $e) {
            $this->assertStringNotContainsString(self::SENTINEL, $e->getMessage());
            $this->assertStringContainsString(SecretRedactor::MASK, $e->getMessage());
            $this->assertStringContainsString('3', $e->getMessage());
        }

        $this->assertNotSame([], $this->logged);
        $this->assertStringNotContainsString(self::SENTINEL, implode("\n", $this->logged));
    }

    public function testCaptureRedactsTheOutputItReturnsAndTheOutputItLogs(): void
    {
        $captured = $this->runner()->capture(
            self::php(['echo "leaking ' . self::SENTINEL . '\n";', 'exit(1);']),
            null,
            10,
        );

        $this->assertSame(1, $captured->exitCode);
        $this->assertStringNotContainsString(self::SENTINEL, $captured->output);
        $this->assertStringContainsString(SecretRedactor::MASK, $captured->output);
        $this->assertStringNotContainsString(self::SENTINEL, implode("\n", $this->logged));
    }

    public function testTryRunRedactsWhatItStreamsToTheLog(): void
    {
        $out = $this->runner()->tryRun(self::php(['echo "ok ' . self::SENTINEL . '\n";']), null, 10);

        $this->assertNotNull($out);
        $this->assertStringNotContainsString(self::SENTINEL, implode("\n", $this->logged));
    }

    /**
     * Layer 1 of the fix: the credential variable is removed from the child
     * environment outright, so a cooperating child cannot echo it at all.
     */
    public function testCredentialEnvironmentVariableIsNotInheritedByChildren(): void
    {
        $probe = self::php([
            'echo getenv("' . TokenResolver::DEFAULT_ENV_VAR . '") === false ? "ABSENT" : "PRESENT";',
        ]);

        $restore = self::exportForChildren(TokenResolver::DEFAULT_ENV_VAR, 'glpat-INHERITEDSECRET');

        try {
            // Control: without the scrub the child does see the variable, so
            // the assertion below cannot pass vacuously.
            $control = new Process($probe, null, timeout: 10);
            $control->run();
            $this->assertSame('PRESENT', $control->getOutput());

            $this->assertSame('ABSENT', $this->runner()->run($probe, null, 10));
        } finally {
            $restore();
        }
    }

    public function testCaptureAlsoRunsChildrenWithoutTheCredentialVariable(): void
    {
        $probe = self::php([
            'echo getenv("' . TokenResolver::DEFAULT_ENV_VAR . '") === false ? "ABSENT" : "PRESENT";',
        ]);

        $restore = self::exportForChildren(TokenResolver::DEFAULT_ENV_VAR, 'glpat-INHERITEDSECRET');

        try {
            $this->assertSame('ABSENT', $this->runner()->capture($probe, null, 10)->output);
        } finally {
            $restore();
        }
    }

    /**
     * Symfony's Process merges getenv(), $_ENV and $_SERVER when building the
     * child environment; an exported variable is in all three.
     *
     * @return \Closure(): void restores the previous state
     */
    private static function exportForChildren(string $name, string $value): \Closure
    {
        $previousEnv = getenv($name);
        $hadServer = \array_key_exists($name, $_SERVER);
        $hadEnv = \array_key_exists($name, $_ENV);
        $previousServer = $_SERVER[$name] ?? null;
        $previousEnvSuper = $_ENV[$name] ?? null;

        putenv($name . '=' . $value);
        $_SERVER[$name] = $value;
        $_ENV[$name] = $value;

        return static function () use (
            $name,
            $previousEnv,
            $hadServer,
            $hadEnv,
            $previousServer,
            $previousEnvSuper
        ): void {
            if (\is_string($previousEnv)) {
                putenv($name . '=' . $previousEnv);
            } else {
                putenv($name);
            }
            if ($hadServer) {
                $_SERVER[$name] = $previousServer;
            } else {
                unset($_SERVER[$name]);
            }
            if ($hadEnv) {
                $_ENV[$name] = $previousEnvSuper;
            } else {
                unset($_ENV[$name]);
            }
        };
    }

    public function testFailureMessageBoundsUnboundedChildOutput(): void
    {
        $command = self::php(['echo str_repeat("x", 60000);', 'exit(2);']);

        try {
            $this->runner()->run($command, null, 20);
            $this->fail('Expected AdapterException for the non-zero exit.');
        } catch (AdapterException $e) {
            $this->assertLessThan(
                60000,
                \strlen($e->getMessage()),
                'Unbounded child output must not become the whole exception message.',
            );
            $this->assertStringContainsString('truncated', $e->getMessage());
        }
    }

    public function testRunConvertsATimeoutIntoAnAdapterException(): void
    {
        $this->expectException(AdapterException::class);
        $this->expectExceptionMessageMatches('/timed out/i');

        $this->runner()->run(self::php(['sleep(30);']), null, 1);
    }

    /**
     * A timeout reports the last thing the command printed.
     *
     * A timeout is exactly the case where output is the only clue: the
     * command did not fail, it stopped making progress, and its last line is
     * where. `ddev start` stalling on "Starting Mutagen sync process..." after
     * a reboot produced an error naming only the command.
     */
    public function testATimeoutSaysWhereTheCommandStopped(): void
    {
        try {
            $this->runner()->run(
                self::php(['echo "Starting Mutagen sync process...", PHP_EOL; flush(); sleep(30);']),
                null,
                1,
            );
            $this->fail('Expected a timeout.');
        } catch (AdapterException $e) {
            $this->assertStringContainsString('timed out', $e->getMessage());
            $this->assertStringContainsString('Last output before it stopped', $e->getMessage());
            $this->assertStringContainsString('Starting Mutagen sync process', $e->getMessage());
        }
    }

    /**
     * The idle hook fires as each child exits, however it exits.
     *
     * It is what clears a live status line, and it has to fire on the paths
     * that matter most for that: a timeout is exactly when the last line on
     * screen is the stall, and leaving it there would glue the error message
     * onto the end of it.
     */
    public function testTheIdleHookFiresWhenEveryChildExitsHoweverItExits(): void
    {
        $idle = 0;
        $runner = new ProcessRunner(
            static function (): void {
            },
            null,
            static function () use (&$idle): void {
                ++$idle;
            },
        );

        $runner->run(self::php(['echo "ok";']));
        self::assertSame(1, $idle, 'a clean run');

        $runner->tryRun(self::php(['exit(3);']));
        self::assertSame(2, $idle, 'a failed run');

        $runner->capture(self::php(['echo "captured";']));
        self::assertSame(3, $idle, 'a capture');

        $runner->tryRun(self::php(['sleep(30);']), null, 1);
        self::assertSame(4, $idle, 'a timeout — the case the status line matters most in');
    }

    public function testTryRunReturnsNullOnTimeoutInsteadOfThrowing(): void
    {
        $this->assertNull($this->runner()->tryRun(self::php(['sleep(30);']), null, 1));
    }

    public function testCaptureStillReportsATimeoutAsData(): void
    {
        $captured = $this->runner()->capture(self::php(['sleep(30);']), null, 1);

        $this->assertTrue($captured->timedOut);
    }

    /**
     * The persisting sink: capture() output becomes CheckResult::$output and
     * is written to <cockpit>/results/**. Redacting at the runner boundary is
     * what keeps the sentinel off disk.
     */
    public function testCapturedOutputStaysRedactedThroughTheCachedResultFile(): void
    {
        $captured = $this->runner()->capture(
            self::php(['echo "boom ' . self::SENTINEL . '\n";', 'exit(1);']),
            null,
            10,
        );

        $resultsDir = sys_get_temp_dir() . '/upkeep-results-' . bin2hex(random_bytes(4));

        try {
            (new ResultsCache($resultsDir))->store(
                'token_or',
                ResultKey::mergeRequest(7),
                '11',
                'deadbee',
                new CheckRunResult([
                CheckResult::fromProcess(
                    CheckType::PhpUnit,
                    $captured->exitCode,
                    $captured->output,
                    $captured->durationSeconds,
                ),
                ])
            );

            $written = glob($resultsDir . '/token_or/7/11/*.json') ?: [];
            $this->assertCount(1, $written);
            $contents = (string) file_get_contents($written[0]);
            $this->assertStringNotContainsString(self::SENTINEL, $contents);
            $this->assertStringContainsString(SecretRedactor::MASK, $contents);
        } finally {
            foreach (glob($resultsDir . '/token_or/7/11/*.json') ?: [] as $file) {
                unlink($file);
            }
            @rmdir($resultsDir . '/token_or/7/11');
            @rmdir($resultsDir . '/token_or/7');
            @rmdir($resultsDir . '/token_or');
            @rmdir($resultsDir);
        }
    }
}

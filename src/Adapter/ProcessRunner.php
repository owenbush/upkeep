<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Symfony\Component\Process\Exception\ProcessTimedOutException;
use Symfony\Component\Process\Process;
use Upkeep\Security\CredentialEnvironment;
use Upkeep\Security\SecretRedactor;

/**
 * Shell-out helper shared by the adapter's engine interactions: streams
 * output lines to the log as they arrive (engine operations are long-running)
 * and turns failures into AdapterException.
 *
 * Two credential guarantees, both enforced here because this class owns every
 * boundary child output crosses:
 *   1. children never inherit the credential environment variables
 *      (CredentialEnvironment::scrubbed());
 *   2. everything leaving this class — log lines, exception messages, and the
 *      combined output in CapturedProcess, which callers persist — passes
 *      through the redactor first.
 */
final readonly class ProcessRunner
{
    public const DEFAULT_TIMEOUT = 3600;

    /**
     * Child output is untrusted and unbounded; a failing composer or engine
     * step can emit megabytes. Failure messages carry the tail only.
     */
    public const FAILURE_OUTPUT_BYTES = 4000;

    private SecretRedactor $redactor;

    /**
     * @param \Closure(string): void $log
     */
    public function __construct(private \Closure $log, ?SecretRedactor $redactor = null)
    {
        $this->redactor = $redactor ?? SecretRedactor::fromEnvironment();
    }

    /**
     * Runs a command and returns its stdout; throws on failure or timeout.
     *
     * @param list<string> $command
     */
    public function run(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): string
    {
        $timedOut = false;
        $process = $this->start($command, $cwd, $timeout, $timedOut);

        if ($timedOut) {
            throw new AdapterException($this->redactor->redact(sprintf(
                'Command timed out after %ds: %s',
                $timeout,
                $process->getCommandLine(),
            )));
        }

        if (!$process->isSuccessful()) {
            throw new AdapterException($this->redactor->redact(sprintf(
                "Command failed (%s): %s\n%s",
                self::exitCodeLabel($process),
                $process->getCommandLine(),
                $this->failureExcerpt($process->getErrorOutput() . "\n" . $process->getOutput()),
            )));
        }

        return $process->getOutput();
    }

    /**
     * Runs a command tolerating failure: returns stdout on success, null on
     * any non-zero exit or timeout. For probes and best-effort cleanup steps.
     *
     * @param list<string> $command
     */
    public function tryRun(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): ?string
    {
        $timedOut = false;
        $process = $this->start($command, $cwd, $timeout, $timedOut);

        return !$timedOut && $process->isSuccessful() ? $process->getOutput() : null;
    }

    /**
     * Runs a command treating any outcome — success, failure, timeout — as
     * data. For check runs, where a non-zero exit is a result to report,
     * not an error to throw.
     *
     * @param list<string> $command
     */
    public function capture(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): CapturedProcess
    {
        $started = microtime(true);
        $combined = '';
        $process = $this->process($command, $cwd, $timeout);
        $timedOut = false;

        try {
            $process->run(function (string $type, string $buffer) use (&$combined): void {
                $combined .= $buffer;
                $this->stream($buffer);
            });
        } catch (ProcessTimedOutException) {
            $timedOut = true;
        }

        return new CapturedProcess(
            exitCode: $process->getExitCode(),
            output: $this->redactor->redact($combined),
            timedOut: $timedOut,
            durationSeconds: microtime(true) - $started,
        );
    }

    /**
     * @param list<string> $command
     * @param bool         $timedOut set to true when the child hit $timeout
     */
    private function start(array $command, ?string $cwd, int $timeout, bool &$timedOut): Process
    {
        $timedOut = false;
        $process = $this->process($command, $cwd, $timeout);

        try {
            $process->run(function (string $type, string $buffer): void {
                $this->stream($buffer);
            });
        } catch (ProcessTimedOutException) {
            $timedOut = true;
        }

        return $process;
    }

    /**
     * @param list<string> $command
     */
    private function process(array $command, ?string $cwd, int $timeout): Process
    {
        return new Process($command, $cwd, CredentialEnvironment::scrubbed(), timeout: $timeout);
    }

    /**
     * Symfony types Process::getExitCode() as ?int: null means the child never
     * reported a status. That is not itself a status, so it is never collapsed
     * onto a number (every value in 0..255 is a status some command really
     * returns) — it is named.
     */
    private static function exitCodeLabel(Process $process): string
    {
        $exitCode = $process->getExitCode();

        return $exitCode === null ? 'no exit status' : (string) $exitCode;
    }

    private function stream(string $buffer): void
    {
        foreach (explode("\n", rtrim($buffer, "\n")) as $line) {
            ($this->log)('  ' . $this->redactor->redact($line));
        }
    }

    private function failureExcerpt(string $output): string
    {
        $output = trim($output);
        if (\strlen($output) <= self::FAILURE_OUTPUT_BYTES) {
            return $output;
        }

        return sprintf(
            "[output truncated to the last %d bytes]\n%s",
            self::FAILURE_OUTPUT_BYTES,
            substr($output, -self::FAILURE_OUTPUT_BYTES),
        );
    }
}

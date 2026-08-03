<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The adapter's shell-out seam.
 *
 * Every engine interaction in this namespace reaches the outside world through
 * exactly these three verbs, which differ only in how they treat a non-zero
 * exit: `run()` throws, `tryRun()` yields null, `capture()` returns the outcome
 * as data. ProcessRunner is the one implementation that actually spawns
 * children (and the one place that owns the credential-scrubbing and redaction
 * guarantees); the interface exists so the orchestration above it — which
 * commands to issue, in what order, and what to conclude from each result — can
 * be exercised without a container runtime present.
 */
interface CommandRunner
{
    public const DEFAULT_TIMEOUT = 3600;

    /**
     * Runs a command and returns its stdout; throws on failure or timeout.
     *
     * @param list<string> $command
     *
     * @throws AdapterException when the command fails or times out
     */
    public function run(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): string;

    /**
     * Runs a command tolerating failure: stdout on success, null on any
     * non-zero exit or timeout. For probes and best-effort cleanup steps.
     *
     * @param list<string> $command
     */
    public function tryRun(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): ?string;

    /**
     * Runs a command treating every outcome — success, failure, timeout — as
     * data. For check runs, where a non-zero exit is a result to report rather
     * than an error to raise.
     *
     * @param list<string> $command
     */
    public function capture(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): CapturedProcess;
}

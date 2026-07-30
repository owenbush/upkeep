<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Outcome of ProcessRunner::capture(): a finished (or timed-out) process
 * whose failure is data, not an exception — check runs need the exit code
 * and output either way.
 */
final readonly class CapturedProcess
{
    public function __construct(
        public int $exitCode,
        /** Combined stdout + stderr, in stream order as far as the runner sees it. */
        public string $output,
        public bool $timedOut,
        public float $durationSeconds,
    ) {
    }
}

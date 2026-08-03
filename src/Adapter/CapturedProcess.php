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
        /**
         * The child's exit status, or null when it produced none at all —
         * killed before it could report one, or never executed. "Unknown" is
         * deliberately its own value rather than a sentinel integer: every
         * number in 0..255 is a status some command really returns, so
         * flattening the unknown case onto one (the old `?? -1`) would make
         * it indistinguishable from a real result. Same contract as
         * Workflow\ExitCode::forChildProcess().
         */
        public ?int $exitCode,
        /** Combined stdout + stderr, in stream order as far as the runner sees it. */
        public string $output,
        public bool $timedOut,
        public float $durationSeconds,
    ) {
    }
}

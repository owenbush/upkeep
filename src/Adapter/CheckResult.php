<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Outcome of one check suite run inside an environment.
 */
final readonly class CheckResult
{
    public function __construct(
        public CheckType $type,
        public bool $passed,
        public int $exitCode,
        /** Combined stdout/stderr of the run, for reporting and triage. */
        public string $output,
        public float $durationSeconds,
    ) {
    }
}

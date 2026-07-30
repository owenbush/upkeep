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
        public CheckStatus $status,
        /** Null when the check never ran (CheckStatus::Unavailable). */
        public ?int $exitCode,
        /** Combined stdout/stderr of the run, for reporting and triage. */
        public string $output,
        public float $durationSeconds,
    ) {
    }

    /**
     * Classifies a finished check process into a result. A phpunit run that
     * discovered no tests is the distinguishable NoTests outcome regardless
     * of exit code (PHPUnit 11.5 exits 0 for it — a silent pass would hide
     * the fact that nothing ran); otherwise zero exit is a pass and any
     * non-zero exit a failure.
     */
    public static function fromProcess(CheckType $type, int $exitCode, string $output, float $durationSeconds): self
    {
        $status = match (true) {
            $type === CheckType::PhpUnit && preg_match('/No tests (executed|found)/i', $output) === 1 => CheckStatus::NoTests,
            $exitCode === 0 => CheckStatus::Passed,
            default => CheckStatus::Failed,
        };

        return new self($type, $status, $exitCode, $output, $durationSeconds);
    }

    /**
     * A check that exceeded its timebox: a failure with the reason recorded
     * ahead of whatever partial output the run produced.
     */
    public static function timedOut(CheckType $type, string $partialOutput, float $durationSeconds, int $timeoutSeconds): self
    {
        return new self(
            $type,
            CheckStatus::Failed,
            null,
            sprintf("Check timed out after %ds.\n%s", $timeoutSeconds, $partialOutput),
            $durationSeconds,
        );
    }

    /**
     * A check the engine cannot run for this environment — recorded
     * explicitly in the result set, never silently omitted.
     */
    public static function unavailable(CheckType $type, string $reason): self
    {
        return new self($type, CheckStatus::Unavailable, null, $reason, 0.0);
    }

    public function passed(): bool
    {
        return $this->status->passed();
    }
}

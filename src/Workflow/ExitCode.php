<?php

declare(strict_types=1);

namespace Upkeep\Workflow;

use Upkeep\Adapter\CheckRunResult;

/**
 * The check/review commands' exit-code contract, kept in one place so
 * scripting can rely on it:
 *
 *   0 — every check passed (CheckStatus::NoTests and ::Unavailable are
 *       honest non-failures, matching CheckStatus::passed());
 *   1 — at least one check Failed;
 *   2 — infrastructure error: the run never produced a verdict (context
 *       resolution, provisioning, MR apply, or fixture load failed).
 */
final readonly class ExitCode
{
    public const int OK = 0;
    public const int CHECKS_FAILED = 1;
    public const int INFRASTRUCTURE = 2;

    public static function forRun(CheckRunResult $run): int
    {
        return $run->allPassed() ? self::OK : self::CHECKS_FAILED;
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Workflow;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Workflow\ExitCode;

final class ExitCodeTest extends TestCase
{
    public function testAllGreenRunMapsToOk(): void
    {
        $run = new CheckRunResult([
            self::check(CheckType::PhpUnit, CheckStatus::Passed),
            self::check(CheckType::PhpCs, CheckStatus::Passed),
        ]);

        self::assertSame(ExitCode::OK, ExitCode::forRun($run));
    }

    public function testNoTestsAndUnavailableAreNonBlocking(): void
    {
        $run = new CheckRunResult([
            self::check(CheckType::PhpUnit, CheckStatus::NoTests),
            self::check(CheckType::Deprecation, CheckStatus::Unavailable),
            self::check(CheckType::PhpStan, CheckStatus::Passed),
        ]);

        self::assertSame(ExitCode::OK, ExitCode::forRun($run));
    }

    public function testAnySingleFailureMapsToFailed(): void
    {
        $run = new CheckRunResult([
            self::check(CheckType::PhpUnit, CheckStatus::Failed),
            self::check(CheckType::PhpCs, CheckStatus::Passed),
        ]);

        self::assertSame(ExitCode::FAILED, ExitCode::forRun($run));
    }

    public function testTheThreeCodesAreDistinctAndStable(): void
    {
        // Scripting contract: 0 the command did what was asked, 1 the work it
        // supervised failed, 2 upkeep could not do the job.
        self::assertSame(0, ExitCode::OK);
        self::assertSame(1, ExitCode::FAILED);
        self::assertSame(2, ExitCode::INFRASTRUCTURE);
    }

    public function testASucceedingChildProcessMapsToOk(): void
    {
        self::assertSame(ExitCode::OK, ExitCode::forChildProcess(0));
    }

    /**
     * Every non-zero child code collapses to 1: a wrapped command exiting 2
     * must never be readable as an upkeep infrastructure failure.
     */
    public function testEveryNonZeroChildCodeCollapsesToFailed(): void
    {
        foreach ([1, 2, 42, 127, 255] as $code) {
            self::assertSame(ExitCode::FAILED, ExitCode::forChildProcess($code), sprintf('child %d', $code));
        }
    }

    public function testAChildThatNeverRanIsAnInfrastructureFailure(): void
    {
        self::assertSame(ExitCode::INFRASTRUCTURE, ExitCode::forChildProcess(null));
    }

    private static function check(CheckType $type, CheckStatus $status): CheckResult
    {
        return new CheckResult($type, $status, $status === CheckStatus::Unavailable ? null : 0, '', 1.0);
    }
}

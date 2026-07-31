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

    public function testAnySingleFailureMapsToChecksFailed(): void
    {
        $run = new CheckRunResult([
            self::check(CheckType::PhpUnit, CheckStatus::Failed),
            self::check(CheckType::PhpCs, CheckStatus::Passed),
        ]);

        self::assertSame(ExitCode::CHECKS_FAILED, ExitCode::forRun($run));
    }

    public function testTheThreeCodesAreDistinctAndStable(): void
    {
        // Scripting contract: 0 all-green, 1 red checks, 2 infrastructure.
        self::assertSame(0, ExitCode::OK);
        self::assertSame(1, ExitCode::CHECKS_FAILED);
        self::assertSame(2, ExitCode::INFRASTRUCTURE);
    }

    private static function check(CheckType $type, CheckStatus $status): CheckResult
    {
        return new CheckResult($type, $status, $status === CheckStatus::Unavailable ? null : 0, '', 1.0);
    }
}

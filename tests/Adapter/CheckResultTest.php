<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;

#[CoversClass(CheckResult::class)]
#[CoversClass(CheckRunResult::class)]
#[CoversClass(CheckStatus::class)]
final class CheckResultTest extends TestCase
{
    public function testZeroExitIsAPass(): void
    {
        $result = CheckResult::fromProcess(CheckType::PhpCs, 0, "OK\n", 1.2);
        self::assertSame(CheckStatus::Passed, $result->status);
        self::assertTrue($result->passed());
    }

    public function testNonZeroExitIsAFailure(): void
    {
        $result = CheckResult::fromProcess(CheckType::PhpStan, 1, "[ERROR] Found 3 errors\n", 4.5);
        self::assertSame(CheckStatus::Failed, $result->status);
        self::assertFalse($result->passed());
    }

    public function testPhpUnitWithNoDiscoveredTestsIsRecordedAsNoTestsNotAFailure(): void
    {
        // A module without a tests/ directory is a legitimate state: the
        // engine's phpunit run then reports no tests, which must be a
        // distinguishable recorded outcome, never a crash, a plain failure,
        // or a silent pass. Observed live: PHPUnit 11.5 exits 0 for this,
        // so classification must not depend on the exit code.
        $zeroExit = CheckResult::fromProcess(
            CheckType::PhpUnit,
            0,
            "PHPUnit 11.5.56 by Sebastian Bergmann and contributors.\n\n"
            . "Runtime:       PHP 8.4.18\n\nNo tests executed!\n",
            2.0,
        );
        self::assertSame(CheckStatus::NoTests, $zeroExit->status);
        self::assertTrue($zeroExit->passed());

        $nonZeroExit = CheckResult::fromProcess(CheckType::PhpUnit, 1, "No tests executed!\n", 2.0);
        self::assertSame(CheckStatus::NoTests, $nonZeroExit->status);
    }

    public function testPhpUnitWithRealFailuresIsAFailure(): void
    {
        $result = CheckResult::fromProcess(
            CheckType::PhpUnit,
            1,
            "FAILURES!\nTests: 5, Assertions: 9, Failures: 1.\n",
            3.0,
        );
        self::assertSame(CheckStatus::Failed, $result->status);
    }

    /**
     * A child that reported no exit status at all (CapturedProcess::$exitCode
     * is null) has not passed: only exit 0 is a pass, and "unknown" is kept as
     * null rather than flattened onto a sentinel number that a real command
     * could also return.
     */
    public function testCheckWithNoExitStatusIsAFailureRatherThanAPass(): void
    {
        $result = CheckResult::fromProcess(CheckType::PhpCs, null, 'killed', 2.0);

        self::assertSame(CheckStatus::Failed, $result->status);
        self::assertFalse($result->passed());
        self::assertNull($result->exitCode);
    }

    public function testTimedOutCheckIsAFailureWithTheReasonRecorded(): void
    {
        $result = CheckResult::timedOut(CheckType::PhpUnit, 'partial output', 1800.0, 1800);
        self::assertSame(CheckStatus::Failed, $result->status);
        self::assertStringContainsString('timed out after 1800s', $result->output);
        self::assertStringContainsString('partial output', $result->output);
    }

    public function testUnavailableCheckIsRecordedExplicitly(): void
    {
        $result = CheckResult::unavailable(
            CheckType::Deprecation,
            'The engine provides no deprecation command for Drupal 11.',
        );
        self::assertSame(CheckStatus::Unavailable, $result->status);
        self::assertNull($result->exitCode);
        self::assertTrue($result->passed());
    }

    public function testRunResultOnlyCountsFailedStatusAsFailure(): void
    {
        $run = new CheckRunResult([
            CheckResult::fromProcess(CheckType::PhpCs, 0, '', 1.0),
            CheckResult::fromProcess(CheckType::PhpUnit, 1, "No tests executed!\n", 1.0),
            CheckResult::unavailable(CheckType::Deprecation, 'n/a'),
        ]);
        self::assertTrue($run->allPassed());
        self::assertSame([], $run->failures());

        $withFailure = new CheckRunResult([
            CheckResult::fromProcess(CheckType::PhpStan, 1, 'errors', 1.0),
        ]);
        self::assertFalse($withFailure->allPassed());
        self::assertCount(1, $withFailure->failures());
    }
}

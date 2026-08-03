<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;

/**
 * The console report and the merge-request comment used to truncate check
 * output two different ways with two separately declared limits. One rule
 * now, tested here.
 */
final class CheckResultExcerptTest extends TestCase
{
    public function testShortOutputIsReturnedWhole(): void
    {
        self::assertSame('boom', self::failedCheck('boom')->outputExcerpt());
    }

    public function testSurroundingWhitespaceIsTrimmed(): void
    {
        self::assertSame('boom', self::failedCheck("\n  boom  \n\n")->outputExcerpt());
    }

    public function testEmptyOutputIsAnEmptyExcerptRatherThanWhitespace(): void
    {
        self::assertSame('', self::failedCheck("   \n\n ")->outputExcerpt());
    }

    /** The tail is where the failure is; the head is scrolled-past noise. */
    public function testLongOutputKeepsTheTail(): void
    {
        $output = str_repeat('a', 3000) . 'THE FAILURE';

        $excerpt = self::failedCheck($output)->outputExcerpt(100);

        self::assertSame(100, \strlen($excerpt));
        self::assertStringEndsWith('THE FAILURE', $excerpt);
    }

    public function testTheDefaultLimitIsTheSharedConstant(): void
    {
        $excerpt = self::failedCheck(str_repeat('x', CheckResult::EXCERPT_BYTES * 2))->outputExcerpt();

        self::assertSame(CheckResult::EXCERPT_BYTES, \strlen($excerpt));
    }

    private static function failedCheck(string $output): CheckResult
    {
        return new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 1, $output, 1.0);
    }
}

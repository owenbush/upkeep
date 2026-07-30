<?php

declare(strict_types=1);

namespace Upkeep\Tests\Maintenance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Maintenance\Duration;

final class DurationTest extends TestCase
{
    /**
     * @return iterable<string, array{string, int}>
     */
    public static function validDurations(): iterable
    {
        yield '30 days' => ['30d', 30 * 86400];
        yield '12 hours' => ['12h', 12 * 3600];
        yield '90 minutes' => ['90m', 90 * 60];
        yield '45 seconds' => ['45s', 45];
        yield '2 weeks' => ['2w', 2 * 7 * 86400];
        yield 'single unit' => ['1d', 86400];
        yield 'zero is allowed' => ['0d', 0];
    }

    #[DataProvider('validDurations')]
    public function testParsesDurationToSeconds(string $input, int $expectedSeconds): void
    {
        self::assertSame($expectedSeconds, Duration::parseToSeconds($input));
    }

    /**
     * @return iterable<string, array{string}>
     */
    public static function invalidDurations(): iterable
    {
        yield 'missing unit' => ['30'];
        yield 'unknown unit' => ['30y'];
        yield 'negative' => ['-3d'];
        yield 'empty' => [''];
        yield 'unit only' => ['d'];
        yield 'decimal' => ['1.5d'];
        yield 'garbage' => ['soon'];
    }

    #[DataProvider('invalidDurations')]
    public function testRejectsInvalidDurations(string $input): void
    {
        $this->expectException(\InvalidArgumentException::class);
        Duration::parseToSeconds($input);
    }
}

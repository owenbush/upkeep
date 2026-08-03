<?php

declare(strict_types=1);

namespace Upkeep\Tests\Maintenance;

use PHPUnit\Framework\TestCase;
use Upkeep\Maintenance\ByteFormat;
use Upkeep\Maintenance\Category;

/**
 * How the disk inventory is rendered to the operator by `status --disk` and
 * `prune`: binary units at their boundaries, and one distinct label per
 * inventory category.
 */
final class DiskReportTest extends TestCase
{
    public function testHumanBytesSwitchUnitAtEachBinaryBoundary(): void
    {
        // `du` measures in binary units, so the rendering must too — the
        // interesting values are the boundaries, not each suffix in isolation.
        self::assertSame('0 B', ByteFormat::human(0));
        self::assertSame('1023 B', ByteFormat::human(1023));
        self::assertSame('1.0 KiB', ByteFormat::human(1024));
        self::assertSame('1024.0 KiB', ByteFormat::human(1024 ** 2 - 1));
        self::assertSame('1.0 MiB', ByteFormat::human(1024 ** 2));
        self::assertSame('1.0 GiB', ByteFormat::human(1024 ** 3));
        self::assertSame('1536.0 GiB', ByteFormat::human(1536 * 1024 ** 3));
    }

    public function testEveryInventoryCategoryHasItsOwnOperatorFacingLabel(): void
    {
        // `status --disk` groups its totals by these labels: a duplicated or
        // empty match arm would silently merge two categories into one row.
        $labels = array_map(static fn (Category $c): string => $c->label(), Category::cases());

        self::assertNotContains('', $labels);
        self::assertCount(\count(Category::cases()), array_unique($labels));
    }
}

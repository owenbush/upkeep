<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\TestCase;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Gitlab\NotFound;
use Upkeep\Workflow\WorkflowException;

/**
 * BP-CMD-14: these invariants used to be \assert()ed, which the production
 * php.ini default (zend.assertions=-1) does not compile at all — so a
 * violated invariant surfaced as a TypeError from inside the GitLab client,
 * on the path that performs merges. They are real guards now, and this test
 * only means anything because it runs whatever assertion mode is in force.
 */
final class DashboardRowGuardTest extends TestCase
{
    public function testAModuleFailureRowRefusesToProduceAMergeRequest(): void
    {
        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessage('no open merge request');

        self::failureRow()->requireMergeRequest();
    }

    public function testAModuleFailureRowRefusesToProduceAProject(): void
    {
        $this->expectException(WorkflowException::class);

        self::failureRow()->requireProject();
    }

    public function testAModuleFailureRowRefusesToProduceAVerdict(): void
    {
        $this->expectException(WorkflowException::class);

        self::failureRow()->requireVerdict();
    }

    /** The row still renders — the failure is a visible cell, not a crash. */
    public function testAModuleFailureRowStillRendersItsCells(): void
    {
        $cells = self::failureRow()->toTableCells();

        self::assertSame('widget', $cells[0]);
        self::assertStringContainsString('n/a', $cells[6]);
        self::assertFalse(self::failureRow()->isReadyAuto());
    }

    private static function failureRow(): DashboardRow
    {
        return DashboardRow::forModuleFailure('widget', new NotFound('https://git.drupalcode.org/project/widget'));
    }
}

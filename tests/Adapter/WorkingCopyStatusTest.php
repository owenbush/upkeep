<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\WorkingCopyStatus;

final class WorkingCopyStatusTest extends TestCase
{
    public function testCleanWorkingCopyIsNotDirty(): void
    {
        $status = new WorkingCopyStatus(false, false, false, 0, '1.0.x');
        self::assertFalse($status->isDirty());
        self::assertFalse($status->hasLocalWork());
        self::assertSame([], $status->describe());
    }

    public function testStagedChangesAreDirty(): void
    {
        $status = new WorkingCopyStatus(true, false, false, 0, '1.0.x');
        self::assertTrue($status->isDirty());
        self::assertTrue($status->hasLocalWork());
        self::assertContains('Staged changes not yet committed', $status->describe());
    }

    public function testUnstagedChangesAreDirty(): void
    {
        $status = new WorkingCopyStatus(false, true, false, 0, '1.0.x');
        self::assertTrue($status->isDirty());
        self::assertContains('Unstaged changes to tracked files', $status->describe());
    }

    public function testUntrackedFilesAreDirty(): void
    {
        $status = new WorkingCopyStatus(false, false, true, 0, '1.0.x');
        self::assertTrue($status->isDirty());
        self::assertContains('Untracked files not in .gitignore', $status->describe());
    }

    public function testUnpushedCommitsAreLocalWork(): void
    {
        $status = new WorkingCopyStatus(false, false, false, 3, '1.0.x');
        self::assertFalse($status->isDirty());
        self::assertTrue($status->hasLocalWork());
        self::assertContains('3 commit(s) ahead of origin (unpushed)', $status->describe());
    }

    public function testNoUpstreamIsLocalWork(): void
    {
        $status = new WorkingCopyStatus(false, false, false, -1, '1.0.x');
        self::assertFalse($status->isDirty());
        self::assertTrue($status->hasLocalWork());
        self::assertContains('No upstream tracking branch configured', $status->describe());
    }

    public function testDetachedHeadIsCustomBranch(): void
    {
        $status = new WorkingCopyStatus(false, false, false, 0, null);
        self::assertTrue($status->isOnCustomBranch());
        self::assertTrue($status->hasLocalWork());
        self::assertContains('HEAD is detached', $status->describe());
    }

    public function testMrBranchIsNotCustom(): void
    {
        $status = new WorkingCopyStatus(false, false, false, 0, 'mr-42');
        self::assertFalse($status->isOnCustomBranch());
        self::assertFalse($status->hasLocalWork());
    }

    public function testBaseBranchPatterns(): void
    {
        foreach (['1.0.x', '2.x', '11.x', '1.2.x'] as $branch) {
            $status = new WorkingCopyStatus(false, false, false, 0, $branch);
            self::assertFalse($status->isOnCustomBranch(), "Branch '$branch' should be recognized as a base branch");
        }
    }

    public function testFeatureBranchIsCustom(): void
    {
        foreach (['feature/new-widget', 'my-fix', 'main', 'develop'] as $branch) {
            $status = new WorkingCopyStatus(false, false, false, 0, $branch);
            self::assertTrue($status->isOnCustomBranch(), "Branch '$branch' should be recognized as a custom branch");
        }
    }

    public function testCustomBranchIsLocalWork(): void
    {
        $status = new WorkingCopyStatus(false, false, false, 0, 'feature/new-widget');
        self::assertFalse($status->isDirty());
        self::assertTrue($status->hasLocalWork());
        self::assertContains('On branch "feature/new-widget" (not a base or MR branch)', $status->describe());
    }

    public function testMultipleReasons(): void
    {
        $status = new WorkingCopyStatus(true, true, true, 5, 'feature/x');
        $reasons = $status->describe();
        self::assertCount(5, $reasons);
    }
}

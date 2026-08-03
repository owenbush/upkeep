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

    /**
     * inspect() reads three git commands and turns their output into the
     * struct above. The porcelain parse is the interesting half: the two
     * status columns mean different things, and `??` in column one is an
     * untracked file rather than a staged-and-unstaged one.
     */
    public function testInspectParsesPorcelainBranchAndAheadCount(): void
    {
        $runner = self::gitRunner([
            'status' => "M  staged.php\n M unstaged.php\n?? untracked.php\n\n",
            'symbolic-ref' => "mr-42\n",
            'rev-list' => "3\n",
        ]);

        $status = WorkingCopyStatus::inspect('/tmp/module', $runner);

        self::assertTrue($status->hasStagedChanges);
        self::assertTrue($status->hasUnstagedChanges);
        self::assertTrue($status->hasUntrackedFiles);
        self::assertSame(3, $status->commitsAhead);
        self::assertSame('mr-42', $status->currentBranch);
        self::assertTrue($runner->issued('git -C /tmp/module status --porcelain'));
    }

    public function testInspectReadsACleanWorkingCopyAsClean(): void
    {
        $status = WorkingCopyStatus::inspect('/tmp/module', self::gitRunner([
            'status' => '',
            'symbolic-ref' => "1.0.x\n",
            'rev-list' => "0\n",
        ]));

        self::assertFalse($status->isDirty());
        self::assertFalse($status->hasLocalWork());
        self::assertSame(0, $status->commitsAhead);
    }

    /**
     * A detached HEAD makes `symbolic-ref` fail and a missing upstream makes
     * `rev-list @{upstream}..HEAD` fail. Both are states the guards must read
     * as local work, so neither may be silently flattened into "clean".
     */
    public function testInspectReadsFailedProbesAsDetachedHeadAndNoUpstream(): void
    {
        $status = WorkingCopyStatus::inspect('/tmp/module', self::gitRunner([
            'status' => '',
            'symbolic-ref' => null,
            'rev-list' => null,
        ]));

        self::assertNull($status->currentBranch);
        self::assertSame(-1, $status->commitsAhead);
        self::assertTrue($status->hasLocalWork());
        self::assertContains('HEAD is detached', $status->describe());
        self::assertContains('No upstream tracking branch configured', $status->describe());
    }

    /**
     * `git status --porcelain` itself failing (not a repository, git absent)
     * must not be read as "no changes": the other two probes still answer.
     */
    public function testInspectToleratesAFailingPorcelainProbe(): void
    {
        $status = WorkingCopyStatus::inspect('/tmp/module', self::gitRunner([
            'status' => null,
            'symbolic-ref' => "2.x\n",
            'rev-list' => "0\n",
        ]));

        self::assertFalse($status->isDirty());
        self::assertSame('2.x', $status->currentBranch);
    }

    /**
     * @param array<string, ?string> $byVerb git sub-command => stdout, or null for a failing probe
     */
    private static function gitRunner(array $byVerb): ScriptedCommandRunner
    {
        return new ScriptedCommandRunner(static function (array $command) use ($byVerb): ?string {
            foreach ($byVerb as $verb => $output) {
                if (\in_array($verb, $command, true)) {
                    return $output;
                }
            }

            return '';
        });
    }
}

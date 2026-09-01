<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\IssueBranch;

/**
 * Starting and publishing a maintainer's own work.
 *
 * The distinguishing property from applyMr/applyPatch, and the whole reason
 * this is a separate operation: those two reset their branch on every call so
 * a contribution is tested alone. This one may hold the only copy of something
 * a human wrote, so it never resets, never forces, and never discards.
 */
final class DdevContribAdapterWorkTest extends DdevAdapterTestCase
{
    protected function setUp(): void
    {
        parent::setUp();

        mkdir($this->projectPath() . '/module', 0o700, true);
    }

    private function branch(): IssueBranch
    {
        return IssueBranch::forIssue(3223746, 'Fix the thing');
    }

    public function testStartingWorkBranchesFromTheCurrentBaseAndRecordsIt(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --verify' => null,
            'ls-remote' => null,
        ]);

        $resumed = $this->adapter($runner)->startWork($this->environment(), $this->branch());

        self::assertFalse($resumed, 'a new branch, not a resumption');
        self::assertTrue($runner->issued('checkout -b 3223746-fix-the-thing 1.0.x'));
        // Recorded so a later applyMr/applyPatch from this working copy knows
        // its origin, exactly as those paths record it for each other.
        self::assertTrue($runner->issued('config upkeep.base-branch 1.0.x'));
        self::assertTrue($runner->issued('composer require drupal/widget:dev-3223746-fix-the-thing'));
    }

    public function testAnExplicitBaseIsUsedInsteadOfTheWorkingCopysBranch(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'rev-parse --verify' => null,
            'ls-remote' => null,
        ]);

        $this->adapter($runner)->startWork($this->environment(), $this->branch(), '2.0.x');

        self::assertTrue($runner->issued('checkout -b 3223746-fix-the-thing 2.0.x'));
    }

    /**
     * The property that matters most: an existing branch is checked out as it
     * stands. `checkout -B`, which the disposable paths use, would silently
     * discard commits nobody else has a copy of.
     */
    public function testExistingWorkIsResumedAndNeverReset(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'rev-parse --verify' => "cccccccc\n",
        ]);

        $resumed = $this->adapter($runner)->startWork($this->environment(), $this->branch());

        self::assertTrue($resumed);
        self::assertTrue($runner->issued('checkout 3223746-fix-the-thing'));
        self::assertFalse($runner->issued('checkout -B'), 'resetting would destroy the work');
        self::assertFalse($runner->issued('checkout -b'), 'the branch already exists');
        self::assertTrue($this->loggedContaining('Resumed existing work branch'));
    }

    /**
     * Started on another machine, or in the environment for another core: the
     * branch is on origin but not here yet.
     */
    public function testWorkPushedElsewhereIsResumedFromOrigin(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'rev-parse --verify' => null,
            'ls-remote' => "cccccccc\trefs/heads/3223746-fix-the-thing\n",
        ]);

        $resumed = $this->adapter($runner)->startWork($this->environment(), $this->branch());

        self::assertTrue($resumed);
        self::assertTrue($runner->issued('fetch origin 3223746-fix-the-thing'));
        self::assertTrue($runner->issued('checkout -b 3223746-fix-the-thing FETCH_HEAD'));
        self::assertTrue($this->loggedContaining('from origin'));
    }

    public function testStartingWorkRefusesAnUncommittedWorkingCopy(): void
    {
        $runner = $this->engine([
            'status --porcelain' => "M  src/Widget.php\n",
            'symbolic-ref --short HEAD' => "1.0.x\n",
        ]);

        try {
            $this->adapter($runner)->startWork($this->environment(), $this->branch());
            self::fail('Expected the dirty working copy to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('Cannot start work on "3223746-fix-the-thing"', $e->getMessage());
            self::assertFalse($runner->issued('checkout -b'), 'nothing may be branched over local changes');
        }
    }

    // -------------------------------------------------------------- publish

    public function testPublishingPushesTheBranchAndReturnsItsHead(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "3223746-fix-the-thing\n",
            'rev-parse HEAD' => "ddddddddeeeeeeee\n",
        ]);

        $sha = $this->adapter($runner)->pushWork($this->environment(), $this->branch());

        self::assertSame('ddddddddeeeeeeee', $sha);
        self::assertTrue($runner->issued('push --set-upstream origin 3223746-fix-the-thing'));
        // A rejected push means the remote moved — a thing to look at, never
        // to overwrite.
        self::assertFalse($runner->issued('--force'));
        self::assertFalse($runner->issued('--force-with-lease'));
    }

    public function testPublishingRefusesWhenTheWorkingCopyIsOnAnotherBranch(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
        ]);

        try {
            $this->adapter($runner)->pushWork($this->environment(), $this->branch());
            self::fail('Expected the mismatch to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('is on "1.0.x", not "3223746-fix-the-thing"', $e->getMessage());
            self::assertFalse($runner->issued('push'), 'nothing may be pushed that nobody is looking at');
        }
    }

    public function testPublishingRefusesUncommittedWork(): void
    {
        $runner = $this->engine([
            'status --porcelain' => "M  src/Widget.php\n",
            'symbolic-ref --short HEAD' => "3223746-fix-the-thing\n",
        ]);

        try {
            $this->adapter($runner)->pushWork($this->environment(), $this->branch());
            self::fail('Expected uncommitted work to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('what is not committed cannot be pushed', $e->getMessage());
            self::assertFalse($runner->issued('push'));
        }
    }

    public function testPublishingRefusesADetachedHead(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => null,
        ]);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessageMatches('/detached HEAD/');
        $this->adapter($runner)->pushWork($this->environment(), $this->branch());
    }
}

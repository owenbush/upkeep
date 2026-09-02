<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\CapturedProcess;
use Upkeep\Adapter\GitRemote;
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

        $sha = $this->adapter($runner)->pushWork($this->environment(), $this->branch(), self::fork());

        self::assertSame('ddddddddeeeeeeee', $sha);
        self::assertTrue($runner->issued('push --set-upstream issue-3597857 3223746-fix-the-thing'));
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
            $this->adapter($runner)->pushWork($this->environment(), $this->branch(), self::fork());
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
            $this->adapter($runner)->pushWork($this->environment(), $this->branch(), self::fork());
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
        $this->adapter($runner)->pushWork($this->environment(), $this->branch(), self::fork());
    }

    // -------------------------------------------------------------- pushing

    /** The issue fork, with the SSH URL GitLab itself would have supplied. */
    private static function fork(): GitRemote
    {
        return GitRemote::issueFork(3597857, 'git@git.drupal.org:issue/widget-3597857.git');
    }

    /**
     * The branch goes to the issue fork, not to origin.
     *
     * This is how contributing to Drupal works and what publish was built
     * without: measured on pathauto, 100 of 100 open merge requests come from
     * a fork and none from the project itself. Origin is left exactly as
     * cloned, so fetch stays anonymous and checking work needs no key.
     */
    public function testTheBranchIsPushedToTheGivenRemoteAndOriginIsUntouched(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "3597857-fix\n",
            'remote get-url --push issue-3597857' => null,
            'rev-parse HEAD' => "ddddddd\n",
        ]);

        $sha = $this->adapter($runner)->pushWork(
            $this->environment(),
            IssueBranch::named(3597857, '3597857-fix'),
            self::fork(),
        );

        self::assertSame('ddddddd', $sha);
        self::assertTrue($runner->issued(
            'remote add issue-3597857 git@git.drupal.org:issue/widget-3597857.git',
        ));
        self::assertTrue($runner->issued('push --set-upstream issue-3597857 3597857-fix'));

        foreach ($runner->commandLines() as $line) {
            self::assertStringNotContainsString('remote set-url origin', $line, 'origin must not be altered');
            self::assertStringNotContainsString('push --set-upstream origin', $line);
        }
    }

    /**
     * A remote left over from a previous run pointing somewhere else is
     * re-pointed rather than trusted: an environment is reused across issues,
     * and a stale URL would send the push to the wrong fork.
     */
    public function testAnExistingRemoteWithADifferentUrlIsRepointed(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "3597857-fix\n",
            'remote get-url --push issue-3597857' => "git@git.drupal.org:issue/widget-9999999.git\n",
            'rev-parse HEAD' => "ddddddd\n",
        ]);

        $this->adapter($runner)->pushWork(
            $this->environment(),
            IssueBranch::named(3597857, '3597857-fix'),
            self::fork(),
        );

        self::assertTrue($runner->issued(
            'remote set-url issue-3597857 git@git.drupal.org:issue/widget-3597857.git',
        ));
    }

    /** Already correct: nothing is added and nothing is re-pointed. */
    public function testARemoteAlreadyPointingAtTheForkIsLeftAlone(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "3597857-fix\n",
            'remote get-url --push issue-3597857' => "git@git.drupal.org:issue/widget-3597857.git\n",
            'rev-parse HEAD' => "ddddddd\n",
        ]);

        $this->adapter($runner)->pushWork(
            $this->environment(),
            IssueBranch::named(3597857, '3597857-fix'),
            self::fork(),
        );

        foreach ($runner->commandLines() as $line) {
            self::assertStringNotContainsString('remote add', $line);
            self::assertStringNotContainsString('remote set-url', $line);
        }
    }

    /**
     * git's own message for a refused push suggests a password, which is the
     * one thing GitLab will never accept. The recovery that works is named
     * instead — against the host actually in the remote, since the SSH host
     * (git.drupal.org) is not the one the API and the web are served from.
     */
    public function testAnAuthFailureNamesTheSshKeyRecoveryAndTheRightHost(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "3597857-fix\n",
            'remote get-url --push issue-3597857' => "git@git.drupal.org:issue/widget-3597857.git\n",
            'push --set-upstream' => new CapturedProcess(
                exitCode: 128,
                output: "remote: HTTP Basic: Access denied.\nfatal: Authentication failed\n",
                timedOut: false,
                durationSeconds: 0.1,
            ),
            'rev-parse HEAD' => "ddddddd\n",
        ]);

        try {
            $this->adapter($runner)->pushWork(
                $this->environment(),
                IssueBranch::named(3597857, '3597857-fix'),
                self::fork(),
            );
            self::fail('a refused push should raise');
        } catch (AdapterException $e) {
            // git's own output is kept — hiding what happened would be worse
            // than the unhelpful suggestion inside it.
            self::assertStringContainsString('Access denied', $e->getMessage());
            self::assertStringContainsString('this is your SSH key', $e->getMessage());
            self::assertStringContainsString('ssh -T git@git.drupal.org', $e->getMessage());
            self::assertStringContainsString('ssh_keys', $e->getMessage());
        }
    }

    /** A push refused for any other reason reports what git said, plainly. */
    public function testANonAuthPushFailureIsReportedAsItself(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "3597857-fix\n",
            'remote get-url --push issue-3597857' => "git@git.drupal.org:issue/widget-3597857.git\n",
            'push --set-upstream' => new CapturedProcess(
                exitCode: 1,
                output: "! [rejected] 3597857-fix -> 3597857-fix (fetch first)\n",
                timedOut: false,
                durationSeconds: 0.1,
            ),
            'rev-parse HEAD' => "ddddddd\n",
        ]);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('fetch first');
        $this->adapter($runner)->pushWork(
            $this->environment(),
            IssueBranch::named(3597857, '3597857-fix'),
            self::fork(),
        );
    }

    // ------------------------------------------------------ the base branch

    /**
     * What `publish` opens the merge request against. It is read back rather
     * than derived because a target is a *branch on the project* and nothing
     * outside the working copy knows which one the work belongs on — publish
     * used to default to the tracked core major, aiming every merge request at
     * a branch named after a version of Drupal.
     */
    public function testTheRecordedBaseBranchIsReadBackAndTrimmed(): void
    {
        $runner = $this->engine(['config --get upkeep.base-branch' => "2.0.x\n"]);

        self::assertSame('2.0.x', $this->adapter($runner)->recordedBaseBranch($this->environment()));
    }

    /** Nothing recorded is null, which callers must not turn into a guess. */
    public function testAnAbsentOrEmptyRecordIsNull(): void
    {
        self::assertNull(
            $this->adapter($this->engine(['config --get upkeep.base-branch' => null]))
                ->recordedBaseBranch($this->environment()),
        );
        self::assertNull(
            $this->adapter($this->engine(['config --get upkeep.base-branch' => "\n"]))
                ->recordedBaseBranch($this->environment()),
        );
    }
}

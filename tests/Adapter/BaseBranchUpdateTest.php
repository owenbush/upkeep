<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\BaseBranchUpdate;
use Upkeep\Adapter\BaseRefresh;
use Upkeep\Adapter\IssueBranch;

/**
 * Cutting from what origin has, rather than from a branch that stopped moving
 * on the day of the clone.
 *
 * The bug this closes, in full, because none of it is guessable from the code:
 * a module working copy was cloned once and never fetched again on any path
 * that cuts a branch, so its `2.0.x` was sixteen months old. drupal.org's CI
 * does not check your branch — it checks `refs/merge-requests/<iid>/merge`,
 * your work merged into the **current** tip. In between, the target had been
 * rewritten for Drupal 12: the module file moved to OOP hooks and lost a `use`
 * import. The patch only added a function, so git merged it without a
 * conflict, and in the merged file the new block was the only thing still
 * naming the imported class — with no import left, so it resolved to the
 * global namespace. Local check: green. CI: one line, one undefined class.
 *
 * The check was not wrong about the tree it was given. It was given the wrong
 * tree, and nothing said so.
 */
final class BaseBranchUpdateTest extends DdevAdapterTestCase
{
    private function branch(): IssueBranch
    {
        return IssueBranch::forIssue(3223746, 'Fix the thing');
    }

    // ------------------------------------------------------------ the wording

    /**
     * Silence when nothing moved. A line per run saying "up to date" is a line
     * people stop reading, and this message is worth reading precisely because
     * it appears only when something changed under them.
     */
    public function testAnAlreadyCurrentBaseSaysNothing(): void
    {
        self::assertNull(BaseBranchUpdate::describe('2.0.x', 0));
        self::assertNull(BaseBranchUpdate::describe('2.0.x', -1));
    }

    public function testAMovedBaseSaysHowFarAndWhyItMatters(): void
    {
        $one = (string) BaseBranchUpdate::describe('2.0.x', 1);
        self::assertStringContainsString('2.0.x was 1 commit behind origin', $one);

        $many = (string) BaseBranchUpdate::describe('2.0.x', 47);
        self::assertStringContainsString('47 commits behind', $many);
        // The half a maintainer cannot infer: why a stale base changes a
        // verdict at all.
        self::assertStringContainsString('merged into this', $many);
    }

    public function testTheCutPointFollowsTheMode(): void
    {
        self::assertSame('FETCH_HEAD', BaseBranchUpdate::cutPoint('2.0.x', BaseRefresh::Update));
        self::assertSame('2.0.x', BaseBranchUpdate::cutPoint('2.0.x', BaseRefresh::Skip));
    }

    public function testSkippingSaysWhatTheVerdictWillAndWillNotCover(): void
    {
        $message = BaseBranchUpdate::skipped('2.0.x');

        self::assertStringContainsString('--no-update', $message);
        self::assertStringContainsString('may not be what CI merges into', $message);
    }

    /** The flag is the only route to Skip, so the default cannot be reached by accident. */
    public function testOnlyTheFlagProducesSkip(): void
    {
        self::assertSame(BaseRefresh::Skip, BaseRefresh::fromNoUpdateFlag(true));
        self::assertSame(BaseRefresh::Update, BaseRefresh::fromNoUpdateFlag(false));
    }

    public function testAnUnreachableOriginNamesTheEscapeHatch(): void
    {
        $refusal = BaseBranchUpdate::unreachable('2.0.x', "fatal: unable to access 'https://...'");

        self::assertInstanceOf(AdapterException::class, $refusal);
        self::assertStringContainsString('2.0.x', $refusal->getMessage());
        self::assertStringContainsString("fatal: unable to access", $refusal->getMessage());
        self::assertStringContainsString('--no-update', $refusal->getMessage());
    }

    /** git saying nothing at all still reads as something. */
    public function testAnUnreachableOriginWithNoOutputStillExplainsItself(): void
    {
        $refusal = BaseBranchUpdate::unreachable('2.0.x', "  \n ");

        self::assertStringContainsString('(no output from git)', $refusal->getMessage());
    }

    // --------------------------------------------------------- through the adapter

    /**
     * The default, end to end: fetch, then cut from what came back.
     */
    public function testTheBaseIsFetchedAndTheBranchCutFromWhatCameBack(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "2.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --verify' => null,
            'ls-remote' => null,
            'rev-list --count' => "47\n",
        ]);

        $this->adapter($runner)->startWork($this->environment(), $this->branch());

        self::assertTrue($runner->issued('fetch origin 2.0.x'));
        self::assertTrue($runner->issued('checkout -b 3223746-fix-the-thing FETCH_HEAD'));
        // The local base is brought forward too, so the working copy a
        // maintainer looks at afterwards is not still behind.
        self::assertTrue($runner->issued('merge --ff-only FETCH_HEAD'));
        self::assertTrue($this->loggedContaining('2.0.x was 47 commits behind origin'));
    }

    /** Nothing moved: no merge, and nothing said about it. */
    public function testACurrentBaseIsNotMergedAndNotAnnounced(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "2.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --verify' => null,
            'ls-remote' => null,
            'rev-list --count' => "0\n",
        ]);

        $this->adapter($runner)->startWork($this->environment(), $this->branch());

        self::assertTrue($runner->issued('fetch origin 2.0.x'));
        self::assertFalse($runner->issued('merge --ff-only'));
        self::assertFalse($this->loggedContaining('behind origin'));
    }

    /**
     * A base carrying local commits is left exactly as it is.
     *
     * Cutting from origin is still right — that is what CI merges into — but
     * the local commits are then not in the tree being checked, and that is a
     * thing to be told rather than have decided for you. Nothing here resets a
     * branch that may hold the only copy of somebody's work.
     */
    public function testALocallyDivergedBaseIsNeverResetAndIsReported(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "2.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --verify' => null,
            'ls-remote' => null,
            'rev-list --count' => "3\n",
            'merge --ff-only' => null,
        ]);

        $this->adapter($runner)->startWork($this->environment(), $this->branch());

        self::assertTrue($runner->issued('checkout -b 3223746-fix-the-thing FETCH_HEAD'));
        self::assertTrue($this->loggedContaining('has commits origin does not'));
        self::assertFalse($runner->issued('reset --hard FETCH_HEAD'), 'never resets somebody\'s branch');
    }

    /**
     * Asked to update and could not: a refusal, not a warning.
     *
     * Everything else in this tool degrades and says so. Not this one: the
     * degraded result here is a verdict indistinguishable from a good one,
     * which gets cached as evidence the fast-lane gate reads. Exit instead,
     * and name the flag that makes it a choice.
     */
    public function testAFailedFetchRefusesRatherThanCheckingAgainstAStaleBase(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "2.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --verify' => null,
            'ls-remote' => null,
            'fetch origin 2.0.x' => null,
        ]);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('--no-update');

        $this->adapter($runner)->startWork($this->environment(), $this->branch());
    }

    /** Skip goes nowhere near the network, and says the verdict is narrower. */
    public function testSkipDoesNotFetchAndCutsFromTheLocalBase(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "2.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --verify' => null,
            'ls-remote' => null,
        ]);

        $this->adapter($runner)->startWork($this->environment(), $this->branch(), null, BaseRefresh::Skip);

        self::assertFalse($runner->issued('fetch origin 2.0.x'));
        self::assertTrue($runner->issued('checkout -b 3223746-fix-the-thing 2.0.x'));
        self::assertTrue($this->loggedContaining('Not updating 2.0.x'));
    }

    /**
     * Resuming an existing work branch touches nothing.
     *
     * The refresh belongs to cutting a *new* branch. A branch already in
     * progress may hold the only copy of something; fetching under it, or
     * moving it, is not this operation's business.
     */
    public function testResumingAnExistingBranchDoesNotTouchTheBase(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'rev-parse --verify' => "aaaaaaa\n",
        ]);

        $resumed = $this->adapter($runner)->startWork($this->environment(), $this->branch());

        self::assertTrue($resumed);
        self::assertFalse($runner->issued('fetch origin'));
        self::assertFalse($runner->issued('merge --ff-only'));
    }
}

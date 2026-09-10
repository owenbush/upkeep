<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\MrCheckout;

#[CoversClass(MrCheckout::class)]
final class MrCheckoutTest extends TestCase
{
    public function testFetchRefspecCarriesWhicheverRefWasChosen(): void
    {
        // Force-refspec (+...) so re-applying an MR after it moved updates the
        // local branch instead of failing non-fast-forward. The merge ref moves
        // for a second reason the head ref does not: GitLab recomputes it when
        // the *target* gains a commit.
        self::assertSame(
            '+refs/merge-requests/2/merge:mr-2',
            MrCheckout::fetchRefspec(MrCheckout::mergeRef(2), 2),
        );
        self::assertSame(
            '+refs/merge-requests/147/head:mr-147',
            MrCheckout::fetchRefspec(MrCheckout::headRef(147), 147),
        );
    }

    /**
     * The merge ref wins, and that is the correction.
     *
     * `/head` is the contributor's branch; `/merge` is that branch merged into
     * the *current* tip of the target, which is what CI analyses. Measured on
     * pathauto: 23 of 25 open merge requests have a merge tree that differs
     * from their head tree, with branches 7 to 41 commits behind. Checking the
     * head meant agreeing with CI by luck.
     */
    public function testTheMergeRefIsPreferredOverTheHeadRef(): void
    {
        $both = ['refs/merge-requests/4/head', 'refs/merge-requests/4/merge'];

        self::assertSame('refs/merge-requests/4/merge', MrCheckout::preferredRef($both, 4));
        self::assertSame('refs/merge-requests/4/merge', MrCheckout::preferredRef(array_reverse($both), 4));
    }

    /**
     * No merge ref means GitLab could not merge the branch into its target —
     * normally a conflict. The branch is still checkable, but the fallback is
     * announced, because a branch-only verdict is not the one CI would give.
     */
    public function testOnlyAHeadRefFallsBackToItAndIsWorthSaying(): void
    {
        self::assertSame(
            'refs/merge-requests/4/head',
            MrCheckout::preferredRef(['refs/merge-requests/4/head'], 4),
        );

        $warning = MrCheckout::noMergeRefWarning(4);
        self::assertStringContainsString('conflict', $warning);
        self::assertStringContainsString('not what CI runs', $warning);
    }

    /** Another merge request's refs are not this one's. */
    public function testRefsBelongingToADifferentMergeRequestAreNotAccepted(): void
    {
        self::assertNull(MrCheckout::preferredRef(
            ['refs/merge-requests/5/merge', 'refs/merge-requests/5/head'],
            4,
        ));
        self::assertNull(MrCheckout::preferredRef([], 4));
    }

    public function testNoRefsAtAllIsARefusalNamingBoth(): void
    {
        $refusal = MrCheckout::noRefsAtAll(4);

        self::assertStringContainsString('refs/merge-requests/4/merge', $refusal->getMessage());
        self::assertStringContainsString('refs/merge-requests/4/head', $refusal->getMessage());
    }

    public function testBranchNameIsDerivedFromTheIid(): void
    {
        self::assertSame('mr-2', MrCheckout::branchName(2));
    }

    /**
     * The record answers only what the working copy cannot.
     *
     * On a managed branch there is no other way to know what it was cut from,
     * so the record wins. On a real branch, that branch *is* the base and the
     * record is at best a description of some earlier state.
     */
    public function testTheRecordedBaseIsUsedOnlyWhenTheBranchCannotAnswer(): void
    {
        self::assertSame('2.0.x', MrCheckout::resolveBaseBranch('mr-2', '2.0.x'), 'managed: ask the record');
        self::assertSame('2.0.x', MrCheckout::resolveBaseBranch('patch-3559057', '2.0.x'));
        self::assertSame('2.0.x', MrCheckout::resolveBaseBranch(null, '2.0.x'), 'detached: ask the record');
    }

    /**
     * This assertion used to be the other way round, and it was wrong.
     *
     * A stale record was permanent once written: check a patch, which records
     * the issue's base as 1.0.x, then move the working copy to the branch a
     * merge request targets, and the check still refused on a 1.0.x base —
     * advising `upkeep dev <module> --branch=2.0.x`, which is exactly what had
     * just been run. The nightly full check hit it twice.
     *
     * Clearing the record on checkout would not fix it. The module working
     * copy is a real clone and `git checkout` in it is ordinary use, so the
     * record can go stale without upkeep ever being told; the reader has to be
     * right rather than the writer being diligent.
     */
    public function testARealBranchOutranksAStaleRecord(): void
    {
        self::assertSame('2.0.x', MrCheckout::resolveBaseBranch('2.0.x', '1.0.x'));
    }

    public function testCurrentBranchIsTheBaseWhenNothingIsRecorded(): void
    {
        self::assertSame('1.0.x', MrCheckout::resolveBaseBranch('1.0.x', null));
    }

    public function testBaseCannotBeResolvedFromAnMrBranchWithoutARecord(): void
    {
        $this->expectException(\Upkeep\Adapter\AdapterException::class);
        $this->expectExceptionMessageMatches('/mr-7/');
        MrCheckout::resolveBaseBranch('mr-7', null);
    }

    public function testBaseCannotBeResolvedFromADetachedHeadWithoutARecord(): void
    {
        $this->expectException(\Upkeep\Adapter\AdapterException::class);
        MrCheckout::resolveBaseBranch(null, null);
    }

    public function testNativeBaseValidationPassesWhenTargetMatchesBase(): void
    {
        MrCheckout::assertNativeBase($this->mergeRequest(targetBranch: '2.0.x'), '2.0.x', 'widget');
        $this->addToAssertionCount(1);
    }

    /**
     * The refusal names the command that fixes it.
     *
     * Not a nicety: a module whose default branch is not the branch its merge
     * requests target hits this on the very first `check`, and the nightly
     * full check hit it against a real module — field_visibility_conditions
     * clones on 1.0.x while both of its open MRs target 2.0.x. "Apply the MR
     * in an environment whose base is its target branch" is true and leaves
     * you to work out how; the how is one command, and every other refusal in
     * this tool ends in something you can paste.
     */
    public function testBackportIsRejectedAndSaysHowToGetTheRightBase(): void
    {
        // The only real-world shape: an MR targeting a branch the environment
        // does not have checked out as its base (e.g. fvc !2 targets 2.0.x,
        // environment base is 1.0.x) — out-of-scope backport testing.
        $this->expectException(\Upkeep\Adapter\AdapterException::class);
        $this->expectExceptionMessageMatches('/MR !2 targets branch "2\.0\.x".*branch "1\.0\.x".*[Bb]ackport/s');
        $this->expectExceptionMessageMatches('/upkeep dev widget --branch=2\.0\.x/');
        MrCheckout::assertNativeBase($this->mergeRequest(targetBranch: '2.0.x'), '1.0.x', 'widget');
    }

    private function mergeRequest(string $targetBranch): \Upkeep\Gitlab\MergeRequest
    {
        return new \Upkeep\Gitlab\MergeRequest(
            iid: 2,
            title: 'Draft: Automated Project Update Bot fixes',
            state: 'opened',
            authorUsername: 'Project-Update-Bot',
            authorId: 66574,
            sourceBranch: 'project-update-bot-only',
            targetBranch: $targetBranch,
            draft: true,
            detailedMergeStatus: 'draft_status',
            headSha: '6e77deef6897f4ac6549d26e66fe0ab3d300bf76',
            webUrl: 'https://git.drupalcode.org/project/field_visibility_conditions/-/merge_requests/2',
        );
    }
}

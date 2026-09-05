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

    public function testRecordedBaseBranchWinsOverTheCurrentBranch(): void
    {
        self::assertSame('2.0.x', MrCheckout::resolveBaseBranch('mr-2', '2.0.x'));
        self::assertSame('2.0.x', MrCheckout::resolveBaseBranch('1.0.x', '2.0.x'));
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
        MrCheckout::assertNativeBase($this->mergeRequest(targetBranch: '2.0.x'), '2.0.x');
        $this->addToAssertionCount(1);
    }

    public function testBackportIsRejectedWithAClearError(): void
    {
        // The only real-world shape: an MR targeting a branch the environment
        // does not have checked out as its base (e.g. fvc !2 targets 2.0.x,
        // environment base is 1.0.x) — out-of-scope backport testing.
        $this->expectException(\Upkeep\Adapter\AdapterException::class);
        $this->expectExceptionMessageMatches('/MR !2 targets branch "2\.0\.x".*branch "1\.0\.x".*[Bb]ackport/s');
        MrCheckout::assertNativeBase($this->mergeRequest(targetBranch: '2.0.x'), '1.0.x');
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

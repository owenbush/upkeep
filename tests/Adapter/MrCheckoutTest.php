<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\MrCheckout;

#[CoversClass(MrCheckout::class)]
final class MrCheckoutTest extends TestCase
{
    public function testFetchRefspecTargetsTheMergeRequestHeadRef(): void
    {
        // Force-refspec (+...) so re-applying an MR after it gained commits
        // updates the local branch instead of failing non-fast-forward.
        self::assertSame('+refs/merge-requests/2/head:mr-2', MrCheckout::fetchRefspec(2));
        self::assertSame('+refs/merge-requests/147/head:mr-147', MrCheckout::fetchRefspec(147));
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

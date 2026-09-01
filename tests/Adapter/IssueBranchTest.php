<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\IssueBranch;
use Upkeep\Adapter\ManagedBranch;
use Upkeep\Adapter\MrCheckout;
use Upkeep\Drupal\IssueStatus;

/**
 * The branch a maintainer's own work lives on, and the wall between it and the
 * disposable branches.
 *
 * That wall is the safety property: `mr-<iid>` and `patch-<nid>` are reset with
 * `checkout -B` on every apply, and pointing that at a work branch would
 * destroy commits that may exist nowhere else.
 */
final class IssueBranchTest extends TestCase
{
    /** @return iterable<string, array{string, string}> */
    public static function slugs(): iterable
    {
        yield 'an ordinary title' => [
            'Add an option to disable auto-updating',
            '3223746-add-an-option-to-disable-auto-updating',
        ];
        yield 'punctuation and case' => [
            '"Delete URL Alias" action does not work!',
            '3223746-delete-url-alias-action-does-not-work',
        ];
        yield 'truncated on a word boundary' => [
            'Memcache Transaction-Aware Issue After Upgrading To The Latest Release',
            '3223746-memcache-transaction-aware-issue-after',
        ];
        // The slug is cosmetic — everything that matters keys on the leading
        // node id — so a title that reduces to nothing still yields a branch.
        yield 'a title with nothing usable in it' => ['??? !!!', '3223746'];
        yield 'a non-latin title' => ['日本語のタイトル', '3223746'];
    }

    #[DataProvider('slugs')]
    public function testTheBranchFollowsTheIssueForkConvention(string $title, string $expected): void
    {
        self::assertSame($expected, IssueBranch::forIssue(3223746, $title)->name);
    }

    /**
     * The convention is not decoration: `<nid>-<slug>` is the shape
     * IssueReference already parses, so a branch started here is one
     * drupal.org, GitLab and this tool all recognise as belonging to the issue.
     */
    public function testTheBranchIsRecognisedAsAnIssueForkByTheToolsOwnParsing(): void
    {
        $branch = IssueBranch::forIssue(3223746, 'Fix the thing');

        self::assertSame(
            3223746,
            \Upkeep\Drupal\IssueReference::extractOwning('No issue in the title', $branch->name, null),
        );
    }

    public function testAnExplicitlyNamedBranchIsStillAnchoredToItsIssue(): void
    {
        $branch = IssueBranch::named(3223746, 'my-own-name');

        self::assertSame('my-own-name', $branch->name);
        self::assertSame(3223746, $branch->issueNid);
        self::assertTrue($branch->matches('my-own-name'));
    }

    /** Resuming yesterday's work means recognising it under any slug. */
    public function testABranchMatchesAnyBranchForTheSameIssue(): void
    {
        $branch = IssueBranch::forIssue(3223746, 'Fix the thing');

        self::assertTrue($branch->matches('3223746-fix-the-thing'));
        self::assertTrue($branch->matches('3223746-a-different-slug'), 'someone named it differently');
        self::assertTrue($branch->matches('3223746'));
        self::assertFalse($branch->matches('3223747-fix-the-thing'), 'a different issue');
        self::assertFalse($branch->matches('1.0.x'));
    }

    /** @return iterable<string, array{string, bool}> */
    public static function branchShapes(): iterable
    {
        yield 'an issue fork' => ['3223746-fix-the-thing', true];
        yield 'a bare issue number' => ['3223746', true];
        yield 'a release branch' => ['1.0.x', false];
        yield 'a managed MR branch' => ['mr-7', false];
        yield 'a managed patch branch' => ['patch-3223746', false];
        yield 'a short number' => ['42-something', false];
        yield 'a topic branch' => ['fix-the-thing', false];
    }

    /**
     * The two kinds are unable to be confused by construction: no managed
     * prefix begins with a digit, and a work branch always does.
     */
    #[DataProvider('branchShapes')]
    public function testWorkBranchesAndManagedBranchesAreDisjoint(string $branch, bool $isWork): void
    {
        self::assertSame($isWork, IssueBranch::isWorkBranch($branch));
        if ($isWork) {
            self::assertFalse(ManagedBranch::isManaged($branch), 'a work branch is never disposable');
        }
    }

    /**
     * The safety property this exists for: cutting mr-<iid> or patch-<nid>
     * from a maintainer's own work would test the contribution *plus* that
     * work, and report the result as a verdict on the contribution alone.
     */
    public function testAWorkBranchIsRefusedAsABaseForAContribution(): void
    {
        $this->expectException(AdapterException::class);
        $this->expectExceptionMessageMatches('/your own work branch "3223746-fix-the-thing"/');
        MrCheckout::resolveBaseBranch('3223746-fix-the-thing', null);
    }

    /** A recorded base still wins — that is how a re-apply knows its origin. */
    public function testARecordedBaseStillResolvesFromAWorkBranch(): void
    {
        self::assertSame('1.0.x', MrCheckout::resolveBaseBranch('3223746-fix-the-thing', '1.0.x'));
    }

    // ------------------------------------------------------------- statuses

    /**
     * The scan used to be Needs Review + RTBC — the two statuses a
     * *contribution* sits in — which made the tool blind to most of a real
     * queue. Measured on pathauto: 42 of 93 open issues.
     */
    public function testEveryLiveStatusIsOpenAndNoResolvedOneIs(): void
    {
        $open = IssueStatus::open();

        self::assertContains(IssueStatus::Active, $open);
        self::assertContains(IssueStatus::NeedsWork, $open);
        self::assertContains(IssueStatus::Postponed, $open);
        self::assertContains(IssueStatus::PostponedNeedsInfo, $open);
        self::assertContains(IssueStatus::NeedsReview, $open);
        self::assertContains(IssueStatus::Rtbc, $open);
        self::assertContains(IssueStatus::PatchToBePorted, $open);

        $resolvedStatuses = [
            IssueStatus::Fixed,
            IssueStatus::ClosedFixed,
            IssueStatus::ClosedDuplicate,
            IssueStatus::ClosedWontFix,
            IssueStatus::ClosedWorksAsDesigned,
            IssueStatus::ClosedOutdated,
            IssueStatus::ClosedCannotReproduce,
        ];
        foreach ($resolvedStatuses as $resolved) {
            self::assertNotContains($resolved, $open, $resolved->label() . ' is not open');
            self::assertFalse($resolved->isOpen());
        }
    }

    /** The historical pair is kept, because it is still a distinct question. */
    public function testAwaitingReviewIsStillTheContributionShapedPair(): void
    {
        self::assertSame([IssueStatus::NeedsReview, IssueStatus::Rtbc], IssueStatus::awaitingReview());
    }

    /**
     * "Needs a maintainer" is narrower than "open": Active is unclaimed, and
     * Needs work and the postponed statuses are waiting on somebody else.
     */
    public function testOnlyReviewAndRtbcPutTheBallWithTheMaintainer(): void
    {
        self::assertTrue(IssueStatus::NeedsReview->needsMaintainer());
        self::assertTrue(IssueStatus::Rtbc->needsMaintainer());
        self::assertFalse(IssueStatus::Active->needsMaintainer());
        self::assertFalse(IssueStatus::NeedsWork->needsMaintainer());
        self::assertFalse(IssueStatus::Postponed->needsMaintainer());
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Patches;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueFile;
use Upkeep\Drupal\IssueStatus;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Patches\Contribution;
use Upkeep\Patches\ContributionKind;

final class ContributionTest extends TestCase
{
    /** @param list<IssueFile> $files */
    private static function issue(array $files = []): Issue
    {
        return new Issue(
            nid: 3489012,
            title: 'Add config schema for settings form',
            status: IssueStatus::NeedsReview,
            url: 'https://www.drupal.org/node/3489012',
            project: 'widget',
            priority: 200,
            version: '2.0.x-dev',
            component: 'Code',
            category: 'Task',
            files: $files,
        );
    }

    private static function patch(): IssueFile
    {
        return new IssueFile('3489012-12-config-schema.patch', 'https://example.test/p.patch', 3072, 1705400000);
    }

    /**
     * $base === $head is an empty MR; differing shas carry changes; null
     * leaves it unknown (a list payload, or a snapshot from before diff refs
     * were cached).
     */
    private static function mr(int $iid, ?string $base, ?string $head): MergeRequest
    {
        return new MergeRequest(
            iid: $iid,
            title: 'Issue #3489012: do the thing',
            state: 'opened',
            authorUsername: 'alice',
            authorId: 100,
            sourceBranch: '3489012-do-the-thing',
            targetBranch: '2.0.x',
            draft: false,
            detailedMergeStatus: null,
            headSha: $head,
            webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/' . $iid,
            diffBaseSha: $base,
            diffHeadSha: $head,
        );
    }

    private static function substantiveMr(int $iid = 7): MergeRequest
    {
        return self::mr($iid, 'base111', 'head222');
    }

    private static function emptyMr(int $iid = 1): MergeRequest
    {
        return self::mr($iid, 'same333', 'same333');
    }

    private static function unknownMr(int $iid = 9): MergeRequest
    {
        return self::mr($iid, null, null);
    }

    /** @return iterable<string, array{list<IssueFile>, list<MergeRequest>, ContributionKind}> */
    public static function classificationCases(): iterable
    {
        yield 'a patch and nothing else' => [
            [self::patch()], [], ContributionKind::PatchOnly,
        ];

        yield 'a patch beside a branch that carries changes' => [
            [self::patch()], [self::substantiveMr()], ContributionKind::PatchAndMergeRequest,
        ];

        yield 'a patch beside an empty merge request' => [
            [self::patch()], [self::emptyMr()], ContributionKind::PatchWithEmptyMergeRequest,
        ];

        yield 'a patch beside several merge requests, all empty' => [
            [self::patch()],
            [self::emptyMr(1), self::emptyMr(2)],
            ContributionKind::PatchWithEmptyMergeRequest,
        ];

        yield 'a patch beside one empty and one real merge request' => [
            [self::patch()],
            [self::emptyMr(1), self::substantiveMr()],
            ContributionKind::PatchAndMergeRequest,
        ];

        yield 'no patch, work on a branch' => [
            [], [self::substantiveMr()], ContributionKind::MergeRequestOnly,
        ];

        yield 'no patch, and the only merge request is empty' => [
            [], [self::emptyMr()], ContributionKind::Nothing,
        ];

        yield 'no patch and no merge request' => [
            [], [], ContributionKind::Nothing,
        ];

        // Unknown emptiness reads as real work: a list payload omits diff refs
        // and so does a snapshot written before they were cached, and neither
        // absence is evidence that a branch is empty.
        yield 'a patch beside a merge request of unknown emptiness' => [
            [self::patch()], [self::unknownMr()], ContributionKind::PatchAndMergeRequest,
        ];

        yield 'no patch, and a merge request of unknown emptiness' => [
            [], [self::unknownMr()], ContributionKind::MergeRequestOnly,
        ];

        // A non-patch attachment is not a contribution: an interdiff, a
        // screenshot or a test-only file leaves the issue with nothing to
        // apply.
        yield 'a screenshot is not a patch' => [
            [new IssueFile('before.png', 'https://example.test/before.png', 900, 1705400000)],
            [],
            ContributionKind::Nothing,
        ];
    }

    /**
     * @param list<IssueFile>    $files
     * @param list<MergeRequest> $mrs
     */
    #[DataProvider('classificationCases')]
    public function testClassifiesHowTheWorkArrived(array $files, array $mrs, ContributionKind $expected): void
    {
        $contribution = new Contribution('widget', self::issue($files), $mrs);

        self::assertSame($expected, $contribution->kind());
    }

    /**
     * Only one kind is the dashboard's business rather than this command's.
     */
    public function testOnlyMergeRequestOnlyWorkIsCoveredByTheDashboard(): void
    {
        foreach (ContributionKind::cases() as $kind) {
            self::assertSame(
                $kind === ContributionKind::MergeRequestOnly,
                $kind->isCoveredByMergeRequest(),
                $kind->name,
            );
        }
    }

    public function testKindsCarryingARealBranchAreTheTwoWithOne(): void
    {
        self::assertTrue(ContributionKind::PatchAndMergeRequest->hasSubstantiveMergeRequest());
        self::assertTrue(ContributionKind::MergeRequestOnly->hasSubstantiveMergeRequest());
        self::assertFalse(ContributionKind::PatchOnly->hasSubstantiveMergeRequest());
        self::assertFalse(ContributionKind::PatchWithEmptyMergeRequest->hasSubstantiveMergeRequest());
        self::assertFalse(ContributionKind::Nothing->hasSubstantiveMergeRequest());
    }

    /**
     * The withheld kind is the one kind that never reaches the summary line,
     * because it never reaches the table either.
     */
    public function testEveryKindTheTableCanShowHasASummaryLabel(): void
    {
        foreach (ContributionKind::cases() as $kind) {
            self::assertSame(
                !$kind->isCoveredByMergeRequest(),
                $kind->summaryLabel() !== null,
                $kind->name,
            );
        }
    }

    /** @return iterable<string, array{list<MergeRequest>, string}> */
    public static function mergeRequestCellCases(): iterable
    {
        yield 'no merge request' => [[], '–'];
        yield 'one that carries changes' => [[self::substantiveMr()], '!7'];
        yield 'one that carries nothing' => [[self::emptyMr()], '!1 empty'];
        yield 'one of unknown emptiness is not flagged' => [[self::unknownMr()], '!9'];

        // The substantive one represents the row wherever it is in the list,
        // so a real branch is never reported as empty because a bot draft was
        // listed first.
        yield 'an empty one listed before a real one' => [
            [self::emptyMr(1), self::substantiveMr()], '!7 +1',
        ];
        yield 'a real one listed before an empty one' => [
            [self::substantiveMr(), self::emptyMr(1)], '!7 +1',
        ];
        yield 'two empty ones' => [[self::emptyMr(1), self::emptyMr(2)], '!1 empty +1'];
    }

    /**
     * @param list<MergeRequest> $mrs
     */
    #[DataProvider('mergeRequestCellCases')]
    public function testTheMergeRequestCellNamesTheRepresentativeBranch(array $mrs, string $expected): void
    {
        $contribution = new Contribution('widget', self::issue([self::patch()]), $mrs);

        self::assertSame($expected, $contribution->mergeRequestCell());
    }

    public function testSubstantiveMergeRequestsDropsOnlyTheProvablyEmptyOnes(): void
    {
        $contribution = new Contribution('widget', self::issue(), [
            self::emptyMr(1),
            self::substantiveMr(7),
            self::unknownMr(9),
        ]);

        self::assertSame(
            [7, 9],
            array_map(
                static fn (MergeRequest $mr): int => $mr->iid,
                $contribution->substantiveMergeRequests(),
            ),
        );
    }
}

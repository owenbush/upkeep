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
    private static function issue(array $files = [], int $nid = 3489012): Issue
    {
        return new Issue(
            nid: $nid,
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
    private static function mr(
        int $iid,
        ?string $base = null,
        ?string $head = null,
        string $state = 'opened',
        ?string $mergedAt = null,
        ?string $updatedAt = null,
        string $title = 'Issue #3489012: do the thing',
    ): MergeRequest {
        return new MergeRequest(
            iid: $iid,
            title: $title,
            state: $state,
            authorUsername: 'alice',
            authorId: 100,
            sourceBranch: '3489012-do-the-thing',
            targetBranch: '2.0.x',
            draft: false,
            detailedMergeStatus: null,
            headSha: $head,
            webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/' . $iid,
            updatedAt: $updatedAt,
            diffBaseSha: $base,
            diffHeadSha: $head,
            mergedAt: $mergedAt,
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

    // ------------------------------------------------ has this already landed

    /**
     * The question a Project Update Bot compatibility issue cannot answer
     * itself. Convention keeps those open so the bot can post again as core
     * moves, so an open one may have had its work merged months ago — and two
     * such issues look identical until you ask whether anything was merged.
     *
     * Verified against live data: conditions_helper #3596502 is "active" with
     * its MR merged 2026-06-12, while field_visibility_conditions #3598272 is
     * "needs review" with an open draft. Opposite states, same appearance.
     */
    public function testAMergedMergeRequestIsReportedAsLanded(): void
    {
        $contribution = new Contribution('widget', self::issue([]), [
            self::mr(1, state: 'merged', mergedAt: '2026-06-12T19:48:54.079Z'),
        ]);

        self::assertSame('!1 merged 2026-06-12', $contribution->mergeRequestCell());
        self::assertFalse($contribution->hasWorkNewerThanLanding());
    }

    /**
     * A bot that posts again after its work merged has raised new work. This
     * is why the cell never says "resolved": both readings are statements
     * about evidence, and which one warrants closing stays the maintainer's.
     */
    public function testAPatchPostedAfterTheMergeIsNewerWork(): void
    {
        $afterTheMerge = strtotime('2026-07-01T00:00:00Z');

        $contribution = new Contribution(
            'widget',
            self::issue([new IssueFile('reroll.patch', 'https://example.test/reroll.patch', 10, $afterTheMerge)]),
            [self::mr(1, state: 'merged', mergedAt: '2026-06-12T19:48:54.079Z')],
        );

        self::assertTrue($contribution->hasWorkNewerThanLanding());
        self::assertStringContainsString('newer work since', $contribution->mergeRequestCell());
    }

    /** An open MR touched after the merge is newer work too. */
    public function testAnOpenMergeRequestUpdatedAfterTheMergeIsNewerWork(): void
    {
        $contribution = new Contribution('widget', self::issue([]), [
            self::mr(1, state: 'merged', mergedAt: '2026-06-12T19:48:54.079Z'),
            self::mr(2, state: 'opened', updatedAt: '2026-07-02T10:00:00Z'),
        ]);

        self::assertTrue($contribution->hasWorkNewerThanLanding());
    }

    /** The most recent landing is the one reported. */
    public function testTheLatestMergeIsTheOneReported(): void
    {
        $contribution = new Contribution('widget', self::issue([]), [
            self::mr(1, state: 'merged', mergedAt: '2025-01-01T00:00:00Z'),
            self::mr(9, state: 'merged', mergedAt: '2026-06-12T19:48:54.079Z'),
        ]);

        self::assertSame(9, $contribution->landed()?->iid);
    }

    /** Nothing merged is nothing landed, and the cell reads as it always did. */
    public function testAnIssueWithNothingMergedHasNotLanded(): void
    {
        $contribution = new Contribution('widget', self::issue([]), [self::mr(2, state: 'opened')]);

        self::assertNull($contribution->landed());
        self::assertFalse($contribution->hasWorkNewerThanLanding());
        self::assertStringNotContainsString('merged', $contribution->mergeRequestCell());
    }

    /** A merge with no timestamp cannot be compared against, so nothing is newer. */
    public function testAMergeWithNoTimestampClaimsNoNewerWork(): void
    {
        $contribution = new Contribution('widget', self::issue([]), [self::mr(1, state: 'merged')]);

        self::assertFalse($contribution->hasWorkNewerThanLanding());
    }

    public function testAnUnparseableMergeTimestampClaimsNoNewerWork(): void
    {
        $contribution = new Contribution('widget', self::issue([]), [
            self::mr(1, state: 'merged', mergedAt: 'not a date at all'),
        ]);

        self::assertFalse($contribution->hasWorkNewerThanLanding());
    }

    // ------------------------------------------------- pairing via the fork

    /**
     * The only thing that pairs a Project Update Bot merge request to its
     * issue. Live: the MR is titled "Automated Project Update Bot fixes" on a
     * branch called project-update-bot-only, and its description says only
     * "Relates to #NNN" — which extractOwning() rejects on purpose so the bot
     * cannot suppress an issue's patches by mentioning it. Correct, and it
     * left every bot MR paired to nothing.
     *
     * The fork path is a fact about how the repository came to exist:
     * drupal.org made issue/<module>-<nid> *for* that issue.
     */
    public function testABotMergeRequestPairsThroughItsIssueFork(): void
    {
        $bot = new MergeRequest(
            iid: 1,
            title: 'Automated Project Update Bot fixes',
            state: 'merged',
            authorUsername: 'project update bot',
            authorId: 3644742,
            sourceBranch: 'project-update-bot-only',
            targetBranch: '1.0.x',
            draft: false,
            detailedMergeStatus: null,
            headSha: null,
            webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/1',
            description: 'Relates to #3596502. This merge request was automatically created by the bot.',
            sourceProjectId: 216803,
            mergedAt: '2026-06-12T19:48:54.079Z',
        );
        $issue = self::issue([], nid: 3596502);

        // Without the fork map it pairs to nothing at all.
        self::assertSame([], Contribution::pair('widget', [$issue], [$bot])[0]->mergeRequests);

        $paired = Contribution::pair('widget', [$issue], [$bot], [216803 => 3596502]);

        self::assertCount(1, $paired[0]->mergeRequests);
        self::assertSame('!1 merged 2026-06-12', $paired[0]->mergeRequestCell());
    }

    /** A fork map that says nothing about an MR leaves the old rules in charge. */
    public function testTheOldRulesStillApplyWhereTheForkMapIsSilent(): void
    {
        $mr = self::mr(7, title: 'Issue #3467675: Make URL field required');

        $paired = Contribution::pair('widget', [self::issue([], nid: 3467675)], [$mr], [999 => 111111]);

        self::assertCount(1, $paired[0]->mergeRequests);
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Dashboard\Guidance;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueFile;
use Upkeep\Drupal\IssueStatus;
use Upkeep\Gate\GateStatus;
use Upkeep\Gate\GateVerdict;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\NotFound;
use Upkeep\Gitlab\Project;
use Upkeep\Patches\Contribution;
use Upkeep\Patches\PatchRevision;
use Upkeep\Results\CachedResult;

/**
 * What a row says it is, and what it tells you to run.
 *
 * The ranking is the design and is what these pin. A row usually carries
 * several gate reasons and only one phrase can be shown, so they are ordered by
 * what actually blocks progress — not by the order the gate happened to record
 * them. Get that wrong and a maintainer is told to check something that has
 * merge conflicts.
 */
final class GuidanceTest extends TestCase
{
    private const HEAD = 'abc123def456abc123def456abc123def456abcd';

    private static function project(): Project
    {
        return Project::fromApi([
            'id' => 42,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ]);
    }

    /**
     * @param list<string> $reasons
     */
    private static function mrRow(GateStatus $status, array $reasons, string $core = '11'): DashboardRow
    {
        return DashboardRow::forMergeRequest(
            'widget',
            $core,
            self::project(),
            new MergeRequest(
                iid: 5,
                title: 'Issue #1: a change',
                state: 'opened',
                authorUsername: 'alice',
                authorId: 1,
                sourceBranch: 'fix',
                targetBranch: '1.0.x',
                draft: false,
                detailedMergeStatus: null,
                headSha: self::HEAD,
                webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/5',
            ),
            null,
            new GateVerdict($status, $reasons),
        );
    }

    /**
     * @return iterable<string, array{GateStatus, list<string>, string, string}>
     */
    public static function mergeRequestCases(): iterable
    {
        // The gate sets Blocked from red CI and nothing else — it never
        // inspects mergeability — so this is "CI failed", not "conflicts".
        // Calling it conflicts would have asked for a rebase nobody needed.
        //
        // And it still names a command: a red pipeline is precisely when a
        // maintainer wants the branch locally to reproduce the failure.
        yield 'blocked means red CI, and you can still pull it down' => [
            GateStatus::Blocked,
            ['ci-red', 'local-missing', 'not-bot-author'],
            'CI failed',
            'upkeep check widget 5 --version=11',
        ];

        yield 'ready to merge' => [
            GateStatus::ReadyAuto,
            [],
            'ready to merge',
            'upkeep merge --fast-lane',
        ];

        // Your own evidence that the work is wrong — worth telling the
        // contributor, which is what needs-work does.
        yield 'a failing check names it and points at needs-work' => [
            GateStatus::Review,
            ['not-bot-author', 'local-failed:phpunit'],
            'phpunit failed',
            'upkeep needs-work widget 5',
        ];

        yield 'several failing checks are counted' => [
            GateStatus::Review,
            ['local-failed:phpunit', 'local-failed:phpcs'],
            '2 checks failed',
            'upkeep needs-work widget 5',
        ];

        // Failing checks outrank red CI: yours is the more specific evidence,
        // and it is the one you can act on.
        yield 'a failing check outranks red CI' => [
            GateStatus::Review,
            ['ci-red', 'local-failed:phpunit'],
            'phpunit failed',
            'upkeep needs-work widget 5',
        ];

        // Red CI changes what the row *is*, not what to do about it — the
        // command still comes from the evidence you hold.
        yield 'red CI with nothing checked locally says check it' => [
            GateStatus::Review,
            ['not-bot-author', 'ci-red', 'local-missing'],
            'CI failed',
            'upkeep check widget 5 --version=11',
        ];

        // Your checks and drupal.org's disagree, which is itself worth
        // looking at rather than re-running.
        yield 'red CI but locally green is a disagreement to look at' => [
            GateStatus::Review,
            ['not-bot-author', 'ci-red'],
            'CI failed, local green',
            'upkeep review widget 5',
        ];

        // A draft used to suggest nothing, on the grounds that its author had
        // said it was unfinished. But unfinished is frequently *abandoned* —
        // somebody started and could not carry on — and that is a thing for a
        // maintainer to pick up, not to wait on. So "draft" prefixes the
        // status and the command is the ordinary one.
        yield 'a draft is checkable, and says it is a draft' => [
            GateStatus::Review,
            ['draft', 'ci-missing', 'local-missing'],
            'draft, needs a check',
            'upkeep check widget 5 --version=11',
        ];

        yield 'a draft with red CI carries both facts' => [
            GateStatus::Review,
            ['draft', 'ci-red', 'local-missing'],
            'draft, CI failed',
            'upkeep check widget 5 --version=11',
        ];

        yield 'a draft already checked green is a change to look at' => [
            GateStatus::Review,
            ['draft', 'not-bot-author'],
            'draft, needs your review',
            'upkeep review widget 5',
        ];

        // The common case, and the one the old output buried under three
        // tokens including one that is on every human MR.
        yield 'the ordinary human MR needs a check' => [
            GateStatus::Review,
            ['not-bot-author', 'ci-missing', 'local-missing'],
            'needs a check',
            'upkeep check widget 5 --version=11',
        ];

        yield 'stale evidence says so rather than saying nothing' => [
            GateStatus::Review,
            ['not-bot-author', 'local-stale'],
            'checks are stale',
            'upkeep check widget 5 --version=11',
        ];

        // Checked, green, human-authored: the fast lane will never take it, so
        // somebody has to look at the change itself.
        yield 'checked and green but not a bot MR' => [
            GateStatus::Review,
            ['not-bot-author'],
            'needs your review',
            'upkeep review widget 5',
        ];
    }

    /**
     * @param list<string> $reasons
     */
    #[DataProvider('mergeRequestCases')]
    public function testTheMostActionableTrueThingIsWhatTheRowSays(
        GateStatus $status,
        array $reasons,
        string $expectedStatus,
        string $expectedCommand,
    ): void {
        $guidance = Guidance::forRow(self::mrRow($status, $reasons));

        self::assertSame($expectedStatus, $guidance->status);
        self::assertSame($expectedCommand, $guidance->command);
    }

    /**
     * The promise the NEXT column makes: there is no row this tool has nothing
     * to say about. Asserted over every combination of reasons the gate can
     * produce, so a future one cannot quietly reintroduce a dead row.
     */
    public function testEveryReasonCombinationYieldsSomethingToRun(): void
    {
        $reasons = ['not-bot-author', 'draft', 'ci-missing', 'ci-red', 'local-missing', 'local-stale'];

        // Every subset, which is what "any combination" means.
        for ($mask = 0; $mask < 2 ** \count($reasons); ++$mask) {
            $subset = [];
            foreach ($reasons as $bit => $reason) {
                if (($mask & (2 ** $bit)) !== 0) {
                    $subset[] = $reason;
                }
            }

            foreach ([GateStatus::Review, GateStatus::Blocked] as $status) {
                $guidance = Guidance::forRow(self::mrRow($status, $subset));
                self::assertNotSame('', $guidance->command, implode(',', $subset));
                self::assertNotSame('', $guidance->status, implode(',', $subset));
            }
        }
    }

    /**
     * A module tracking one core does not need telling which, and the command
     * is shorter for it.
     */
    public function testTheCoreIsNamedOnlyWhenTheRowHasOne(): void
    {
        $withCore = Guidance::forRow(self::mrRow(GateStatus::Review, ['local-missing'], '11'));
        self::assertSame('upkeep check widget 5 --version=11', $withCore->command);

        $withoutCore = Guidance::forRow(self::mrRow(GateStatus::Review, ['local-missing'], '-'));
        self::assertSame('upkeep check widget 5', $withoutCore->command);
    }

    /** A module whose MRs could not be listed is told to re-fetch. */
    public function testAFailedModuleIsToldToRefresh(): void
    {
        $guidance = Guidance::forRow(DashboardRow::forModuleFailure(
            'widget',
            new NotFound('https://git.drupalcode.org/project/widget'),
        ));

        self::assertSame('unavailable', $guidance->status);
        self::assertSame('upkeep dashboard --refresh=widget', $guidance->command);
    }

    // ---------------------------------------------------------------- patches

    /** @param list<IssueFile> $files */
    private static function patchRow(array $files, ?CachedResult $local = null): DashboardRow
    {
        return DashboardRow::forPatch('11', new Contribution('widget', new Issue(
            nid: 3597808,
            title: 'An issue',
            status: IssueStatus::NeedsReview,
            url: 'https://www.drupal.org/node/3597808',
            project: 'widget',
            priority: 200,
            version: '1.0.x-dev',
            component: 'Code',
            category: 'Task',
            files: $files,
        )), $local);
    }

    private static function file(string $url): IssueFile
    {
        return new IssueFile('p.patch', $url, 2048, 1705400000);
    }

    private static function cachedRun(string $sha, CheckStatus $status): CachedResult
    {
        return new CachedResult(
            $sha,
            new \DateTimeImmutable(),
            new CheckRunResult([new CheckResult(CheckType::PhpUnit, $status, 0, 'x', 1.0)]),
        );
    }

    public function testAnUncheckedPatchPointsAtCheckingIt(): void
    {
        $guidance = Guidance::forRow(self::patchRow([self::file('https://x.test/a.patch')]));

        self::assertSame('1 patch', $guidance->status);
        self::assertSame('upkeep patch:check widget 3597808 --version=11', $guidance->command);
    }

    public function testSeveralPatchesAreCounted(): void
    {
        $guidance = Guidance::forRow(self::patchRow([
            self::file('https://x.test/a.patch'),
            self::file('https://x.test/b.patch'),
        ]));

        self::assertSame('2 patches', $guidance->status);
    }

    /**
     * A green patch is one you might actually want to apply and look at, which
     * is a different verb from checking it again.
     */
    public function testACheckedPatchPointsAtApplyingIt(): void
    {
        $url = 'https://x.test/a.patch';
        $guidance = Guidance::forRow(self::patchRow(
            [self::file($url)],
            self::cachedRun(PatchRevision::of($url), CheckStatus::Passed),
        ));

        self::assertSame('1 patch, checked', $guidance->status);
        self::assertSame('upkeep patch:apply widget 3597808', $guidance->command);
    }

    public function testAFailedPatchPointsAtTheIssueToReportIt(): void
    {
        $url = 'https://x.test/a.patch';
        $guidance = Guidance::forRow(self::patchRow(
            [self::file($url)],
            self::cachedRun(PatchRevision::of($url), CheckStatus::Failed),
        ));

        self::assertSame('1 patch, failed', $guidance->status);
        self::assertSame('upkeep issue widget 3597808', $guidance->command);
    }

    public function testAStaleVerdictSaysSoAndPointsAtCheckingAgain(): void
    {
        $guidance = Guidance::forRow(self::patchRow(
            [self::file('https://x.test/new.patch')],
            self::cachedRun(PatchRevision::of('https://x.test/old.patch'), CheckStatus::Passed),
        ));

        self::assertSame('1 patch, stale check', $guidance->status);
        self::assertStringContainsString('patch:check', (string) $guidance->command);
    }

    /**
     * The row the whole patch surface exists for: a merge request that covers
     * nothing, beside a patch that is the only work there is.
     */
    public function testAnEmptyMergeRequestBesideAPatchIsNamedInTheStatus(): void
    {
        $row = DashboardRow::forPatch('11', new Contribution(
            'widget',
            new Issue(
                nid: 3597808,
                title: 'An issue',
                status: IssueStatus::NeedsReview,
                url: 'https://www.drupal.org/node/3597808',
                project: 'widget',
                priority: 200,
                version: '1.0.x-dev',
                component: 'Code',
                category: 'Task',
                files: [self::file('https://x.test/a.patch')],
            ),
            [new MergeRequest(
                iid: 1,
                title: 'Issue #3597808: bot',
                state: 'opened',
                authorUsername: 'bot',
                authorId: 2,
                sourceBranch: '3597808-bot',
                targetBranch: '1.0.x',
                draft: true,
                detailedMergeStatus: null,
                headSha: 'same',
                webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/1',
                diffBaseSha: 'same',
                diffHeadSha: 'same',
            )],
        ), null);

        self::assertSame('1 patch, empty MR', Guidance::forRow($row)->status);
    }

    public function testAnIssueWithNoPatchAtAllPointsAtOpeningIt(): void
    {
        $guidance = Guidance::forRow(self::patchRow([]));

        self::assertSame('no patch attached', $guidance->status);
        self::assertSame('upkeep issue widget 3597808', $guidance->command);
    }

    // ------------------------------------------------------------- landings

    /**
     * The row that prompted this: an issue with the bot's open draft on it,
     * whose real work was promoted from a patch, fixed, and merged. The draft
     * is still the only *open* merge request, so the row said
     * "draft, needs a check" and pointed at checking a branch that had been
     * superseded — while the work was already in git.
     *
     * A landing outranks every other reading of the row, because it is the one
     * fact a maintainer cannot recover by looking at the issue.
     */
    public function testALandedIssueSaysSoRatherThanProposingAnotherCheck(): void
    {
        $guidance = Guidance::forRow(self::landedRow(newerWork: false));

        self::assertSame('merged 2026-09-03', $guidance->status);
        self::assertStringContainsString('upkeep issue widget 3', $guidance->command);
    }

    /**
     * Landed, then somebody posted again — a bot re-running after core moved
     * is the standard case. That is an ordinary open contribution once more,
     * so the command goes back to checking it.
     */
    public function testNewerWorkAfterALandingIsCheckableAgain(): void
    {
        $guidance = Guidance::forRow(self::landedRow(newerWork: true));

        self::assertSame('merged 2026-09-03, newer work since', $guidance->status);
        self::assertStringContainsString('upkeep check', $guidance->command);
    }

    private static function landedRow(bool $newerWork): DashboardRow
    {
        $landed = new MergeRequest(
            iid: 3,
            title: 'Issue #3598272: Automated Drupal 12 compatibility fixes',
            state: 'merged',
            authorUsername: 'owenbush',
            authorId: 1,
            sourceBranch: '3598272-automated-drupal-12',
            targetBranch: '2.0.x',
            draft: false,
            detailedMergeStatus: null,
            headSha: null,
            webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/3',
            mergedAt: '2026-09-03T10:00:00Z',
        );

        return DashboardRow::forMergeRequest(
            'widget',
            '11',
            new Project(1, 'widget', 'project/widget', 'Widget', 'https://git.drupalcode.org/project/widget'),
            self::draftMr(),
            null,
            new GateVerdict(GateStatus::Review, ['draft', 'ci-missing', 'local-missing']),
            null,
            $landed,
            $newerWork,
        );
    }

    private static function draftMr(): MergeRequest
    {
        return new MergeRequest(
            iid: 2,
            title: 'Draft: Automated Project Update Bot fixes',
            state: 'opened',
            authorUsername: 'project update bot',
            authorId: 3644742,
            sourceBranch: 'project-update-bot-only',
            targetBranch: '2.0.x',
            draft: true,
            detailedMergeStatus: null,
            headSha: 'aaa',
            webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/2',
        );
    }
}

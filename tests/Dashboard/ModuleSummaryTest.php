<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Dashboard\LocalEvidence;
use Upkeep\Dashboard\ModuleSummary;
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
 * The overview's arithmetic.
 *
 * Worth pinning directly rather than through the rendered table: the counting
 * rule is subtle (subjects, not rows) and getting it wrong would overstate a
 * maintainer's queue — the sort of error that looks plausible on screen and is
 * never questioned.
 *
 * The rule survived the row model; what it counts changed. Rows are no longer
 * multiplied by core, so the gap between rows and subjects narrowed — but an
 * issue can still carry two merge requests on one branch, and a backport is
 * still two rows over one issue.
 */
final class ModuleSummaryTest extends TestCase
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

    private static function mr(int $iid, string $state = 'opened'): MergeRequest
    {
        return new MergeRequest(
            iid: $iid,
            title: 'Issue #1: a change',
            state: $state,
            authorUsername: 'alice',
            authorId: 1,
            sourceBranch: 'fix',
            targetBranch: '1.0.x',
            draft: false,
            detailedMergeStatus: null,
            headSha: self::HEAD,
            webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/' . $iid,
            mergedAt: $state === 'merged' ? '2026-09-01T00:00:00Z' : null,
        );
    }

    /**
     * @param list<string> $cores
     */
    private static function evidence(
        ?CachedResult $local,
        array $cores = ['11'],
        ?string $revision = self::HEAD,
    ): LocalEvidence {
        $byCore = [];
        foreach ($cores as $core) {
            $byCore[$core] = $local;
        }

        return LocalEvidence::of($byCore, $revision);
    }

    private static function mrRow(int $iid, GateStatus $status, ?CachedResult $local = null): DashboardRow
    {
        return DashboardRow::forUnlinkedMergeRequest(
            'widget',
            '1.0.x',
            self::project(),
            self::mr($iid),
            self::evidence($local),
            new GateVerdict($status, $status === GateStatus::ReadyAuto ? [] : ['because']),
        );
    }

    private static function green(string $sha): CachedResult
    {
        return new CachedResult(
            $sha,
            new \DateTimeImmutable(),
            new CheckRunResult([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'ok', 1.0)]),
        );
    }

    /**
     * @param list<MergeRequest> $mergeRequests
     */
    private static function issue(int $nid, array $mergeRequests = []): Contribution
    {
        $url = 'https://example.test/' . $nid . '.patch';

        return new Contribution('widget', new Issue(
            nid: $nid,
            title: 'An issue',
            status: IssueStatus::NeedsReview,
            url: 'https://www.drupal.org/node/' . $nid,
            project: 'widget',
            priority: 200,
            version: '1.0.x-dev',
            component: 'Code',
            category: 'Task',
            files: [new IssueFile('p.patch', $url, 2048, 1705400000)],
        ), $mergeRequests);
    }

    private static function patchRow(int $nid, ?CachedResult $local = null): DashboardRow
    {
        $contribution = self::issue($nid);

        return DashboardRow::forIssue(
            'widget',
            '1.0.x',
            $contribution,
            null,
            null,
            self::evidence($local, ['11'], $contribution->currentRevision()),
            null,
        );
    }

    /**
     * The rule that matters, in the shape that survives the row model: an
     * issue carrying two open merge requests on one branch is one row and two
     * merge requests. Counting rows instead would understate the queue — the
     * opposite of the old error, and just as wrong.
     */
    public function testMergeRequestsAreCountedPerSubjectNotPerRow(): void
    {
        $contribution = self::issue(3597808, [self::mr(5), self::mr(6)]);

        $row = DashboardRow::forIssue(
            'widget',
            '1.0.x',
            $contribution,
            self::project(),
            self::mr(5),
            self::evidence(null),
            new GateVerdict(GateStatus::Review, ['because']),
        );

        $summary = ModuleSummary::fromRows('widget', [$row]);

        self::assertSame(2, $summary->mergeRequests, 'two MRs on one row');
        self::assertSame(1, $summary->patchIssues, 'one issue');
        self::assertSame(['1.0.x'], $summary->branches);
    }

    /** A merged merge request is not part of the open queue it is counting. */
    public function testAMergedMergeRequestIsNotCountedAsOpen(): void
    {
        $contribution = self::issue(3597808, [self::mr(5), self::mr(3, 'merged')]);

        $row = DashboardRow::forIssue(
            'widget',
            '1.0.x',
            $contribution,
            self::project(),
            self::mr(5),
            self::evidence(null),
            new GateVerdict(GateStatus::Review, ['because']),
        );

        self::assertSame(1, ModuleSummary::fromRows('widget', [$row])->mergeRequests);
    }

    /**
     * One verdict per row, because a row is one piece of work. This used to be
     * one verdict per (merge request x core), which is how a merge request
     * green on 11 and unchecked on 10 could contribute a READY count of 1.
     */
    public function testVerdictsAreCountedOncePerRow(): void
    {
        $summary = ModuleSummary::fromRows('widget', [
            self::mrRow(5, GateStatus::ReadyAuto, self::green(self::HEAD)),
            self::mrRow(6, GateStatus::Review, self::green(self::HEAD)),
            self::mrRow(7, GateStatus::Blocked, self::green(self::HEAD)),
        ]);

        self::assertSame(3, $summary->mergeRequests);
        self::assertSame(1, $summary->readyAuto);
        self::assertSame(1, $summary->review);
        self::assertSame(1, $summary->blocked);
    }

    /**
     * UNCHECKED is the actionable number: rows with no local verdict, plus
     * rows whose verdict is about a revision that is no longer current. A
     * stale pass is not a pass.
     */
    public function testUncheckedCountsBothNeverCheckedAndStale(): void
    {
        $summary = ModuleSummary::fromRows('widget', [
            self::mrRow(5, GateStatus::Review),
            self::mrRow(6, GateStatus::Review, self::green('0000000')),
            self::mrRow(7, GateStatus::Review, self::green(self::HEAD)),
            self::patchRow(3597808),
            self::patchRow(3501234, self::green(PatchRevision::of('https://example.test/3501234.patch'))),
        ]);

        self::assertSame(3, $summary->unchecked, 'never-checked MR, stale MR, never-checked patch');
    }

    /**
     * A row green on one core and unchecked on another counts as unchecked.
     * The number exists to say how much work stands between the queue and a
     * verdict, and half-covered evidence is work.
     */
    public function testARowGreenOnOneCoreAndUncheckedOnAnotherCountsAsUnchecked(): void
    {
        $row = DashboardRow::forUnlinkedMergeRequest(
            'widget',
            '1.0.x',
            self::project(),
            self::mr(5),
            LocalEvidence::of(['10' => null, '11' => self::green(self::HEAD)], self::HEAD),
            new GateVerdict(GateStatus::Review, ['local-missing']),
        );

        self::assertSame(1, ModuleSummary::fromRows('widget', [$row])->unchecked);
    }

    public function testAModuleWhoseMergeRequestsCannotBeListedIsMarkedFailed(): void
    {
        $summary = ModuleSummary::fromRows('widget', [
            DashboardRow::forModuleFailure('widget', new NotFound('https://git.drupalcode.org/project/widget')),
        ]);

        self::assertTrue($summary->failed);
        self::assertSame(
            ['widget', '–', '–', '–', '–', '–', '–', 'never'],
            $summary->toTableCells('never'),
            'a module that could not be read reports nothing rather than zero',
        );
    }

    /**
     * Zero and "could not be read" must not render the same way anywhere else:
     * a real zero is a fact, and the dash on a failed row is the absence of
     * one.
     */
    public function testARealZeroRendersAsADashButTheModuleStillReportsItsBranches(): void
    {
        $summary = ModuleSummary::fromRows('widget', [
            self::mrRow(5, GateStatus::Review, self::green(self::HEAD)),
        ]);

        self::assertFalse($summary->failed);
        // MODULE, BRANCHES, MRS, PATCH ISSUES, READY, CI FAILED, UNCHECKED,
        // CACHED. BRANCHES replaced CORES: a branch is what a row is about,
        // and it supports several cores at once. The REVIEW column is gone —
        // it counted everything neither ready nor CI-failed, which is every
        // row, and a number that is always the total says nothing. The count
        // itself survives on the object.
        self::assertSame(
            ['widget', '1.0.x', '1', '0', '–', '–', '–', '2m ago'],
            $summary->toTableCells('2m ago'),
        );
        self::assertSame(1, $summary->review, 'still counted, just not a column');
    }

    public function testAModuleWithNothingAtAllSummarisesToZeroes(): void
    {
        $summary = ModuleSummary::fromRows('widget', []);

        self::assertSame(0, $summary->mergeRequests);
        self::assertSame(0, $summary->patchIssues);
        self::assertSame([], $summary->branches);
        self::assertSame(['widget', '–', '0', '0', '–', '–', '–', 'never'], $summary->toTableCells('never'));
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Dashboard\DashboardRow;
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
 * maintainer's queue by however many core versions they track — the sort of
 * error that looks plausible on screen and is never questioned.
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

    private static function mr(int $iid): MergeRequest
    {
        return new MergeRequest(
            iid: $iid,
            title: 'Issue #1: a change',
            state: 'opened',
            authorUsername: 'alice',
            authorId: 1,
            sourceBranch: 'fix',
            targetBranch: '1.0.x',
            draft: false,
            detailedMergeStatus: null,
            headSha: self::HEAD,
            webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/' . $iid,
        );
    }

    private static function mrRow(int $iid, string $core, GateStatus $status, ?CachedResult $local = null): DashboardRow
    {
        return DashboardRow::forMergeRequest(
            'widget',
            $core,
            self::project(),
            self::mr($iid),
            $local,
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

    private static function patchRow(int $nid, string $core, ?CachedResult $local = null): DashboardRow
    {
        $url = 'https://example.test/' . $nid . '.patch';

        return DashboardRow::forPatch($core, new Contribution('widget', new Issue(
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
        )), $local);
    }

    /**
     * The rule that matters: one merge request tracked across two cores is one
     * merge request. Counting rows instead would report a two-core module's
     * queue as twice its real size.
     */
    public function testSubjectsAreCountedOncePerModuleNotOncePerCore(): void
    {
        $summary = ModuleSummary::fromRows('widget', [
            self::mrRow(5, '10', GateStatus::Review),
            self::mrRow(5, '11', GateStatus::Review),
            self::mrRow(6, '10', GateStatus::Review),
            self::mrRow(6, '11', GateStatus::Review),
            self::patchRow(3597808, '10'),
            self::patchRow(3597808, '11'),
        ]);

        self::assertSame(2, $summary->mergeRequests, 'two MRs, not four rows');
        self::assertSame(1, $summary->patchIssues, 'one issue, not two rows');
        self::assertSame(['10', '11'], $summary->cores);
    }

    /**
     * Verdicts and evidence *are* per (subject x core): the same branch can be
     * green on one core and red on another, which is the entire reason both
     * are tracked. Those stay per-row.
     */
    public function testVerdictsAreCountedPerCoreBecauseTheyDifferPerCore(): void
    {
        $summary = ModuleSummary::fromRows('widget', [
            self::mrRow(5, '10', GateStatus::ReadyAuto, self::green(self::HEAD)),
            self::mrRow(5, '11', GateStatus::Review, self::green(self::HEAD)),
            self::mrRow(6, '11', GateStatus::Blocked, self::green(self::HEAD)),
        ]);

        self::assertSame(2, $summary->mergeRequests);
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
            self::mrRow(5, '11', GateStatus::Review),
            self::mrRow(6, '11', GateStatus::Review, self::green('0000000')),
            self::mrRow(7, '11', GateStatus::Review, self::green(self::HEAD)),
            self::patchRow(3597808, '11'),
            self::patchRow(3501234, '11', self::green(PatchRevision::of('https://example.test/3501234.patch'))),
        ]);

        self::assertSame(3, $summary->unchecked, 'never-checked MR, stale MR, never-checked patch');
    }

    public function testAModuleWhoseMergeRequestsCannotBeListedIsMarkedFailed(): void
    {
        $summary = ModuleSummary::fromRows('widget', [
            DashboardRow::forModuleFailure('widget', new NotFound('https://git.drupalcode.org/project/widget')),
        ]);

        self::assertTrue($summary->failed);
        self::assertSame(
            ['widget', '–', '–', '–', '–', '–', '–', '–', 'never'],
            $summary->toTableCells('never'),
            'a module that could not be read reports nothing rather than zero',
        );
    }

    /**
     * Zero and "could not be read" must not render the same way anywhere else:
     * a real zero is a fact, and the dash on a failed row is the absence of
     * one.
     */
    public function testARealZeroRendersAsADashButTheModuleStillReportsItsCores(): void
    {
        $summary = ModuleSummary::fromRows('widget', [
            self::mrRow(5, '11', GateStatus::Review, self::green(self::HEAD)),
        ]);

        self::assertFalse($summary->failed);
        self::assertSame(
            ['widget', '11', '1', '–', '1', '–', '0', '–', '2m ago'],
            $summary->toTableCells('2m ago'),
        );
    }

    public function testAModuleWithNothingAtAllSummarisesToZeroes(): void
    {
        $summary = ModuleSummary::fromRows('widget', []);

        self::assertSame(0, $summary->mergeRequests);
        self::assertSame(0, $summary->patchIssues);
        self::assertSame([], $summary->cores);
        self::assertSame(['widget', '–', '0', '–', '–', '–', '0', '–', 'never'], $summary->toTableCells('never'));
    }
}

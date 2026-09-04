<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Cockpit\Module;
use Upkeep\Dashboard\ModuleSnapshot;
use Upkeep\Dashboard\RowFactory;
use Upkeep\Gate\GateStatus;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Project;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;

/**
 * RowFactory is the one classification pipeline. The dashboard (from its
 * cached snapshot) and the fast-lane merge command (from live client data)
 * both go through it, so a row's verdict cannot depend on which command
 * asked for it — the drift that would misclassify merge eligibility.
 */
final class RowFactoryTest extends TestCase
{
    private const HEAD_SHA = 'abc123def456abc123def456abc123def456abcd';

    private string $resultsDir;

    protected function setUp(): void
    {
        $this->resultsDir = sys_get_temp_dir() . '/upkeep-rowfactory-' . bin2hex(random_bytes(4));
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->resultsDir));
    }

    /**
     * Without a snapshot there are no issues to group by, so every merge
     * request is its own row — and the tracked core versions no longer
     * multiply them. Two merge requests across two tracked cores used to be
     * four rows describing the same two branches.
     */
    public function testOneRowPerMergeRequestOrderedByIidWhateverCoresAreTracked(): void
    {
        $rows = $this->factory()->rows(
            self::module(['10', '11']),
            self::project(),
            [self::mergeRequest(9), self::mergeRequest(4)],
        );

        self::assertSame(
            [['widget', '4', '1.x'], ['widget', '9', '1.x']],
            array_map(
                static fn ($row): array => [$row->module, (string) $row->requireMergeRequest()->iid, $row->branch],
                $rows,
            ),
        );
        self::assertSame(['10', '11'], $rows[0]->local->cores(), 'both cores, as one row\'s evidence');
    }

    /**
     * `--version` narrows the *evidence*, not the row count. Core stopped
     * being part of a row's identity, so filtering by one cannot remove rows —
     * it says which core to look at on the rows there are.
     */
    public function testAVersionFilterNarrowsTheEvidenceRatherThanTheRows(): void
    {
        $rows = $this->factory()->rows(self::module(['10', '11']), self::project(), [self::mergeRequest(4)], '11');

        self::assertCount(1, $rows);
        self::assertSame(['11'], $rows[0]->local->cores());
    }

    public function testAVersionFilterTheModuleDoesNotTrackYieldsNoRows(): void
    {
        $rows = $this->factory()->rows(self::module(['11']), self::project(), [self::mergeRequest(4)], '9');

        self::assertSame([], $rows);
    }

    /**
     * The reason the pipeline is shared at all: the gate verdict a row
     * carries is computed here, from the same evidence, whoever asked.
     */
    public function testTheGateVerdictIsComputedFromTheCachedLocalEvidence(): void
    {
        $this->storePassingLocal(4);

        $rows = $this->factory()->rows(self::module(['11']), self::project(), [self::mergeRequest(4)]);

        self::assertSame(GateStatus::ReadyAuto, $rows[0]->requireVerdict()->status);
        self::assertTrue($rows[0]->isReadyAuto());
    }

    public function testWithoutLocalEvidenceTheGateWithholdsReadyAuto(): void
    {
        $rows = $this->factory()->rows(self::module(['11']), self::project(), [self::mergeRequest(4)]);

        self::assertNotSame(GateStatus::ReadyAuto, $rows[0]->requireVerdict()->status);
    }

    /**
     * The behaviour change the one-row model forced, seen end to end.
     *
     * A merge request checked on 11 and never checked on 10 used to produce
     * two rows — one READY-AUTO, one not — and the fast lane took the ready
     * one, merging on evidence that covered half the cores the module tracks.
     * One row per merge request cannot hide that: the unchecked core is in the
     * same cell as the pass.
     */
    public function testEvidenceOnOnlySomeTrackedCoresDeniesTheFastLane(): void
    {
        $this->storePassingLocal(4);

        $rows = $this->factory()->rows(self::module(['10', '11']), self::project(), [self::mergeRequest(4)]);

        self::assertCount(1, $rows);
        self::assertFalse($rows[0]->isReadyAuto());
        self::assertSame('pass 11 · ? 10', $rows[0]->localCell());
    }

    private function factory(): RowFactory
    {
        return new RowFactory(new ResultsCache($this->resultsDir));
    }

    private function storePassingLocal(int $iid): void
    {
        (new ResultsCache($this->resultsDir))->store(
            'widget',
            ResultKey::mergeRequest($iid),
            '11',
            self::HEAD_SHA,
            new CheckRunResult([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.0)]),
        );
    }

    /** @param list<string> $coreVersions */
    private static function module(array $coreVersions): Module
    {
        return new Module('widget', 'project/widget', $coreVersions);
    }

    private static function project(): Project
    {
        return Project::fromApi([
            'id' => 1,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ]);
    }

    private static function mergeRequest(int $iid): MergeRequest
    {
        return MergeRequest::fromApi([
            'iid' => $iid,
            'title' => 'Automated Project Update Bot fixes',
            'state' => 'opened',
            'source_branch' => 'project-update-bot-only',
            'target_branch' => '1.x',
            'sha' => self::HEAD_SHA,
            'draft' => false,
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/' . $iid,
            'author' => ['username' => 'Project-Update-Bot', 'id' => 66574],
            'detailed_merge_status' => 'mergeable',
            'head_pipeline' => [
                'id' => 1,
                'status' => 'success',
                'web_url' => 'https://git.drupalcode.org/project/widget/-/pipelines/1',
                'sha' => self::HEAD_SHA,
            ],
        ]);
    }

    /**
     * The reported row, end to end.
     *
     * An issue with the bot's open draft on it, whose real work was promoted
     * from a patch, fixed and merged. The draft is the only *open* merge
     * request, so its row said "draft, needs a check" and pointed at checking
     * a branch that had been superseded — while the work was already in git.
     *
     * The pairing runs through the issue fork, which is the only thing that
     * connects a bot MR to its issue at all.
     */
    public function testABotsDraftRowReportsThatTheIssuesWorkHasLanded(): void
    {
        $draft = MergeRequest::fromApi([
            'iid' => 2,
            'title' => 'Draft: Automated Project Update Bot fixes',
            'state' => 'opened',
            'source_branch' => 'project-update-bot-only',
            'target_branch' => '2.0.x',
            'draft' => true,
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/2',
            'author' => ['username' => 'Project-Update-Bot', 'id' => 66574],
            // The fork is what pairs it: the title names no issue and the
            // description would only say "Relates to".
            'source_project_id' => 218528,
        ]);

        $rows = $this->factory()->rows(
            self::module(['11']),
            self::project(),
            [$draft],
            null,
            [],
            self::snapshotWithLanding(),
        );

        self::assertCount(1, $rows);
        self::assertSame(3, $rows[0]->landed?->iid);
        self::assertFalse($rows[0]->newerWorkSinceLanding);
    }

    /**
     * An issue whose work is entirely merged, carrying no patch, leaves the
     * queue. There is nothing left to do about it, and a row per finished
     * issue is how a dashboard stops being read.
     *
     * It used to produce a row (the merged MR's own) that reported no landing
     * at all — a row saying nothing, about work that was done.
     */
    public function testAnIssueWhoseWorkIsAllMergedAndCarriesNoPatchLeavesTheQueue(): void
    {
        $merged = MergeRequest::fromApi(self::landedMrPayload());

        $rows = $this->factory()->rows(
            self::module(['11']),
            self::project(),
            [$merged],
            null,
            [],
            self::snapshotWithLanding(),
        );

        self::assertSame([], $rows);
    }

    /**
     * The one multiplier left, and a real one: an issue backported to two
     * branches is two pieces of work, not one seen twice. Branch, not core —
     * a branch supports several cores at once.
     */
    public function testAnIssueWithWorkOnTwoBranchesIsTwoRows(): void
    {
        $onOldBranch = MergeRequest::fromApi([
            'iid' => 5,
            'title' => 'Issue #3598272: backport',
            'state' => 'opened',
            'source_branch' => '3598272-backport',
            'target_branch' => '1.0.x',
            'draft' => false,
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/5',
            'author' => ['username' => 'owenbush', 'id' => 1],
            'source_project_id' => 218528,
        ]);
        $onNewBranch = MergeRequest::fromApi([
            'iid' => 6,
            'title' => 'Issue #3598272: the fix',
            'state' => 'opened',
            'source_branch' => '3598272-fix',
            'target_branch' => '2.0.x',
            'draft' => false,
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/6',
            'author' => ['username' => 'owenbush', 'id' => 1],
            'source_project_id' => 218528,
        ]);

        $rows = $this->factory()->rows(
            self::module(['11']),
            self::project(),
            [$onOldBranch, $onNewBranch],
            null,
            [],
            self::snapshotWithLanding(),
        );

        self::assertSame(
            [['1.0.x', 5], ['2.0.x', 6]],
            array_map(static fn ($row): array => [$row->branch, $row->requireMergeRequest()->iid], $rows),
        );
        // Both rows belong to the same issue, and both see its landing.
        self::assertSame([3598272, 3598272], array_map(static fn ($row): ?int => $row->issueNid, $rows));
    }

    /**
     * A merge request claiming no issue in the module's open queue keeps a row
     * of its own — 33 of pathauto's 162 claim no issue at all. The nid it does
     * claim is still shown; unpaired is not anonymous.
     */
    public function testAMergeRequestOutsideTheIssueQueueKeepsItsOwnRow(): void
    {
        $orphan = MergeRequest::fromApi([
            'iid' => 8,
            'title' => 'Issue #3111111: something long since fixed',
            'state' => 'opened',
            'source_branch' => '3111111-thing',
            'target_branch' => '2.0.x',
            'draft' => false,
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/8',
            'author' => ['username' => 'owenbush', 'id' => 1],
        ]);

        $rows = $this->factory()->rows(
            self::module(['11']),
            self::project(),
            [$orphan],
            null,
            [],
            self::snapshotWithLanding(),
        );

        self::assertCount(1, $rows);
        self::assertNull($rows[0]->contribution, 'no issue to pair it with');
        self::assertSame(3111111, $rows[0]->issueNid, 'but the nid it claims is still shown');
        self::assertSame('3111111', $rows[0]->issueCell(), 'without a status, which we do not have');
    }

    /** With no snapshot to consult, rows are exactly what they always were. */
    public function testWithoutASnapshotNoLandingIsClaimed(): void
    {
        $rows = $this->factory()->rows(self::module(['11']), self::project(), [self::mergeRequest(4)]);

        self::assertNull($rows[0]->landed);
    }

    /** @return array<string, mixed> */
    private static function landedMrPayload(): array
    {
        return [
            'iid' => 3,
            'title' => 'Issue #3598272: Automated Drupal 12 compatibility fixes',
            'state' => 'merged',
            'source_branch' => '3598272-automated-drupal-12',
            'target_branch' => '2.0.x',
            'draft' => false,
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/3',
            'author' => ['username' => 'owenbush', 'id' => 1],
            'source_project_id' => 218528,
            'merged_at' => '2026-09-03T10:00:00Z',
        ];
    }

    private static function snapshotWithLanding(): ModuleSnapshot
    {
        return new ModuleSnapshot(
            new \DateTimeImmutable('2026-09-03T12:00:00+00:00'),
            ['id' => 1, 'path' => 'widget', 'path_with_namespace' => 'project/widget', 'name' => 'Widget'],
            [],
            [[
                'nid' => 3598272,
                'title' => 'Automated Drupal 12 compatibility fixes',
                'field_issue_status' => 8,
                'url' => 'https://www.drupal.org/node/3598272',
                'field_project' => ['machine_name' => 'widget'],
                'field_issue_files' => [],
            ]],
            [self::landedMrPayload()],
            [218528 => 3598272]
        );
    }
}

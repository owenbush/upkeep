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

    public function testOneRowPerMergeRequestPerTrackedCoreVersionOrderedByIid(): void
    {
        $rows = $this->factory()->rows(
            self::module(['10', '11']),
            self::project(),
            [self::mergeRequest(9), self::mergeRequest(4)],
        );

        self::assertSame(
            [['widget', '4', '10'], ['widget', '4', '11'], ['widget', '9', '10'], ['widget', '9', '11']],
            array_map(
                static fn ($row): array => [$row->module, (string) $row->requireMergeRequest()->iid, $row->core],
                $rows,
            ),
        );
    }

    public function testAVersionFilterKeepsOnlyThatCoreVersion(): void
    {
        $rows = $this->factory()->rows(self::module(['10', '11']), self::project(), [self::mergeRequest(4)], '11');

        self::assertCount(1, $rows);
        self::assertSame('11', $rows[0]->core);
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

    /** A merged MR's own row does not report itself; that says nothing. */
    public function testAMergedMergeRequestDoesNotReportItselfAsTheLanding(): void
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

        self::assertNull($rows[0]->landed);
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
            [218528 => 3598272],
        );
    }
}

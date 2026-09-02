<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\TestCase;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Gate\GateStatus;
use Upkeep\Gate\GateVerdict;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\NotFound;
use Upkeep\Gitlab\Pipeline;
use Upkeep\Gitlab\PipelineStatus;
use Upkeep\Gitlab\Project;

/**
 * The dashboard table and the fast-lane merge command render the same evidence
 * through these helpers, so the two commands can never describe one merge
 * request differently. What matters per cell is that an unknown is never shown
 * as a pass: a pipeline that failed to fetch, or one that has not run, must
 * read distinctly from a green one.
 */
final class DashboardRowCellsTest extends TestCase
{
    private const HEAD_SHA = 'abc123def456abc123def456abc123def456abcd';

    public function testTheCiCellDistinguishesGreenRedUnfinishedAbsentAndUnfetchable(): void
    {
        self::assertSame('pass', self::row(self::pipeline(PipelineStatus::Success))->ciCell());
        self::assertSame('fail', self::row(self::pipeline(PipelineStatus::Failed))->ciCell());
        self::assertSame('running', self::row(self::pipeline(PipelineStatus::Running))->ciCell());
        self::assertSame(
            'some_future_status',
            self::row(self::pipeline(PipelineStatus::Unknown, 'some_future_status'))->ciCell(),
        );
        self::assertSame('–', self::row(null)->ciCell(), 'No pipeline is an unknown, never a pass.');

        // The detail fetch that carries head_pipeline failed, so the row fell
        // back to the listed MR data: the cell says so rather than reporting
        // the absent pipeline as "no CI configured".
        $unfetchable = self::row(null, new NotFound('https://git.drupalcode.org/project/widget'));
        self::assertSame('n/a (404)', $unfetchable->ciCell());
    }

    public function testTheStatusCellCarriesTheVerdictForAnMrRowAndTheFailureForAModuleRow(): void
    {
        $row = self::row(self::pipeline(PipelineStatus::Success));
        // statusCell() is still the gate's own vocabulary — the merge command
        // and -v both read it. The plain-English phrase is Guidance's.
        self::assertSame('REVIEW local-missing', $row->statusCell());
        // The rendered cell is a phrase, not the verdict: a maintainer reading
        // a hundred rows needs what to do, not the gate's bookkeeping. The
        // verdict is what -v restores, and what the merge command reads.
        self::assertSame('needs a check', $row->toTableCells()[6]);
        self::assertSame($row->statusCell(), $row->toTableCells(true)[6]);
        self::assertStringContainsString('upkeep check widget 4', $row->toTableCells()[7]);

        $failure = DashboardRow::forModuleFailure(
            'widget',
            new NotFound('https://git.drupalcode.org/project/widget'),
        );
        self::assertSame('n/a (404)', $failure->statusCell());
    }

    private static function pipeline(PipelineStatus $status, ?string $raw = null): Pipeline
    {
        return new Pipeline(1, $status, $raw ?? $status->value, self::HEAD_SHA, 'https://example.org/pipelines/1');
    }

    private static function row(?Pipeline $pipeline, ?NotFound $ciFailure = null): DashboardRow
    {
        return DashboardRow::forMergeRequest(
            'widget',
            '11',
            Project::fromApi([
                'id' => 1,
                'path' => 'widget',
                'path_with_namespace' => 'project/widget',
                'web_url' => 'https://git.drupalcode.org/project/widget',
            ]),
            new MergeRequest(
                iid: 4,
                title: 'Automated Project Update Bot fixes',
                state: 'opened',
                authorUsername: 'Project-Update-Bot',
                authorId: 66574,
                sourceBranch: 'project-update-bot-only',
                targetBranch: '1.x',
                draft: false,
                detailedMergeStatus: 'mergeable',
                headSha: self::HEAD_SHA,
                webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/4',
                headPipeline: $pipeline,
            ),
            null,
            new GateVerdict(GateStatus::Review, ['local-missing']),
            $ciFailure,
        );
    }
}

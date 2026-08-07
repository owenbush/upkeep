<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Cockpit\Module;
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
}

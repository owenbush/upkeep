<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Cockpit\Module;
use Upkeep\Dashboard\RowAssembler;
use Upkeep\Gate\GateStatus;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Results\ResultsCache;

/**
 * RowAssembler is the read-only fetch half of the dashboard: registry modules
 * in, rows out. It talks to a block-by-default GitLab instance, so the
 * behaviour that matters is what happens when a fetch does not succeed —
 * every typed client failure has to become a visible row state, never a crash
 * and never a silently missing module.
 */
final class RowAssemblerTest extends TestCase
{
    private const HEAD_SHA = 'abc123def456abc123def456abc123def456abcd';

    private string $resultsDir;

    protected function setUp(): void
    {
        $this->resultsDir = sys_get_temp_dir() . '/upkeep-rowassembler-' . bin2hex(random_bytes(4));
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->resultsDir));
    }

    public function testEveryOpenMergeRequestBecomesARowPerTrackedCoreOrderedByModule(): void
    {
        $assembler = $this->assembler([
            self::json(self::projectPayload()),
            self::json([self::listedMrPayload(7)]),
            self::json(self::detailedMrPayload(7)),
        ]);

        $rows = $assembler->assemble(['widget' => new Module('widget', 'project/widget', ['10', '11'])]);

        self::assertSame(
            [['widget', '10'], ['widget', '11']],
            array_map(static fn ($row): array => [$row->module, $row->core], $rows),
        );
        self::assertSame(7, $rows[0]->requireMergeRequest()->iid);
        self::assertSame('pass', $rows[0]->ciCell(), 'The detail fetch supplies head_pipeline.');
    }

    public function testAModuleWhoseProjectCannotBeFetchedStillGetsAVisibleRow(): void
    {
        $assembler = $this->assembler([new MockResponse('', ['http_code' => 404])]);

        $rows = $assembler->assemble(['widget' => new Module('widget', 'project/widget', ['11'])]);

        self::assertCount(1, $rows);
        self::assertSame('-', $rows[0]->core);
        self::assertSame('n/a (404)', $rows[0]->statusCell());
        self::assertFalse($rows[0]->isReadyAuto());
    }

    public function testAModuleWhoseMergeRequestsCannotBeListedStillGetsAVisibleRow(): void
    {
        $assembler = $this->assembler([
            self::json(self::projectPayload()),
            new MockResponse('', ['http_code' => 403]),
        ]);

        $rows = $assembler->assemble(['widget' => new Module('widget', 'project/widget', ['11'])]);

        self::assertCount(1, $rows);
        self::assertSame('n/a (403)', $rows[0]->statusCell());
    }

    public function testAFailedDetailFetchFallsBackToTheListedMrAndTheGateStillWithholdsReadyAuto(): void
    {
        // Only the single-MR endpoint carries head_pipeline. Losing it must not
        // lose the row — but a row with no observable CI can never be
        // READY-AUTO, whatever the local evidence says.
        $this->storePassingLocal(7);
        $assembler = $this->assembler([
            self::json(self::projectPayload()),
            self::json([self::listedMrPayload(7)]),
            new MockResponse('', ['http_code' => 404]),
        ]);

        $rows = $assembler->assemble(['widget' => new Module('widget', 'project/widget', ['11'])]);

        self::assertCount(1, $rows);
        self::assertSame(7, $rows[0]->requireMergeRequest()->iid);
        self::assertSame('n/a (404)', $rows[0]->ciCell());
        self::assertSame(GateStatus::Review, $rows[0]->requireVerdict()->status);
        self::assertContains('ci-missing', $rows[0]->requireVerdict()->reasons);
    }

    public function testAModuleFilteredOutByTheVersionSelectorIsNotFetchedAtAll(): void
    {
        // No HTTP responses are queued: reaching the client at all would fail
        // the test. A module that tracks no matching core has nothing to ask
        // GitLab about.
        $assembler = $this->assembler([]);

        self::assertSame([], $assembler->assemble(
            ['widget' => new Module('widget', 'project/widget', ['11'])],
            '9',
        ));
    }

    /**
     * @param list<MockResponse> $responses
     */
    private function assembler(array $responses): RowAssembler
    {
        $queue = $responses;
        $factory = function (string $method, string $url) use (&$queue): MockResponse {
            $response = array_shift($queue);
            if ($response === null) {
                $this->fail('Unexpected HTTP request: ' . $method . ' ' . $url);
            }

            return $response;
        };

        return new RowAssembler(
            new GitlabClient(new MockHttpClient($factory), 'glpat-test-token'),
            new ResultsCache($this->resultsDir),
        );
    }

    private function storePassingLocal(int $iid): void
    {
        (new ResultsCache($this->resultsDir))->store(
            'widget',
            $iid,
            '11',
            self::HEAD_SHA,
            new CheckRunResult([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.0)]),
        );
    }

    /**
     * @param array<array-key, mixed> $payload
     */
    private static function json(array $payload): MockResponse
    {
        return new MockResponse(
            (string) json_encode($payload),
            ['response_headers' => ['content-type' => 'application/json']],
        );
    }

    /** @return array<array-key, mixed> */
    private static function projectPayload(): array
    {
        return [
            'id' => 1,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ];
    }

    /** @return array<array-key, mixed> */
    private static function listedMrPayload(int $iid): array
    {
        return [
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
        ];
    }

    /** @return array<array-key, mixed> */
    private static function detailedMrPayload(int $iid): array
    {
        return self::listedMrPayload($iid) + [
            'head_pipeline' => [
                'id' => 1,
                'status' => 'success',
                'sha' => self::HEAD_SHA,
                'web_url' => 'https://git.drupalcode.org/project/widget/-/pipelines/1',
            ],
        ];
    }
}

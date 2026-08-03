<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Command\DashboardCommand;
use Upkeep\Dashboard\DashboardCache;
use Upkeep\Dashboard\ModuleSnapshot;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Results\ResultsCache;

/**
 * Command-level tests for the dashboard's thin wiring: row assembly per
 * (open MR x tracked core), the --version filter, LOCAL cache states, and
 * typed client failures rendering as explicit cell states. The gate logic
 * itself is exhaustively covered in FastLaneGateTest.
 */
final class DashboardCommandTest extends TestCase
{
    private const HEAD_SHA = 'abc123def456abc123def456abc123def456abcd';

    private string $cockpit;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-dashboard-cmd-test-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit, 0o755, true);
        file_put_contents(
            $this->cockpit . '/registry.yml',
            "modules:\n  widget:\n    project: project/widget\n    core_versions: [\"10\", \"11\"]\n",
        );
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->cockpit));
    }

    /**
     * Client whose responses are routed by URL substring, so the client's
     * per-URL memoization and request order stay irrelevant to the tests.
     *
     * @param array<string, MockResponse> $routes substring => response
     */
    private function client(array $routes): GitlabClient
    {
        $factory = static function (string $method, string $url) use ($routes): MockResponse {
            foreach ($routes as $needle => $response) {
                if (str_contains($url, $needle)) {
                    return $response;
                }
            }

            throw new \LogicException('Unrouted request in test: ' . $method . ' ' . $url);
        };

        return new GitlabClient(new MockHttpClient($factory), 'glpat-test-token');
    }

    private static function json(array $payload, int $status = 200): MockResponse
    {
        return new MockResponse(json_encode($payload, JSON_THROW_ON_ERROR), [
            'http_code' => $status,
            'response_headers' => ['content-type' => 'application/json'],
        ]);
    }

    private static function projectPayload(): array
    {
        return [
            'id' => 4242,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'name' => 'Widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ];
    }

    private static function botMrPayload(array $overrides = []): array
    {
        return $overrides + [
            'id' => 900001,
            'iid' => 5,
            'title' => 'Automated Project Update Bot fixes',
            'state' => 'opened',
            'draft' => false,
            'author' => ['username' => 'Project-Update-Bot', 'id' => 66574],
            'source_branch' => 'project-update-bot-only',
            'target_branch' => '1.x',
            'detailed_merge_status' => 'mergeable',
            'sha' => self::HEAD_SHA,
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/5',
        ];
    }

    private static function greenPipeline(): array
    {
        return [
            'id' => 77,
            'status' => 'success',
            'sha' => self::HEAD_SHA,
            'web_url' => 'https://git.drupalcode.org/project/widget/-/pipelines/77',
        ];
    }

    private function noDrupalClient(): DrupalOrgClient
    {
        return new DrupalOrgClient(new MockHttpClient(static fn () => new MockResponse('', ['http_code' => 404])));
    }

    private function runDashboard(GitlabClient $client, array $args = []): CommandTester
    {
        $tester = new CommandTester(new DashboardCommand($client, $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, ...$args]);

        return $tester;
    }

    private function storeLocal(array $checks, string $sha = self::HEAD_SHA): void
    {
        (new ResultsCache($this->cockpit . '/results'))->store(
            'widget',
            5,
            '11',
            $sha,
            new CheckRunResult($checks),
        );
    }

    /** Routes for the healthy one-bot-MR module. */
    private function healthyRoutes(): array
    {
        return [
            '/merge_requests/5' => self::json(self::botMrPayload(['head_pipeline' => self::greenPipeline()])),
            '/merge_requests?' => self::json([self::botMrPayload()]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ];
    }

    public function testRendersOneRowPerOpenMrPerTrackedCoreVersion(): void
    {
        $tester = $this->runDashboard($this->client($this->healthyRoutes()));

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();

        foreach (['MODULE', 'MR', 'ISSUE', 'CORE', 'TITLE', 'CI', 'LOCAL', 'STATUS'] as $header) {
            self::assertStringContainsString($header, $display);
        }
        // One row per tracked core version of the module for the single MR.
        self::assertSame(2, substr_count($display, 'Automated Project Update Bot fixes'));
        self::assertMatchesRegularExpression('/widget\s+!5\s+–\s+10/', $display);
        self::assertMatchesRegularExpression('/widget\s+!5\s+–\s+11/', $display);
        self::assertStringContainsString('cached just now', $display);
        // CI green, nothing cached locally: visible – and a review status.
        self::assertSame(2, substr_count($display, 'REVIEW local-missing'));
    }

    public function testVersionOptionFiltersToOneTargetCoreVersion(): void
    {
        $tester = $this->runDashboard($this->client($this->healthyRoutes()), ['--version' => '11']);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();

        self::assertSame(1, substr_count($display, 'Automated Project Update Bot fixes'));
        self::assertMatchesRegularExpression('/widget\s+!5\s+–\s+11/', $display);
        self::assertDoesNotMatchRegularExpression('/widget\s+!5\s+–\s+10/', $display);
    }

    public function testFreshAllGreenLocalResultYieldsReadyAutoRow(): void
    {
        $this->storeLocal([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.2)]);

        $tester = $this->runDashboard($this->client($this->healthyRoutes()), ['--version' => '11']);

        $display = $tester->getDisplay();
        self::assertStringContainsString('READY-AUTO', $display);
        self::assertMatchesRegularExpression('/pass\s+pass\s+READY-AUTO/', $display);
    }

    public function testFreshFailedLocalCheckRendersFailAndReviewNamingTheCheck(): void
    {
        $this->storeLocal([new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 2, 'FAILURES!', 3.4)]);

        $tester = $this->runDashboard($this->client($this->healthyRoutes()), ['--version' => '11']);

        $display = $tester->getDisplay();
        self::assertMatchesRegularExpression('/\bfail\b/', $display);
        self::assertStringContainsString('REVIEW local-failed:phpunit', $display);
        self::assertStringNotContainsString('READY-AUTO', $display);
    }

    public function testLocalResultForAnOlderShaRendersAsStaleAndDeniesReadyAuto(): void
    {
        $this->storeLocal(
            [new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.2)],
            '0000000000000000000000000000000000000000',
        );

        $tester = $this->runDashboard($this->client($this->healthyRoutes()), ['--version' => '11']);

        $display = $tester->getDisplay();
        self::assertStringContainsString('stale', $display);
        self::assertStringContainsString('REVIEW local-stale', $display);
        self::assertStringNotContainsString('READY-AUTO', $display);
    }

    public function testClosedMrListEndpointRendersAsExplicitCellStateNotACrash(): void
    {
        $client = $this->client([
            '/merge_requests?' => self::json(['message' => '403 Forbidden'], 403),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runDashboard($client);

        $tester->assertCommandIsSuccessful();
        self::assertStringContainsString('n/a (403)', $tester->getDisplay());
    }

    public function testClosedSingleMrEndpointDegradesTheCiCellOnly(): void
    {
        // The MR list works but the detail fetch (pipeline source) is closed:
        // the row still renders with the listed data (no pipeline), CI shows
        // "-", and the gate conservatively denies READY-AUTO for lack of CI.
        $client = $this->client([
            '/merge_requests/5' => self::json(['message' => '403 Forbidden'], 403),
            '/merge_requests?' => self::json([self::botMrPayload()]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runDashboard($client, ['--version' => '11']);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('ci-missing', $display);
        self::assertStringNotContainsString('READY-AUTO', $display);
    }

    public function testCachedSnapshotSkipsApiCallsOnSubsequentRun(): void
    {
        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [self::botMrPayload(['head_pipeline' => self::greenPipeline()])],
            [],
        );
        $cache = new DashboardCache($this->cockpit . '/cache/dashboard');
        $cache->save('widget', $snapshot);

        // Client that would throw on any request — proving no API calls are made.
        $client = new GitlabClient(
            new MockHttpClient(static fn () => throw new \LogicException('No API calls expected')),
            'glpat-test-token',
        );
        $tester = new CommandTester(new DashboardCommand($client, $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11']);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('Automated Project Update Bot fixes', $display);
        self::assertStringContainsString('cached', $display);
    }

    public function testRefreshBypassesCacheAndFetchesFresh(): void
    {
        // Seed cache with old data (different MR title).
        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable('-1 hour'),
            self::projectPayload(),
            [self::botMrPayload(['title' => 'Old cached title', 'head_pipeline' => self::greenPipeline()])],
            [],
        );
        $cache = new DashboardCache($this->cockpit . '/cache/dashboard');
        $cache->save('widget', $snapshot);

        // Fresh fetch returns a different title.
        $tester = $this->runDashboard(
            $this->client($this->healthyRoutes()),
            ['--refresh' => null],
        );

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('Automated Project Update Bot fixes', $display);
        self::assertStringNotContainsString('Old cached title', $display);
        self::assertStringContainsString('just now', $display);
    }

    public function testRefreshSingleModuleOnlyRefetchesThatModule(): void
    {
        // Register a second module to verify selective refresh.
        file_put_contents(
            $this->cockpit . '/registry.yml',
            "modules:\n  alpha:\n    project: project/alpha\n    core_versions: [\"11\"]\n"
            . "  widget:\n    project: project/widget\n    core_versions: [\"11\"]\n",
        );

        // Cache both modules.
        $cache = new DashboardCache($this->cockpit . '/cache/dashboard');
        $cache->save('alpha', new ModuleSnapshot(
            new \DateTimeImmutable('-2 hours'),
            [
                'id' => 1000,
                'path' => 'alpha',
                'path_with_namespace' => 'project/alpha',
                'name' => 'Alpha',
                'web_url' => 'https://git.drupalcode.org/project/alpha',
            ],
            [
                [
                    'iid' => 1,
                    'title' => 'Alpha MR',
                    'state' => 'opened',
                    'draft' => false,
                    'author' => ['username' => 'bot', 'id' => 1],
                    'source_branch' => 'fix',
                    'target_branch' => '1.x',
                    'detailed_merge_status' => 'mergeable',
                    'sha' => self::HEAD_SHA,
                    'web_url' => 'https://git.drupalcode.org/project/alpha/-/merge_requests/1',
                ],
            ],
            [],
        ));
        $cache->save('widget', new ModuleSnapshot(
            new \DateTimeImmutable('-2 hours'),
            self::projectPayload(),
            [self::botMrPayload(['head_pipeline' => self::greenPipeline()])],
            [],
        ));

        // Only refresh widget — alpha should come from cache, widget from API.
        $tester = $this->runDashboard(
            $this->client($this->healthyRoutes()),
            ['--refresh' => 'widget'],
        );

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('Alpha MR', $display);
        self::assertStringContainsString('Automated Project Update Bot fixes', $display);
    }

    public function testCachePersistsAfterFreshFetch(): void
    {
        $tester = $this->runDashboard($this->client($this->healthyRoutes()), ['--version' => '11']);
        $tester->assertCommandIsSuccessful();

        $cache = new DashboardCache($this->cockpit . '/cache/dashboard');
        $snapshot = $cache->load('widget');
        self::assertNotNull($snapshot);
        self::assertSame(4242, $snapshot->project()->id);
        self::assertCount(1, $snapshot->mergeRequests());
        self::assertSame(5, $snapshot->mergeRequests()[0]->iid);
    }

    public function testIssueWithNewerPatchShowsPatchFlag(): void
    {
        $mrUpdated = '2026-07-20T10:00:00Z';
        $patchTimestamp = strtotime('2026-07-25T12:00:00Z');

        $issueNid = 3467675;
        $issueData = [
            'nid' => $issueNid,
            'title' => 'Make URL field required',
            'url' => 'https://www.drupal.org/project/widget/issues/' . $issueNid,
            'field_issue_status' => '8',
            'field_issue_priority' => '200',
            'field_project' => ['machine_name' => 'widget'],
            'field_issue_files' => [
                ['file' => [
                    'filename' => $issueNid . '-42.patch',
                    'url' => 'https://www.drupal.org/files/issues/' . $issueNid . '-42.patch',
                    'filesize' => '1024',
                    'timestamp' => (string) $patchTimestamp,
                ]],
            ],
        ];

        $mr = self::botMrPayload([
            'title' => 'Issue #' . $issueNid . ': Make URL field required',
            'source_branch' => $issueNid . '-make-url-required',
            'head_pipeline' => self::greenPipeline(),
            'updated_at' => $mrUpdated,
        ]);

        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [$mr],
            [$issueNid => $issueData],
        );
        $cache = new DashboardCache($this->cockpit . '/cache/dashboard');
        $cache->save('widget', $snapshot);

        $client = new GitlabClient(
            new MockHttpClient(static fn () => throw new \LogicException('No API calls expected')),
            'glpat-test-token',
        );
        $tester = new CommandTester(new DashboardCommand($client, $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11']);

        $tester->assertCommandIsSuccessful();
        self::assertStringContainsString('patch↑', $tester->getDisplay());
    }

    public function testIssueWithOlderPatchDoesNotShowFlag(): void
    {
        $mrUpdated = '2026-07-25T12:00:00Z';
        $patchTimestamp = strtotime('2026-07-20T10:00:00Z');

        $issueNid = 3467675;
        $issueData = [
            'nid' => $issueNid,
            'title' => 'Make URL field required',
            'url' => 'https://www.drupal.org/project/widget/issues/' . $issueNid,
            'field_issue_status' => '8',
            'field_issue_priority' => '200',
            'field_project' => ['machine_name' => 'widget'],
            'field_issue_files' => [
                ['file' => [
                    'filename' => $issueNid . '-42.patch',
                    'url' => 'https://www.drupal.org/files/issues/' . $issueNid . '-42.patch',
                    'filesize' => '1024',
                    'timestamp' => (string) $patchTimestamp,
                ]],
            ],
        ];

        $mr = self::botMrPayload([
            'title' => 'Issue #' . $issueNid . ': Make URL field required',
            'source_branch' => $issueNid . '-make-url-required',
            'head_pipeline' => self::greenPipeline(),
            'updated_at' => $mrUpdated,
        ]);

        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [$mr],
            [$issueNid => $issueData],
        );
        $cache = new DashboardCache($this->cockpit . '/cache/dashboard');
        $cache->save('widget', $snapshot);

        $client = new GitlabClient(
            new MockHttpClient(static fn () => throw new \LogicException('No API calls expected')),
            'glpat-test-token',
        );
        $tester = new CommandTester(new DashboardCommand($client, $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11']);

        $tester->assertCommandIsSuccessful();
        self::assertStringNotContainsString('patch↑', $tester->getDisplay());
    }

    public function testCachedLocalResultsAlwaysResolvedFresh(): void
    {
        // Cache remote data.
        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [self::botMrPayload(['head_pipeline' => self::greenPipeline()])],
            [],
        );
        $cache = new DashboardCache($this->cockpit . '/cache/dashboard');
        $cache->save('widget', $snapshot);

        // Store a fresh passing local result.
        $this->storeLocal([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.2)]);

        $client = new GitlabClient(
            new MockHttpClient(static fn () => throw new \LogicException('No API calls expected')),
            'glpat-test-token',
        );
        $tester = new CommandTester(new DashboardCommand($client, $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11']);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('READY-AUTO', $display);
        self::assertMatchesRegularExpression('/pass\s+pass\s+READY-AUTO/', $display);
    }
}

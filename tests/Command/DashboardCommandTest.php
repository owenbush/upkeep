<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Output\OutputInterface;
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
use Upkeep\Patches\PatchRevision;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;

/**
 * Command-level tests for the dashboard's thin wiring: a row per (issue,
 * module branch), the --version filter over the evidence, LOCAL cache states,
 * and typed client failures rendering as explicit cell states. The gate logic
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

            // Landing lookups a test has not pinned answer empty rather than
            // exploding: they are additive, and a test about CI colouring
            // should not have to know that merged MRs exist.
            if (str_contains($url, '/forks?') || str_contains($url, 'state=merged')) {
                return new MockResponse('[]', ['response_headers' => ['content-type' => 'application/json']]);
            }

            throw new \LogicException('Unrouted request in test: ' . $method . ' ' . $url);
        };

        return new GitlabClient(new MockHttpClient($factory), 'glpat-test-token');
    }

    /** @param array<array-key, mixed> $payload single object payload or a list of them */
    private static function json(array $payload, int $status = 200): MockResponse
    {
        return new MockResponse(json_encode($payload, JSON_THROW_ON_ERROR), [
            'http_code' => $status,
            'response_headers' => ['content-type' => 'application/json'],
        ]);
    }

    /** @return array<string, mixed> */
    private static function projectPayload(): array
    {
        return [
            'id' => 4242,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'name' => 'Widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
            'default_branch' => '1.x',
        ];
    }

    /**
     * @param array<string, mixed> $overrides
     *
     * @return array<string, mixed>
     */
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

    /** @return array<string, mixed> */
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

    /**
     * Drives the detailed table. The command's default is now the per-module
     * overview, so the rows these tests assert on have to be asked for; the
     * overview has its own tests below.
     *
     * @param array<string, mixed> $args
     */
    private function runDashboard(GitlabClient $client, array $args = []): CommandTester
    {
        $tester = new CommandTester(new DashboardCommand($client, $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, '--all' => true, ...$args]);

        return $tester;
    }

    /** @param list<CheckResult> $checks */
    private function storeLocal(array $checks, string $sha = self::HEAD_SHA): void
    {
        (new ResultsCache($this->cockpit . '/results'))->store(
            'widget',
            ResultKey::mergeRequest(5),
            '11',
            $sha,
            new CheckRunResult($checks),
        );
    }

    /**
     * Routes for the healthy one-bot-MR module.
     *
     * @return array<string, MockResponse>
     */
    private function healthyRoutes(): array
    {
        return [
            '/merge_requests/5' => self::json(self::botMrPayload(['head_pipeline' => self::greenPipeline()])),
            '/merge_requests?' => self::json([self::botMrPayload()]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ];
    }

    public function testRendersOneRowPerMergeRequestWhateverCoresAreTracked(): void
    {
        $tester = $this->runDashboard($this->client($this->healthyRoutes()));

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();

        foreach (['MODULE', 'ISSUE', 'VERSION', 'TITLE', 'MR', 'PATCH', 'CI', 'LOCAL', 'STATUS'] as $header) {
            self::assertStringContainsString($header, $display);
        }
        // One row, for the one merge request. The module tracks two cores and
        // that used to be two rows describing the same branch.
        self::assertSame(1, substr_count($display, 'Automated Project Update Bot fixes'));
        // VERSION is the branch the MR targets, not a core major.
        self::assertMatchesRegularExpression('/widget\s+–\s+1\.x\s+Automated/', $display);
        self::assertStringContainsString('cached just now', $display);
        // CI green, nothing cached locally: visible – and a review status.
        self::assertSame(1, substr_count($display, 'needs a check'));
        // The command names the first core that needs attention.
        self::assertStringContainsString('upkeep check widget 5 --version=10', $display);
    }

    public function testVersionOptionNarrowsTheEvidenceRatherThanTheRows(): void
    {
        $tester = $this->runDashboard($this->client($this->healthyRoutes()), ['--version' => '11']);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();

        // The row is the same row; what changed is which core it reports on.
        self::assertSame(1, substr_count($display, 'Automated Project Update Bot fixes'));
        self::assertStringContainsString('upkeep check widget 5 --version=11', $display);
        self::assertStringNotContainsString('--version=10', $display);
    }

    public function testFreshAllGreenLocalResultYieldsReadyAutoRow(): void
    {
        $this->storeLocal([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.2)]);

        $tester = $this->runDashboard($this->client($this->healthyRoutes()), ['--version' => '11']);

        $display = $tester->getDisplay();
        self::assertStringContainsString('ready to merge', $display);
        self::assertMatchesRegularExpression('/pass\s+pass 11\s+ready to merge/', $display);
        self::assertStringContainsString('upkeep merge --fast-lane', $display);
    }

    public function testFreshFailedLocalCheckRendersFailAndReviewNamingTheCheck(): void
    {
        $this->storeLocal([new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 2, 'FAILURES!', 3.4)]);

        $tester = $this->runDashboard($this->client($this->healthyRoutes()), ['--version' => '11']);

        $display = $tester->getDisplay();
        self::assertMatchesRegularExpression('/\bfail\b/', $display);
        self::assertStringContainsString('phpunit failed', $display);
        // A failing check is your evidence, so the row points at telling the
        // contributor rather than at checking it again.
        self::assertStringContainsString('upkeep needs-work widget 5', $display);
        self::assertStringNotContainsString('ready to merge', $display);
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
        self::assertStringContainsString('checks are stale', $display);
        self::assertStringNotContainsString('ready to merge', $display);
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
        // The detail fetch failed, so the row falls back to listed data, which
        // carries no pipeline — an en-dash CI cell and no fast lane. The gate's
        // own ci-missing reason is what -v restores, which is now the only
        // place that vocabulary appears.
        self::assertMatchesRegularExpression('/widget\s+–\s+1\.x\s+.*!5\s+–\s+–\s+–/', $display);
        self::assertStringNotContainsString('ready to merge', $display);

        // CommandTester carries verbosity as an option to execute(), not as an
        // argv flag: a bare CommandTester has no application to define -v.
        $verbose = new CommandTester(new DashboardCommand($client, $this->noDrupalClient()));
        $verbose->execute(
            ['--cockpit' => $this->cockpit, '--version' => '11', '--all' => true],
            ['verbosity' => OutputInterface::VERBOSITY_VERBOSE],
        );
        self::assertStringContainsString('ci-missing', $verbose->getDisplay());
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
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11', '--all' => true]);

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

    /**
     * The row the user reported, rendered.
     *
     * A compatibility issue kept open by convention, carrying the bot's draft
     * — and, invisibly, a merge request whose work went in weeks ago. The
     * draft was the only *open* merge request, so the row said "draft, needs a
     * check" and pointed at checking a branch that had been superseded.
     *
     * The MR column now leads with the landing, and it is the one cell on the
     * table coloured for being *done* rather than for needing something.
     */
    public function testALandedIssueSaysSoInTheMergeRequestColumn(): void
    {
        $nid = 3598272;
        $draft = self::botMrPayload([
            'iid' => 5,
            'title' => 'Draft: Automated Project Update Bot fixes',
            'draft' => true,
            'source_project_id' => 218528,
            'head_pipeline' => self::greenPipeline(),
        ]);
        $landed = self::botMrPayload([
            'iid' => 3,
            'title' => 'Issue #' . $nid . ': the real work',
            'state' => 'merged',
            'source_branch' => $nid . '-the-real-work',
            'source_project_id' => 218528,
            'merged_at' => '2026-09-03T10:00:00Z',
        ]);

        (new DashboardCache($this->cockpit . '/cache/dashboard'))->save('widget', new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [$draft],
            [],
            [self::patchIssuePayload($nid, [])],
            [$landed],
            [218528 => $nid],
        ));

        $tester = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $tester->execute(['module' => 'widget', '--cockpit' => $this->cockpit]);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('!3 merged 2026-09-03', $display, 'the landing, in the MR column');
        self::assertStringContainsString('merged 2026-09-03', $display, 'and as the row\'s status');
        self::assertStringNotContainsString('needs a check', $display);
        // The issue and its drupal.org status, which the old ISSUE cell never
        // carried — "review" and "RTBC" ask different things of a maintainer.
        self::assertStringContainsString($nid . ' review', $display);
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

        // The issue goes in the module's open queue: a row *is* an issue now,
        // so the patch flag is a statement about the row rather than a
        // cross-reference from a merge-request row to an issue somewhere else.
        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [$mr],
            [$issueNid => $issueData],
            [$issueData],
        );
        $cache = new DashboardCache($this->cockpit . '/cache/dashboard');
        $cache->save('widget', $snapshot);

        $client = new GitlabClient(
            new MockHttpClient(static fn () => throw new \LogicException('No API calls expected')),
            'glpat-test-token',
        );
        $tester = new CommandTester(new DashboardCommand($client, $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11', '--all' => true]);

        $tester->assertCommandIsSuccessful();
        self::assertStringContainsString('↑', $tester->getDisplay());
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

        // The issue goes in the module's open queue: a row *is* an issue now,
        // so the patch flag is a statement about the row rather than a
        // cross-reference from a merge-request row to an issue somewhere else.
        $snapshot = new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [$mr],
            [$issueNid => $issueData],
            [$issueData],
        );
        $cache = new DashboardCache($this->cockpit . '/cache/dashboard');
        $cache->save('widget', $snapshot);

        $client = new GitlabClient(
            new MockHttpClient(static fn () => throw new \LogicException('No API calls expected')),
            'glpat-test-token',
        );
        $tester = new CommandTester(new DashboardCommand($client, $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11', '--all' => true]);

        $tester->assertCommandIsSuccessful();
        self::assertStringNotContainsString('↑', $tester->getDisplay());
    }

    /**
     * Nothing open anywhere is a normal answer, not an empty table: the note
     * says so in the operator's own terms, and unfiltered it must not claim a
     * core version was asked for.
     */
    public function testNoOpenMergeRequestsAnywhereIsReportedAsANoteNotAnEmptyTable(): void
    {
        $tester = $this->runDashboard($this->client([
            '/merge_requests?' => self::json([]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]));

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('No open contributions across the registered modules.', $display);
        self::assertStringNotContainsString('targeting core', $display);
        self::assertStringNotContainsString('MODULE', $display);
    }

    /** The same emptiness under a filter names the core version that was asked for. */
    public function testNoOpenMergeRequestsForTheFilteredCoreNamesThatVersion(): void
    {
        $tester = $this->runDashboard(
            $this->client([
                '/merge_requests?' => self::json([]),
                '/projects/project%2Fwidget' => self::json(self::projectPayload()),
            ]),
            ['--version' => '11'],
        );

        $tester->assertCommandIsSuccessful();
        self::assertStringContainsString('targeting core 11', $tester->getDisplay());
    }

    /**
     * A module whose project cannot even be looked up still gets a row. The
     * dashboard's job is to show the state of every registered module, and
     * "we could not ask" is a state — silently dropping the module would read
     * as "nothing open here".
     */
    public function testAModuleWhoseProjectLookupFailsStillGetsARowNamingTheFailure(): void
    {
        $tester = $this->runDashboard($this->client([
            '/projects/project%2Fwidget' => self::json(['message' => '404 Project Not Found'], 404),
        ]));

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('widget', $display);
        self::assertStringContainsString('n/a (404)', $display);
        self::assertStringContainsString('(merge requests unavailable)', $display);
    }

    /**
     * On a live fetch the linked drupal.org issue is resolved and cached into
     * the snapshot, so the ISSUE column is populated on the very first run —
     * not only on later runs reading a hand-seeded cache.
     */
    public function testALiveFetchResolvesTheLinkedIssueAndCachesItIntoTheSnapshot(): void
    {
        $issueNid = 3467675;
        $mr = self::botMrPayload([
            'title' => 'Issue #' . $issueNid . ': Make URL field required',
            'source_branch' => $issueNid . '-make-url-required',
            'head_pipeline' => self::greenPipeline(),
        ]);

        $drupal = new DrupalOrgClient(new MockHttpClient(static fn (): MockResponse => self::json([
            'nid' => $issueNid,
            'title' => 'Make URL field required',
            'url' => 'https://www.drupal.org/project/widget/issues/' . $issueNid,
            'field_issue_status' => '8',
            'field_project' => ['machine_name' => 'widget'],
        ])));

        $client = $this->client([
            '/merge_requests/5' => self::json($mr),
            '/merge_requests?' => self::json([$mr]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = new CommandTester(new DashboardCommand($client, $drupal));
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11', '--all' => true]);

        $tester->assertCommandIsSuccessful();
        self::assertStringContainsString((string) $issueNid, $tester->getDisplay());

        // The issue travelled into the on-disk snapshot, so the next run needs
        // neither GitLab nor drupal.org to render the same ISSUE cell.
        $snapshot = (new DashboardCache($this->cockpit . '/cache/dashboard'))->load('widget');
        self::assertNotNull($snapshot);
        self::assertNotNull($snapshot->issue($issueNid));
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
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11', '--all' => true]);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('ready to merge', $display);
        self::assertMatchesRegularExpression('/pass\s+pass 11\s+ready to merge/', $display);
        self::assertStringContainsString('upkeep merge --fast-lane', $display);
    }

    /**
     * @param list<array<string, mixed>> $patchIssues
     */
    private function saveSnapshotWithPatchIssues(array $patchIssues): void
    {
        (new DashboardCache($this->cockpit . '/cache/dashboard'))->save('widget', new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [],
            [],
            $patchIssues,
        ));
    }

    /**
     * @param list<array{string, string}> $patches filename and URL
     *
     * @return array<string, mixed>
     */
    private static function patchIssuePayload(int $nid, array $patches): array
    {
        return [
            'nid' => $nid,
            'title' => 'Automated Drupal 12 compatibility fixes',
            'url' => 'https://www.drupal.org/node/' . $nid,
            'field_issue_status' => '8',
            'field_project' => ['machine_name' => 'widget'],
            'field_issue_files' => array_map(
                static fn (array $p): array => ['file' => [
                    'filename' => $p[0],
                    'url' => $p[1],
                    'filesize' => '2048',
                    'timestamp' => '1705400000',
                ]],
                $patches,
            ),
        ];
    }

    private function noApiClient(): GitlabClient
    {
        return new GitlabClient(
            new MockHttpClient(static fn () => throw new \LogicException('No API calls expected')),
            'glpat-test-token',
        );
    }

    /**
     * Patch contributions are rows on the same table as merge requests: one
     * view of everything open, which is what a maintainer actually triages.
     */
    public function testPatchIssuesFromTheSnapshotAppearAsRows(): void
    {
        $this->saveSnapshotWithPatchIssues([
            self::patchIssuePayload(3597808, [['bot.patch', 'https://example.test/2026-07-11/bot.patch']]),
        ]);

        $tester = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11', '--all' => true]);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('patch', $display);
        self::assertStringContainsString('3597808', $display);
        self::assertStringContainsString('1 patch', $display);
        self::assertStringContainsString('upkeep patch:check widget 3597808', $display);
        self::assertStringContainsString('1 patch issue', $display);
    }

    /**
     * The escape hatch for anyone who wants the pre-existing view: the patch
     * scan is skipped entirely, not merely hidden.
     */
    public function testNoPatchesRestoresTheMergeRequestOnlyDashboard(): void
    {
        $this->saveSnapshotWithPatchIssues([
            self::patchIssuePayload(3597808, [['bot.patch', 'https://example.test/2026-07-11/bot.patch']]),
        ]);

        $tester = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11', '--no-patches' => true, '--all' => true]);

        $tester->assertCommandIsSuccessful();
        self::assertStringNotContainsString('3597808', $tester->getDisplay());
    }

    /**
     * A patch:check verdict reaches the LOCAL column of its own row, keyed by
     * the patch's revision — and, being in the patch namespace, cannot be read
     * as evidence about any merge request.
     */
    public function testACachedPatchCheckShowsInTheLocalColumn(): void
    {
        $url = 'https://example.test/2026-07-11/bot.patch';
        $this->saveSnapshotWithPatchIssues([self::patchIssuePayload(3597808, [['bot.patch', $url]])]);

        (new ResultsCache($this->cockpit . '/results'))->store(
            'widget',
            ResultKey::patch(3597808),
            '11',
            PatchRevision::of($url),
            new CheckRunResult([new CheckResult(CheckType::PhpCs, CheckStatus::Failed, 1, 'bad spacing', 0.5)]),
        );

        $tester = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11', '--all' => true]);

        $tester->assertCommandIsSuccessful();
        self::assertMatchesRegularExpression('/3597808.*fail/', $tester->getDisplay());
    }

    /**
     * A verdict recorded against a patch that has since been re-rolled reads
     * as stale, not as a green light for code nobody checked.
     */
    public function testAVerdictAboutASupersededPatchReadsAsStale(): void
    {
        $old = 'https://example.test/2026-06-11/bot.patch';
        $new = 'https://example.test/2026-07-11/bot.patch';
        $this->saveSnapshotWithPatchIssues([
            self::patchIssuePayload(3597808, [['bot.patch', $new], ['bot.patch', $old]]),
        ]);

        (new ResultsCache($this->cockpit . '/results'))->store(
            'widget',
            ResultKey::patch(3597808),
            '11',
            PatchRevision::of($old),
            new CheckRunResult([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'ok', 1.0)]),
        );

        $tester = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit, '--version' => '11', '--all' => true]);

        $tester->assertCommandIsSuccessful();
        self::assertMatchesRegularExpression('/3597808.*stale/', $tester->getDisplay());
    }

    /**
     * A refresh is minutes of silence otherwise — each module costs a GitLab
     * round trip plus a drupal.org scan whose attachment lookups are one
     * request per file. The bar goes to stderr, so a piped stdout still
     * receives nothing but the table.
     */
    public function testARefreshShowsProgressOnStderrAndLeavesStdoutClean(): void
    {
        $tester = new CommandTester(new DashboardCommand(
            $this->client([
                '/merge_requests?' => self::json([self::botMrPayload()]),
                '/merge_requests/' => self::json(self::botMrPayload()),
                '/projects/' => self::json(self::projectPayload()),
            ]),
            $this->noDrupalClient(),
        ));

        $tester->execute(
            ['--cockpit' => $this->cockpit, '--version' => '11', '--refresh' => null, '--all' => true],
            ['decorated' => true, 'capture_stderr_separately' => true],
        );

        $tester->assertCommandIsSuccessful();
        self::assertStringContainsString('fetching', $tester->getErrorOutput());
        self::assertStringNotContainsString('fetching', $tester->getDisplay());
    }

    /**
     * A cached run does no fetching, so it gets no bar — one that finished
     * before it rendered would be pure noise.
     */
    public function testACachedRunShowsNoProgressBar(): void
    {
        (new DashboardCache($this->cockpit . '/cache/dashboard'))->save('widget', new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            [self::botMrPayload()],
            [],
        ));

        $tester = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $tester->execute(
            ['--cockpit' => $this->cockpit, '--version' => '11', '--all' => true],
            ['decorated' => true, 'capture_stderr_separately' => true],
        );

        $tester->assertCommandIsSuccessful();
        self::assertStringNotContainsString('fetching', $tester->getErrorOutput());
    }

    // ------------------------------------------------------ two-tier surface

    /**
     * @param list<array<string, mixed>> $mrs
     * @param list<array<string, mixed>> $patchIssues
     */
    private function saveSnapshot(string $module, array $mrs = [], array $patchIssues = []): void
    {
        (new DashboardCache($this->cockpit . '/cache/dashboard'))->save($module, new ModuleSnapshot(
            new \DateTimeImmutable(),
            self::projectPayload(),
            $mrs,
            [],
            $patchIssues,
        ));
    }

    private function twoModuleCockpit(): void
    {
        file_put_contents(
            $this->cockpit . '/registry.yml',
            "modules:\n"
            . "  widget:\n    project: project/widget\n    core_versions: [\"10\", \"11\"]\n"
            . "  gadget:\n    project: project/gadget\n    core_versions: [\"11\"]\n",
        );
    }

    /**
     * The reason the overview exists: a mature contrib project can carry a
     * hundred open merge requests, and multiplying that by tracked cores puts
     * one module past two hundred rows. The aggregate is one line per module.
     */
    public function testTheDefaultViewIsOnePerModuleNotOnePerRow(): void
    {
        $this->twoModuleCockpit();
        $this->saveSnapshot('widget', [
            self::botMrPayload(['iid' => 5, 'head_pipeline' => self::greenPipeline()]),
            self::botMrPayload(['iid' => 6, 'head_pipeline' => self::greenPipeline()]),
        ], [self::patchIssuePayload(3597808, [['bot.patch', 'https://example.test/a.patch']])]);
        $this->saveSnapshot('gadget');

        $tester = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit]);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('MODULE', $display);
        self::assertStringContainsString('UNCHECKED', $display);
        // The overview speaks the same vocabulary as the rows it summarises.
        self::assertStringContainsString('PATCH ISSUES', $display, 'issues, not files');
        self::assertStringContainsString('CI FAILED', $display, 'what BLOCKED actually meant');
        self::assertStringNotContainsString('BLOCKED', $display);
        // Two merge requests and a patch issue are three detailed rows; the
        // overview is one line, and never names an individual MR. BRANCHES,
        // not CORES: a branch is what a row is about, and supports several
        // cores at once.
        self::assertStringNotContainsString('!5', $display);
        self::assertStringNotContainsString('3597808', $display);
        self::assertMatchesRegularExpression('/widget\s+1\.x\s+2\s/', $display);
        self::assertStringContainsString('upkeep dashboard <module>', $display);
    }

    /**
     * The BRANCHES column, and counts per distinct subject. One merge request
     * on one branch is one of each, whatever the module tracks — the column
     * used to name the tracked cores, which said nothing about the work.
     */
    public function testTheOverviewNamesBranchesAndCountsSubjects(): void
    {
        $this->twoModuleCockpit();
        $this->saveSnapshot('widget', [self::botMrPayload(['iid' => 5])]);
        $this->saveSnapshot('gadget');

        $tester = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertStringContainsString('BRANCHES', $tester->getDisplay());
        self::assertMatchesRegularExpression('/widget\s+1\.x\s+1\s/', $tester->getDisplay());
    }

    /** Naming a module drills in, and shows only that module. */
    public function testNamingAModuleShowsItsRowsAndOnlyIts(): void
    {
        $this->twoModuleCockpit();
        $this->saveSnapshot('widget', [self::botMrPayload(['iid' => 5])]);
        $this->saveSnapshot('gadget', [self::botMrPayload(['iid' => 9])]);

        $tester = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $tester->execute(['module' => 'widget', '--cockpit' => $this->cockpit]);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('!5', $display);
        self::assertStringNotContainsString('!9', $display);
        self::assertStringNotContainsString('gadget', $display);
    }

    /**
     * The overview and the drill-down are the same rows counted twice, so a
     * module reporting one MR must show exactly one when opened. Two counts of
     * the same thing that can disagree are worse than one count.
     */
    public function testTheOverviewAndTheDrillDownCannotDisagree(): void
    {
        $this->twoModuleCockpit();
        $this->saveSnapshot('widget', [
            self::botMrPayload(['iid' => 5]),
            self::botMrPayload(['iid' => 6]),
        ], [self::patchIssuePayload(3597808, [['a.patch', 'https://example.test/a.patch']])]);
        $this->saveSnapshot('gadget');

        $overview = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $overview->execute(['--cockpit' => $this->cockpit, '--version' => '11']);
        self::assertMatchesRegularExpression('/widget\s+1\.x\s+2\s/', $overview->getDisplay());

        $detail = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $detail->execute(['module' => 'widget', '--cockpit' => $this->cockpit, '--version' => '11']);
        self::assertSame(2, substr_count($detail->getDisplay(), '!5') + substr_count($detail->getDisplay(), '!6'));
        self::assertStringContainsString('3597808', $detail->getDisplay());
    }

    /**
     * A bare --refresh alongside a named module re-fetches that module only.
     * Refreshing a whole cockpit to look at one module would be the expensive
     * half of a command whose entire point was to be specific.
     */
    public function testRefreshingWhileNamingAModuleRefetchesThatModuleOnly(): void
    {
        $this->twoModuleCockpit();
        $this->saveSnapshot('widget', [self::botMrPayload(['iid' => 5, 'title' => 'Cached widget title'])]);
        $this->saveSnapshot('gadget', [self::botMrPayload(['iid' => 9, 'title' => 'Cached gadget title'])]);

        $fetched = [];
        $client = new GitlabClient(
            new MockHttpClient(static function (string $method, string $url) use (&$fetched): MockResponse {
                if (preg_match('#/projects/project%2F(\w+)$#', $url, $m) === 1) {
                    $fetched[] = $m[1];

                    return self::json(self::projectPayload());
                }
                if (str_contains($url, '/merge_requests?')) {
                    return self::json([self::botMrPayload(['iid' => 5, 'title' => 'Freshly fetched'])]);
                }

                return self::json(self::botMrPayload(['iid' => 5, 'title' => 'Freshly fetched']));
            }),
            'glpat-test-token',
        );

        $tester = new CommandTester(new DashboardCommand($client, $this->noDrupalClient()));
        $tester->execute(['module' => 'widget', '--cockpit' => $this->cockpit, '--refresh' => null]);

        $tester->assertCommandIsSuccessful();
        self::assertSame(['widget'], $fetched, 'gadget must not be re-fetched');
        self::assertStringContainsString('Freshly fetched', $tester->getDisplay());
    }

    public function testAnUnregisteredModuleArgumentIsRefused(): void
    {
        $tester = new CommandTester(new DashboardCommand($this->noApiClient(), $this->noDrupalClient()));
        $exit = $tester->execute(['module' => 'nope', '--cockpit' => $this->cockpit]);

        self::assertSame(2, $exit);
        self::assertStringContainsString('not registered', $tester->getDisplay());
    }

    /** A module whose MRs cannot be listed is still visible on the overview. */
    public function testAFailedModuleStillGetsAnOverviewLine(): void
    {
        $tester = $this->runDashboard(
            $this->client(['/projects/' => new MockResponse('', ['http_code' => 404])]),
            ['--all' => false],
        );

        $tester->assertCommandIsSuccessful();
        self::assertStringContainsString('widget', $tester->getDisplay());
    }

    /**
     * The drill-down had no guidance at all — the overview carried a hint and
     * the view where work is actually chosen carried none, which is backwards.
     */
    public function testTheDrillDownTellsYouWhatToReadAndWhatToRun(): void
    {
        $tester = $this->runDashboard($this->client($this->healthyRoutes()), ['--version' => '11']);

        $display = $tester->getDisplay();
        self::assertStringContainsString('NEXT', $display, 'the column exists');
        self::assertStringContainsString('Run the command in NEXT for any row', $display);
        self::assertStringContainsString('upkeep explain', $display);
        self::assertStringContainsString('-v shows the gate', $display);
    }

    /** Under -v the hint about -v is pointless, so it is not printed. */
    public function testTheVerbosityHintIsAbsentWhenAlreadyVerbose(): void
    {
        $tester = new CommandTester(
            new DashboardCommand($this->client($this->healthyRoutes()), $this->noDrupalClient()),
        );
        $tester->execute(
            ['--cockpit' => $this->cockpit, '--version' => '11', '--all' => true],
            ['verbosity' => OutputInterface::VERBOSITY_VERBOSE],
        );

        self::assertStringNotContainsString('-v shows the gate', $tester->getDisplay());
    }

    /**
     * A red pipeline is precisely when a maintainer wants the branch on their
     * own machine to reproduce the failure, so the row names the command that
     * puts it there rather than declining to suggest anything.
     */
    public function testARedCiRowStillNamesTheWayToReproduceItLocally(): void
    {
        $client = $this->client([
            '/merge_requests?' => self::json([self::botMrPayload()]),
            '/merge_requests/5' => self::json(
                self::botMrPayload(['head_pipeline' => ['id' => 1, 'status' => 'failed']]),
            ),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $display = $this->runDashboard($client, ['--version' => '11'])->getDisplay();

        self::assertStringContainsString('CI failed', $display);
        self::assertStringContainsString('upkeep check widget 5', $display);
        self::assertStringNotContainsString('manual fix needed', $display);
    }

    /**
     * A draft says it is one and is still checkable. Unfinished is frequently
     * abandoned — somebody started and could not carry on — and that is a
     * thing for a maintainer to pick up rather than to wait on.
     */
    public function testADraftIsFlaggedButStillCheckable(): void
    {
        $client = $this->client([
            '/merge_requests?' => self::json([self::botMrPayload()]),
            '/merge_requests/5' => self::json(self::botMrPayload([
                'draft' => true,
                'head_pipeline' => self::greenPipeline(),
            ])),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $display = $this->runDashboard($client, ['--version' => '11'])->getDisplay();

        self::assertStringContainsString('draft, needs a check', $display);
        self::assertStringContainsString('upkeep check widget 5', $display);
        self::assertStringNotContainsString('not ready for review yet', $display);
    }

    /** A ready row names the merge command in its own NEXT cell. */
    public function testAReadyRowIsCountedAndNamedInTheHints(): void
    {
        $this->storeLocal([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.2)]);

        $display = $this->runDashboard($this->client($this->healthyRoutes()), ['--version' => '11'])->getDisplay();

        self::assertStringContainsString('1 row ready to merge', $display);
        self::assertStringContainsString('upkeep merge --fast-lane', $display);
    }
}

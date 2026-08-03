<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Tester\ApplicationTester;
use Symfony\Component\Console\Tester\CommandTester;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Command\MergeCommand;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Results\ResultsCache;
use Upkeep\Workflow\ExitCode;

/**
 * CommandTester-level tests for `merge --fast-lane`.
 *
 * TDD split (per the task's verification plan): the guards — approval
 * gating (no API write without an explicit affirmative), the freshness
 * re-check demotions, the EndpointClosed degraded path, and per-MR failure
 * continuation — are strict-TDD'd here against a mocked HTTP transport that
 * records every request; the interactive prompt-loop wiring itself is
 * verified via CommandTester with scripted inputs (setInputs), not by
 * unit-testing Symfony's question helper.
 *
 * @phpstan-type ResponseSpec array{payload: array<array-key, mixed>, status: int}
 */
final class MergeCommandTest extends TestCase
{
    private const HEAD_SHA = 'abc123def456abc123def456abc123def456abcd';
    private const DRIFTED_SHA = 'ffff23def456abc123def456abc123def456ffff';

    private string $cockpit;

    /** @var list<array{method: string, url: string, body: string}> */
    private array $requests = [];

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-merge-cmd-test-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit, 0o755, true);
        file_put_contents(
            $this->cockpit . '/registry.yml',
            "modules:\n  widget:\n    project: project/widget\n    core_versions: [\"11\"]\n",
        );
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->cockpit));
    }

    /**
     * Client whose responses are routed by URL substring, first match wins
     * (order the map most-specific first). A route holds one response spec
     * or a queue of specs consumed one per matching request — the last spec
     * repeats — so the freshness re-fetch (a fresh, unmemoized GET for the
     * same URL) can be served drifted data. A MockResponse is built fresh
     * per request (instances are single-use). Every request is recorded
     * with its body for the no-write assertions.
     *
     * @param array<string, non-empty-list<ResponseSpec>> $routes
     */
    private function client(array $routes): GitlabClient
    {
        $this->requests = [];
        // How many specs of each route's queue have been consumed so far.
        $consumed = [];
        /** @param array<string, mixed> $options */
        $factory = function (string $method, string $url, array $options) use ($routes, &$consumed): MockResponse {
            $body = $options['body'] ?? '';
            $this->requests[] = [
                'method' => $method,
                'url' => $url,
                'body' => \is_string($body) ? $body : '',
            ];
            foreach ($routes as $needle => $specs) {
                if (!str_contains($url, $needle)) {
                    continue;
                }
                // The route's queue is consumed one spec per request; its
                // last spec then repeats for every further request.
                $index = min($consumed[$needle] ?? 0, \count($specs) - 1);
                $consumed[$needle] = $index + 1;

                return self::response($specs[$index]);
            }

            throw new \LogicException('Unrouted request in test: ' . $method . ' ' . $url);
        };

        return new GitlabClient(new MockHttpClient($factory), 'glpat-test-token');
    }

    /**
     * A route serving this one JSON response to every matching request.
     *
     * @param array<array-key, mixed> $payload single object payload or a list of them
     *
     * @return non-empty-list<ResponseSpec>
     */
    private static function json(array $payload, int $status = 200): array
    {
        return [['payload' => $payload, 'status' => $status]];
    }

    /**
     * A route serving each given response in turn, the last one repeating.
     *
     * @param non-empty-list<ResponseSpec>    $first
     * @param non-empty-list<ResponseSpec> ...$rest
     *
     * @return non-empty-list<ResponseSpec>
     */
    private static function queue(array $first, array ...$rest): array
    {
        return array_merge($first, ...$rest);
    }

    /** @param ResponseSpec $spec */
    private static function response(array $spec): MockResponse
    {
        return new MockResponse(json_encode($spec['payload'], JSON_THROW_ON_ERROR), [
            'http_code' => $spec['status'],
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
        ];
    }

    /**
     * @param array<string, mixed> $overrides
     *
     * @return array<string, mixed>
     */
    private static function botMrPayload(int $iid = 5, array $overrides = []): array
    {
        return $overrides + [
            'id' => 900000 + $iid,
            'iid' => $iid,
            'title' => 'Automated Project Update Bot fixes !' . $iid,
            'state' => 'opened',
            'draft' => false,
            'author' => ['username' => 'Project-Update-Bot', 'id' => 66574],
            'source_branch' => 'project-update-bot-only',
            'target_branch' => '1.x',
            'detailed_merge_status' => 'mergeable',
            'sha' => self::HEAD_SHA,
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/' . $iid,
        ];
    }

    /** @return array<string, mixed> */
    private static function greenPipeline(string $sha = self::HEAD_SHA): array
    {
        return [
            'id' => 77,
            'status' => 'success',
            'sha' => $sha,
            'web_url' => 'https://git.drupalcode.org/project/widget/-/pipelines/77',
        ];
    }

    private function storePassingLocal(int $iid = 5, string $sha = self::HEAD_SHA): void
    {
        (new ResultsCache($this->cockpit . '/results'))->store(
            'widget',
            $iid,
            '11',
            $sha,
            new CheckRunResult([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.2)]),
        );
    }

    /**
     * Routes for one healthy READY-AUTO bot MR (!5), most-specific first.
     *
     * @return array<string, non-empty-list<ResponseSpec>>
     */
    private function readyAutoRoutes(): array
    {
        return [
            '/merge_requests/5/merge' => self::json(self::botMrPayload(5, ['state' => 'merged'])),
            '/merge_requests/5' => self::json(self::botMrPayload(5, ['head_pipeline' => self::greenPipeline()])),
            '/merge_requests?' => self::json([self::botMrPayload(5)]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ];
    }

    /**
     * @param list<string> $inputs scripted interactive answers
     */
    private function runMerge(GitlabClient $client, array $inputs = [], bool $interactive = true): CommandTester
    {
        $tester = new CommandTester(new MergeCommand($client));
        if ($inputs !== []) {
            $tester->setInputs($inputs);
        }
        $tester->execute(
            ['--fast-lane' => true, '--cockpit' => $this->cockpit],
            ['interactive' => $interactive],
        );

        return $tester;
    }

    /** @return list<array{method: string, url: string, body: string}> */
    private function putRequests(): array
    {
        return array_values(array_filter($this->requests, static fn (array $r): bool => $r['method'] === 'PUT'));
    }

    public function testZeroReadyAutoRowsPrintsNonActionableSummaryWithReasonsAndNoPrompt(): void
    {
        // A draft bot MR with a green pipeline and no local evidence:
        // REVIEW (draft, local-missing) — never offered for merge.
        $client = $this->client([
            '/merge_requests/5' => self::json(self::botMrPayload(5, [
                'draft' => true,
                'title' => 'Draft: Automated Project Update Bot fixes',
                'head_pipeline' => self::greenPipeline(),
            ])),
            '/merge_requests?' => self::json([self::botMrPayload(5, ['draft' => true])]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runMerge($client);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('widget !5', $display);
        self::assertStringContainsString('REVIEW draft, local-missing', $display);
        self::assertStringContainsString('No READY-AUTO rows', $display);
        self::assertStringNotContainsString('Fast-lane action', $display, 'no prompt may be offered');
        self::assertSame([], $this->putRequests(), 'no API write may happen');
    }

    public function testAffirmativeApprovalPerformsSingleMergeWithShaGuard(): void
    {
        $this->storePassingLocal();
        $tester = $this->runMerge($this->client($this->readyAutoRoutes()), ['merge']);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();

        // Full per-row context block before the prompt.
        self::assertStringContainsString('widget !5', $display);
        self::assertStringContainsString('Automated Project Update Bot fixes !5', $display);
        self::assertStringContainsString('project-update-bot-only -> 1.x', $display);
        self::assertStringContainsString(self::HEAD_SHA, $display);
        self::assertStringContainsString('CI:', $display);
        self::assertStringContainsString('Local:', $display);
        self::assertStringContainsString('https://git.drupalcode.org/project/widget/-/merge_requests/5', $display);
        self::assertStringContainsString('Fast-lane action', $display);

        // Exactly one API write, carrying the expected head SHA so GitLab
        // itself rejects a race the re-check could not see.
        $puts = $this->putRequests();
        self::assertCount(1, $puts);
        self::assertStringContainsString('/merge_requests/5/merge', $puts[0]['url']);
        self::assertStringContainsString(self::HEAD_SHA, $puts[0]['body']);

        self::assertStringContainsString('Merged widget !5', $display);
        self::assertStringContainsString('Merged: 1', $display);
        self::assertStringContainsString('Skipped: 0', $display);
    }

    public function testSkipPerformsNoApiWriteForThatMr(): void
    {
        $this->storePassingLocal();
        $tester = $this->runMerge($this->client($this->readyAutoRoutes()), ['skip']);

        $tester->assertCommandIsSuccessful();
        self::assertSame([], $this->putRequests(), 'declining must perform no API write');
        $display = $tester->getDisplay();
        self::assertStringContainsString('Skipped widget !5', $display);
        self::assertStringContainsString('Skipped: 1', $display);
        self::assertStringContainsString('Merged: 0', $display);
    }

    public function testEmptyPromptResponseFallsBackToSkipAndNeverMerges(): void
    {
        // The affirmative-consent guarantee at its weakest input: pressing
        // Enter (accepting the prompt default) must select the
        // non-destructive answer. Approval is typing "merge" — never the
        // absence of an answer.
        $this->storePassingLocal();
        $tester = $this->runMerge($this->client($this->readyAutoRoutes()), ['']);

        $tester->assertCommandIsSuccessful();
        self::assertSame([], $this->putRequests(), 'the prompt default must never write');
        $display = $tester->getDisplay();
        self::assertStringContainsString('Skipped widget !5', $display);
        self::assertStringContainsString('Merged: 0', $display);
        self::assertStringContainsString('Skipped: 1', $display);
    }

    public function testQuitExitsTheLoopImmediatelyWithoutTouchingRemainingRows(): void
    {
        // Two READY-AUTO rows; quitting on the first must never prompt for
        // (or write to) the second.
        $this->storePassingLocal(5);
        $this->storePassingLocal(7);
        $client = $this->client([
            '/merge_requests/5' => self::json(self::botMrPayload(5, ['head_pipeline' => self::greenPipeline()])),
            '/merge_requests/7' => self::json(self::botMrPayload(7, ['head_pipeline' => self::greenPipeline()])),
            '/merge_requests?' => self::json([self::botMrPayload(5), self::botMrPayload(7)]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runMerge($client, ['quit']);

        $tester->assertCommandIsSuccessful();
        self::assertSame([], $this->putRequests());
        $display = $tester->getDisplay();
        self::assertStringContainsString('Quit', $display);
        self::assertStringContainsString('Fast-lane action for widget !5', $display);
        self::assertStringNotContainsString(
            'Fast-lane action for widget !7',
            $display,
            'quit must not prompt for later rows',
        );
        self::assertStringContainsString('Merged: 0', $display);
    }

    public function testHeadShaDriftAtFreshnessRecheckDemotesInsteadOfMerging(): void
    {
        // Classification saw HEAD_SHA; by approval time the branch moved.
        // The pre-merge re-fetch (unmemoized) sees the drift and demotes.
        $this->storePassingLocal();
        $client = $this->client([
            '/merge_requests/5/merge' => self::json(['message' => 'must not be called'], 500),
            '/merge_requests/5' => self::queue(
                self::json(self::botMrPayload(5, ['head_pipeline' => self::greenPipeline()])),
                self::json(self::botMrPayload(5, [
                    'sha' => self::DRIFTED_SHA,
                    'head_pipeline' => self::greenPipeline(self::DRIFTED_SHA),
                ])),
            ),
            '/merge_requests?' => self::json([self::botMrPayload(5)]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runMerge($client, ['merge']);

        $tester->assertCommandIsSuccessful();
        self::assertSame([], $this->putRequests(), 'a drifted head must never be merged');
        $display = $tester->getDisplay();
        self::assertStringContainsString('sha-drift', $display);
        self::assertStringContainsString('Demoted widget !5', $display);
        self::assertStringContainsString('Demoted: 1', $display);
        self::assertStringContainsString('Merged: 0', $display);
    }

    public function testCiRegressionAtFreshnessRecheckDemotesInsteadOfMerging(): void
    {
        // Same head, but the pipeline flipped to failed between
        // classification and approval.
        $this->storePassingLocal();
        $client = $this->client([
            '/merge_requests/5/merge' => self::json(['message' => 'must not be called'], 500),
            '/merge_requests/5' => self::queue(
                self::json(self::botMrPayload(5, ['head_pipeline' => self::greenPipeline()])),
                self::json(self::botMrPayload(5, [
                    'head_pipeline' => ['status' => 'failed'] + self::greenPipeline(),
                ])),
            ),
            '/merge_requests?' => self::json([self::botMrPayload(5)]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runMerge($client, ['merge']);

        $tester->assertCommandIsSuccessful();
        self::assertSame([], $this->putRequests(), 'a red pipeline must never be merged');
        $display = $tester->getDisplay();
        self::assertStringContainsString('ci-red', $display);
        self::assertStringContainsString('Demoted: 1', $display);
        self::assertStringContainsString('Merged: 0', $display);
    }

    public function testStateChangeAtFreshnessRecheckDemotesInsteadOfMerging(): void
    {
        // Someone merged (or closed) the MR in the browser after
        // classification: the re-fetch sees the state change.
        $this->storePassingLocal();
        $client = $this->client([
            '/merge_requests/5/merge' => self::json(['message' => 'must not be called'], 500),
            '/merge_requests/5' => self::queue(
                self::json(self::botMrPayload(5, ['head_pipeline' => self::greenPipeline()])),
                self::json(self::botMrPayload(5, [
                    'state' => 'merged',
                    'head_pipeline' => self::greenPipeline(),
                ])),
            ),
            '/merge_requests?' => self::json([self::botMrPayload(5)]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runMerge($client, ['merge']);

        $tester->assertCommandIsSuccessful();
        self::assertSame([], $this->putRequests(), 'a no-longer-open MR must never be merged');
        $display = $tester->getDisplay();
        self::assertStringContainsString('state-changed:merged', $display);
        self::assertStringContainsString('Demoted: 1', $display);
        self::assertStringContainsString('Merged: 0', $display);
    }

    public function testNewDraftMarkerAtFreshnessRecheckDemotesInsteadOfMerging(): void
    {
        $this->storePassingLocal();
        $client = $this->client([
            '/merge_requests/5/merge' => self::json(['message' => 'must not be called'], 500),
            '/merge_requests/5' => self::queue(
                self::json(self::botMrPayload(5, ['head_pipeline' => self::greenPipeline()])),
                self::json(self::botMrPayload(5, [
                    'draft' => true,
                    'head_pipeline' => self::greenPipeline(),
                ])),
            ),
            '/merge_requests?' => self::json([self::botMrPayload(5)]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runMerge($client, ['merge']);

        $tester->assertCommandIsSuccessful();
        self::assertSame([], $this->putRequests(), 'a re-drafted MR must never be merged');
        $display = $tester->getDisplay();
        self::assertStringContainsString('draft', $display);
        self::assertStringContainsString('Demoted: 1', $display);
        self::assertStringContainsString('Merged: 0', $display);
    }

    public function testFailedFreshnessRefetchDemotesInsteadOfMerging(): void
    {
        // If we cannot re-verify freshness, we do not merge — the unknown is
        // treated exactly as conservatively as at classification time.
        $this->storePassingLocal();
        $client = $this->client([
            '/merge_requests/5/merge' => self::json(['message' => 'must not be called'], 500),
            '/merge_requests/5' => self::queue(
                self::json(self::botMrPayload(5, ['head_pipeline' => self::greenPipeline()])),
                self::json(['message' => '403 Forbidden'], 403),
            ),
            '/merge_requests?' => self::json([self::botMrPayload(5)]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runMerge($client, ['merge']);

        $tester->assertCommandIsSuccessful();
        self::assertSame([], $this->putRequests(), 'no re-verification, no merge');
        $display = $tester->getDisplay();
        self::assertStringContainsString('freshness re-check failed', $display);
        self::assertStringContainsString('Demoted: 1', $display);
        self::assertStringContainsString('Merged: 0', $display);
    }

    public function testEndpointClosedOnMergePrintsExactBrowserUrlAndMarksHandledManually(): void
    {
        // The documented degraded path on the block-by-default instance: the
        // approved action hands the exact browser merge URL to the human.
        $this->storePassingLocal();
        $routes = $this->readyAutoRoutes();
        $routes['/merge_requests/5/merge'] = self::json(['message' => '403 Forbidden'], 403);

        $tester = $this->runMerge($this->client($routes), ['merge']);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString(
            'Merge in the browser: https://git.drupalcode.org/project/widget/-/merge_requests/5',
            $display,
        );
        self::assertStringContainsString('handled-manually', $display);
        self::assertStringContainsString('Handed to browser: 1', $display);
        self::assertStringContainsString('Merged: 0', $display);
        self::assertStringContainsString('Failed: 0', $display, 'EndpointClosed is a documented path, not a failure');
    }

    public function testMergeFailureIsReportedPerMrAndTheLoopContinues(): void
    {
        // First MR's merge is rejected (405 branch-cannot-be-merged); the
        // loop reports it with the reason and still offers the second MR.
        $this->storePassingLocal(5);
        $this->storePassingLocal(7);
        $client = $this->client([
            '/merge_requests/5/merge' => self::json(['message' => '405 Method Not Allowed'], 405),
            '/merge_requests/7/merge' => self::json(self::botMrPayload(7, ['state' => 'merged'])),
            '/merge_requests/5' => self::json(self::botMrPayload(5, ['head_pipeline' => self::greenPipeline()])),
            '/merge_requests/7' => self::json(self::botMrPayload(7, ['head_pipeline' => self::greenPipeline()])),
            '/merge_requests?' => self::json([self::botMrPayload(5), self::botMrPayload(7)]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runMerge($client, ['merge', 'merge']);

        // BP-CMD-04: a run in which a merge failed must not exit 0.
        self::assertSame(ExitCode::FAILED, $tester->getStatusCode());
        $display = $tester->getDisplay();
        self::assertStringContainsString('Merge failed for widget !5', $display);
        self::assertStringContainsString('405', $display);
        self::assertStringContainsString(
            'Fast-lane action for widget !7',
            $display,
            'a failure must not abort the loop',
        );
        self::assertStringContainsString('Merged widget !7', $display);
        self::assertStringContainsString('Merged: 1', $display);
        self::assertStringContainsString('Failed: 1', $display);
        self::assertCount(2, $this->putRequests());
    }

    public function testHelpOffersNoBypassFlagAndDefinitionHasNoMergeWithoutPromptOption(): void
    {
        $command = new MergeCommand();

        // The complete option surface: nothing that could merge without the
        // per-MR prompt.
        $options = array_keys($command->getDefinition()->getOptions());
        sort($options);
        self::assertSame(['cockpit', 'fast-lane'], $options);

        // And the rendered `merge --help` text offers no bypass vocabulary.
        $application = new Application('Upkeep', 'test');
        $application->setAutoExit(false);
        $application->addCommands([$command]);
        $help = new ApplicationTester($application);
        $help->run(['command' => 'merge', '--help' => true]);
        $display = $help->getDisplay();
        self::assertStringContainsString('--fast-lane', $display);
        self::assertStringContainsString('--cockpit', $display);
        foreach (['--yes', '--force', '--all', '--batch', '--no-prompt', '--auto', '--non-interactive'] as $bypass) {
            self::assertStringNotContainsString($bypass, $display, 'no flag may merge without prompting');
        }
    }

    public function testNonInteractiveRunNeverMergesAndSaysApprovalRequiresATerminal(): void
    {
        // Without a terminal there can be no explicit affirmative, so no
        // code path may reach the merge call.
        $this->storePassingLocal();
        $tester = $this->runMerge($this->client($this->readyAutoRoutes()), [], false);

        $tester->assertCommandIsSuccessful();
        self::assertSame([], $this->putRequests(), 'non-interactive runs must never write');
        $display = $tester->getDisplay();
        self::assertStringContainsString('interactive', $display);
        self::assertStringContainsString('Merged: 0', $display);
        self::assertStringContainsString('Skipped: 1', $display);
    }
    /**
     * A rejected credential is the operator's setup, not a verdict about any
     * one merge request: it takes the infrastructure code, which outranks a
     * merge failure.
     */
    public function testARejectedCredentialExitsWithTheInfrastructureCode(): void
    {
        $this->storePassingLocal(5);
        $client = $this->client([
            '/merge_requests/5/merge' => self::json(['message' => '401 Unauthorized'], 401),
            '/merge_requests/5' => self::json(self::botMrPayload(5, ['head_pipeline' => self::greenPipeline()])),
            '/merge_requests?' => self::json([self::botMrPayload(5)]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runMerge($client, ['merge']);

        self::assertSame(ExitCode::INFRASTRUCTURE, $tester->getStatusCode());
        self::assertStringContainsString('Merge failed for widget !5', $tester->getDisplay());
    }

    /** Bad usage attempts nothing, so it is an infrastructure outcome, not a verdict. */
    public function testOmittingFastLaneIsAnInfrastructureFailure(): void
    {
        $tester = new CommandTester(new MergeCommand());
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('only operates in fast-lane mode', $tester->getDisplay());
    }

    /**
     * A module GitLab cannot even be asked about produces a row with no merge
     * request behind it. It has to be listed under "needs a human" — silently
     * dropping it would let a module quietly fall out of the fast lane and
     * look, run after run, as though it simply had nothing open — and it must
     * never be offered for merge, because there is nothing to merge.
     */
    public function testAModuleWhoseMergeRequestsAreUnavailableIsListedAndNeverOffered(): void
    {
        $client = $this->client([
            '/projects/project%2Fwidget' => self::json(['message' => '403 Forbidden'], 403),
        ]);

        $tester = $this->runMerge($client);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('widget: merge requests unavailable — n/a (403)', $display);
        self::assertStringContainsString('No READY-AUTO rows', $display);
        self::assertStringNotContainsString('Fast-lane action', $display, 'no prompt may be offered');
        self::assertSame([], $this->putRequests(), 'no API write may happen');
    }

    public function testACleanRunWithNothingEligibleExitsOk(): void
    {
        $client = $this->client([
            '/merge_requests?' => self::json([]),
            '/projects/project%2Fwidget' => self::json(self::projectPayload()),
        ]);

        $tester = $this->runMerge($client);

        self::assertSame(ExitCode::OK, $tester->getStatusCode());
    }
}

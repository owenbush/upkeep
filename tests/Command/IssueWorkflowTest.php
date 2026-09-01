<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\Environment;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Workflow\ExitCode;

/**
 * The loop the tool was missing: see an issue, start work on it, publish it.
 *
 * Every other verb begins at a *contribution* — a merge request, or a patch
 * somebody already posted — which left the half of the job where a maintainer
 * writes the fix happening somewhere else entirely. What `publish` pushes
 * re-enters the pipeline that already existed, as an ordinary MR.
 */
final class IssueWorkflowTest extends TestCase
{
    private const PROJECT_NID = 12345;

    private ?CliHarness $cli = null;

    protected function tearDown(): void
    {
        $this->cli?->destroy();
    }

    private function cli(): CliHarness
    {
        if ($this->cli === null) {
            $this->cli = CliHarness::create('issue-work');
            $this->cli->registerModule('widget');
        }

        return $this->cli;
    }

    private static function environment(): Environment
    {
        return new Environment('widget', '11', 'widget-11', '/tmp/projects/widget-11', 'https://w.test', false);
    }

    /** @param array<array-key, mixed> $payload */
    private static function json(array $payload, int $status = 200): MockResponse
    {
        return new MockResponse(json_encode($payload, \JSON_THROW_ON_ERROR), [
            'http_code' => $status,
            'response_headers' => ['content-type' => 'application/json'],
        ]);
    }

    /**
     * @param list<array<string, mixed>> $issues one entry per status page
     */
    private function withIssues(array $issues, int $singleNid = 3223746, string $title = 'Fix the thing'): CliHarness
    {
        return $this->cli()->withDrupalOrg(new DrupalOrgClient(new MockHttpClient(
            static function (string $method, string $url) use ($issues, $singleNid, $title): MockResponse {
                if (str_contains($url, 'field_project_machine_name=')) {
                    return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
                }
                if (str_contains($url, '/node/')) {
                    return self::json([
                        'nid' => $singleNid,
                        'title' => $title,
                        'url' => 'https://www.drupal.org/node/' . $singleNid,
                        'field_issue_status' => '1',
                        'field_project' => ['machine_name' => 'widget'],
                    ]);
                }

                // Filtered by the requested status, as api-d7 does: a fixture
                // that answered every status with everything would prove
                // nothing about which statuses the command actually asks for.
                preg_match('/field_issue_status=(\d+)/', $url, $m);
                $wanted = $m[1] ?? '';

                return self::json(['list' => array_values(array_filter(
                    $issues,
                    static fn (array $i): bool => ($i['field_issue_status'] ?? null) === $wanted,
                ))]);
            },
        )));
    }

    /**
     * @param array<string, mixed> $overrides
     *
     * @return array<string, mixed>
     */
    private static function issue(int $nid, string $status, array $overrides = []): array
    {
        return $overrides + [
            'nid' => $nid,
            'title' => 'Issue ' . $nid,
            'url' => 'https://www.drupal.org/node/' . $nid,
            'field_issue_status' => $status,
            'field_issue_priority' => '200',
            'field_project' => ['machine_name' => 'widget'],
        ];
    }

    // --------------------------------------------------------------- issues

    /**
     * The regression that motivated all of this: the contribution-shaped scan
     * saw Needs Review and RTBC only, which on a real module is under half the
     * open queue and excludes Active entirely — where new work begins.
     */
    public function testEveryOpenStatusIsListedNotJustTheContributionShapedTwo(): void
    {
        $cli = $this->withIssues([
            self::issue(1001, '1'),     // Active
            self::issue(1002, '8'),     // Needs review
            self::issue(1003, '13'),    // Needs work
            self::issue(1004, '14'),    // RTBC
            self::issue(1005, '4'),     // Postponed
            self::issue(1006, '7'),     // Closed (fixed) — not open
        ]);

        $exit = $cli->run('issues', 'widget');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        $display = $cli->display();
        foreach ([1001, 1002, 1003, 1004, 1005] as $nid) {
            self::assertStringContainsString('#' . $nid, $display, "issue {$nid} should be listed");
        }
        self::assertStringNotContainsString('#1006', $display, 'a closed issue is not open work');
    }

    /**
     * An issue nobody has contributed to is the most actionable row on the
     * list — it is unclaimed work — so it is named rather than left blank.
     */
    public function testAnIssueWithNoContributionIsMarkedUnclaimed(): void
    {
        $cli = $this->withIssues([self::issue(1001, '1')]);

        $cli->run('issues', 'widget');

        self::assertStringContainsString('unclaimed', $cli->display());
        self::assertStringContainsString('1 unclaimed', $cli->display());
    }

    public function testUnclaimedNarrowsToWorkNobodyHasStarted(): void
    {
        $cli = $this->withIssues([
            self::issue(1001, '1'),
            self::issue(1002, '8', ['field_issue_files' => [['file' => [
                'filename' => 'a.patch',
                'url' => 'https://www.drupal.org/files/issues/a.patch',
                'filesize' => '10',
                'timestamp' => '1705400000',
            ]]]]),
        ]);

        $cli->run('issues', 'widget', '--unclaimed');

        self::assertStringContainsString('#1001', $cli->display());
        self::assertStringNotContainsString('#1002', $cli->display(), 'it has a patch');
    }

    /**
     * The counts underneath are the point of the view, so they say which of
     * them is waiting on the maintainer rather than only how many there are.
     */
    public function testTheSummaryDistinguishesWhatAwaitsTheMaintainer(): void
    {
        $cli = $this->withIssues([
            self::issue(1001, '1'),     // Active — waiting on nobody
            self::issue(1002, '8'),     // Needs review — waiting on you
            self::issue(1003, '14'),    // RTBC — waiting on you
        ]);

        $cli->run('issues', 'widget');

        self::assertStringContainsString('3 open issues', $cli->display());
        self::assertStringContainsString('2 awaiting you', $cli->display());
    }

    /**
     * A cockpit that has never refreshed still gets its issues — and is told
     * why the contribution column is empty, rather than being left to read it
     * as "no merge requests exist".
     */
    public function testAnUnrefreshedCockpitStillListsIssuesAndSaysWhyMrsAreMissing(): void
    {
        $cli = $this->withIssues([self::issue(1001, '8')]);

        $cli->run('issues', 'widget');

        self::assertStringContainsString('#1001', $cli->display());
        self::assertStringContainsString('no cached MRs', $cli->display());
    }

    public function testAnUnregisteredModuleIsRefused(): void
    {
        $cli = $this->cli();

        self::assertSame(ExitCode::INFRASTRUCTURE, $cli->run('issues', 'nope'));
        self::assertStringContainsString('not registered', $cli->display());
    }

    // ---------------------------------------------------------------- start

    public function testStartingWorkProvisionsAndOpensAnIssueForkBranch(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssues([], 3223746, 'Add an option to disable auto-updating');
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->runSplittingStreams('start', 'widget', '3223746', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $cli->display() . $cli->errorDisplay());
        self::assertSame(['3223746-add-an-option-to-disable-auto-updating'], $engine->startedBranches);
        // The base is the working copy's own, resolved by the adapter.
        self::assertSame(['(from working copy)'], $engine->startedBases);
        // stdout is the path alone, so `cd $(upkeep start ...)` works.
        self::assertSame('/tmp/projects/widget-11', trim($cli->display()));
        self::assertStringContainsString('Started', $cli->errorDisplay());
    }

    public function testResumingSaysSoRatherThanClaimingToHaveStarted(): void
    {
        $engine = FakeEngineAdapter::resumingWork(self::environment());
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()->withEngine($engine);

        $cli->runSplittingStreams('start', 'widget', '3223746', '--version=11');

        self::assertStringContainsString('Resumed', $cli->errorDisplay());
        self::assertStringNotContainsString('Started', $cli->errorDisplay());
    }

    public function testTheBranchAndBaseCanBeNamedExplicitly(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()->withEngine($engine);

        $cli->runSplittingStreams(
            'start',
            'widget',
            '3223746',
            '--version=11',
            '--branch=my-own-name',
            '--base=2.0.x',
        );

        self::assertSame(['my-own-name'], $engine->startedBranches);
        self::assertSame(['2.0.x'], $engine->startedBases);
    }

    public function testStartingRefusesADirtyWorkingCopyRatherThanMixingWork(): void
    {
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()->withEngine(FakeEngineAdapter::withFailingWorkStart(
            self::environment(),
            new AdapterException('Cannot start work on "3223746-fix-the-thing": uncommitted changes'),
        ));

        $exit = $cli->run('start', 'widget', '3223746', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('uncommitted changes', $cli->display());
    }

    /** @return iterable<string, array{string}> */
    public static function badIssueArguments(): iterable
    {
        yield 'not a number' => ['abc'];
        yield 'zero' => ['0'];
        yield 'a decimal' => ['1.5'];
    }

    #[DataProvider('badIssueArguments')]
    public function testTheIssueArgumentMustBeANodeId(string $raw): void
    {
        $cli = $this->cli();

        self::assertSame(ExitCode::INFRASTRUCTURE, $cli->run('start', 'widget', $raw));
        self::assertStringContainsString('must be a drupal.org issue node id', $cli->display());
    }

    public function testAnUnreadableIssueIsRefusedBeforeAnythingIsBuilt(): void
    {
        $cli = $this->cli()
            ->withDrupalOrg(new DrupalOrgClient(new MockHttpClient(
                static fn (): MockResponse => new MockResponse('', ['http_code' => 404]),
            )))
            ->withEngine(FakeEngineAdapter::failing(new AdapterException('must not be provisioned')));

        $exit = $cli->run('start', 'widget', '3223746');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('could not be read', $cli->display());
    }

    // -------------------------------------------------------------- publish

    /**
     * @param array<string, MockResponse> $routes substring => response
     */
    private function gitlab(array $routes): GitlabClient
    {
        $project = [
            'id' => 42,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ];

        return new GitlabClient(
            new MockHttpClient(static function (string $method, string $url) use ($routes, $project): MockResponse {
                foreach ($routes as $needle => $response) {
                    if (str_contains($url, $needle)) {
                        return $response;
                    }
                }

                return self::json($project);
            }),
            'test-token',
        );
    }

    private static function mrPayload(int $iid, string $title): MockResponse
    {
        return self::json([
            'iid' => $iid,
            'title' => $title,
            'state' => 'opened',
            'author' => ['username' => 'owen', 'id' => 1],
            'source_branch' => '3223746-fix-the-thing',
            'target_branch' => '11',
            'sha' => str_repeat('a', 40),
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/' . $iid,
        ]);
    }

    /**
     * What publish pushes re-enters the pipeline that already exists: an
     * ordinary merge request the dashboard, check and merge already handle.
     */
    public function testPublishingPushesTheBranchAndOpensAMergeRequest(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()->withEngine($engine)->withGitlab($this->gitlab([
            // No MR open for the branch yet, then the created one.
            'source_branch=' => self::json([]),
            'merge_requests' => self::mrPayload(19, 'Issue #3223746: Fix the thing'),
        ]));

        $exit = $cli->run('publish', 'widget', '3223746', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertSame(['3223746-fix-the-thing'], $engine->pushedBranches);
        self::assertStringContainsString('Opened !19', $cli->display());
        // Named so the tool's own reference parsing links it to the issue.
        self::assertStringContainsString('upkeep check widget 19', $cli->display());
    }

    /**
     * Re-running after more commits is the normal way to update an MR — the
     * push already moved it — so the existing one is reported, not duplicated.
     */
    public function testRepublishingReportsTheExistingMergeRequestRatherThanDuplicatingIt(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()->withEngine($engine)->withGitlab($this->gitlab([
            'source_branch=' => self::json([[
                'iid' => 19,
                'title' => 'Issue #3223746: Fix the thing',
                'state' => 'opened',
                'author' => ['username' => 'owen', 'id' => 1],
                'source_branch' => '3223746-fix-the-thing',
                'target_branch' => '11',
                'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/19',
            ]]),
        ]));

        $exit = $cli->run('publish', 'widget', '3223746', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertStringContainsString('Updated the open merge request', $cli->display());
        self::assertStringContainsString('!19', $cli->display());
    }

    /**
     * The branch is pushed before the MR is opened, so a failure to open one
     * must not read as a failure to push — the work is safe on origin either
     * way, and the operator needs to know that.
     */
    public function testAFailureToOpenTheMergeRequestStillReportsThatThePushSucceeded(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()->withEngine($engine)->withGitlab($this->gitlab([
            'source_branch=' => self::json([]),
            'merge_requests' => self::json(['message' => 'refused'], 403),
        ]));

        $exit = $cli->run('publish', 'widget', '3223746', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertSame(['3223746-fix-the-thing'], $engine->pushedBranches, 'the push still happened');
        self::assertStringContainsString('branch was pushed', $cli->display());
        self::assertStringContainsString('merge_requests/new', $cli->display(), 'and where to finish by hand');
    }

    public function testPublishingRefusesWhenTheBranchCannotBePushed(): void
    {
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()
            ->withEngine(FakeEngineAdapter::withFailingPush(
                self::environment(),
                new AdapterException('the remote moved'),
            ))
            ->withGitlab($this->gitlab([]));

        $exit = $cli->run('publish', 'widget', '3223746', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('the remote moved', $cli->display());
    }

    /**
     * Publishing proposes work for review, which is the opposite of the risk
     * the one-approval-per-merge stance exists to manage. It must never merge.
     */
    public function testPublishingNeverMerges(): void
    {
        $methods = [];
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()->withEngine($engine)->withGitlab(new GitlabClient(
            new MockHttpClient(static function (string $method, string $url) use (&$methods): MockResponse {
                $methods[] = $method . ' ' . $url;
                if (str_contains($url, 'source_branch=')) {
                    return self::json([]);
                }
                if (str_contains($url, 'merge_requests')) {
                    return self::mrPayload(19, 'Issue #3223746: Fix the thing');
                }

                return self::json(['id' => 42, 'path_with_namespace' => 'project/widget', 'path' => 'widget']);
            }),
            'test-token',
        ));

        $cli->run('publish', 'widget', '3223746', '--version=11');

        self::assertNotSame([], $methods);
        foreach ($methods as $call) {
            // The merge endpoint is /merge_requests/<iid>/merge — matched
            // precisely, because /merge_requests trivially contains "/merge".
            self::assertDoesNotMatchRegularExpression(
                '#/merge_requests/\d+/merge$#',
                $call,
                'publish must never call the merge endpoint',
            );
            self::assertStringStartsNotWith('PUT ', $call, 'publish only ever reads or POSTs a proposal');
        }
    }

    public function testAStatusFilterNarrowsToThatStatusAlone(): void
    {
        $cli = $this->withIssues([self::issue(1001, '1'), self::issue(1002, '8')]);

        $cli->run('issues', 'widget', '--status=active');

        self::assertStringContainsString('#1001', $cli->display());
        self::assertStringNotContainsString('#1002', $cli->display());
    }

    /** An unrecognised status falls back to everything open, not to nothing. */
    public function testAnUnknownStatusFilterShowsEverythingRatherThanNothing(): void
    {
        $cli = $this->withIssues([self::issue(1001, '1'), self::issue(1002, '8')]);

        $cli->run('issues', 'widget', '--status=nonsense');

        self::assertStringContainsString('#1001', $cli->display());
        self::assertStringContainsString('#1002', $cli->display());
    }

    public function testAModuleWithNothingOpenSaysSo(): void
    {
        $cli = $this->withIssues([]);

        self::assertSame(ExitCode::OK, $cli->run('issues', 'widget'));
        self::assertStringContainsString('No matching open issues', $cli->display());
    }

    /** Priority is what a maintainer triages by, so it is coloured. */
    public function testPriorityIsShownAndCriticalWorkIsHighlighted(): void
    {
        $cli = $this->withIssues([
            self::issue(1001, '1', ['field_issue_priority' => '400']),
            self::issue(1002, '1', ['field_issue_priority' => null]),
        ]);

        $cli->run('issues', 'widget');

        self::assertStringContainsString('Critical', $cli->display());
        self::assertStringContainsString('–', $cli->display(), 'an unset priority is a dash');
    }

    /** A contribution of both kinds is reported as both. */
    public function testAnIssueCarryingBothAPatchAndAnMrSaysSo(): void
    {
        $cli = $this->withIssues([self::issue(3223746, '8', ['field_issue_files' => [['file' => [
            'filename' => 'a.patch',
            'url' => 'https://www.drupal.org/files/issues/a.patch',
            'filesize' => '10',
            'timestamp' => '1705400000',
        ]]]])]);

        $this->cli()->saveSnapshotWithMrs('widget', [[
            'iid' => 7,
            'title' => 'Issue #3223746: a change',
            'state' => 'opened',
            'author' => ['username' => 'alice', 'id' => 1],
            'source_branch' => '3223746-fix',
            'target_branch' => '1.0.x',
            'sha' => str_repeat('a', 40),
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/7',
            'diff_refs' => ['base_sha' => 'a', 'head_sha' => 'b'],
        ]]);

        $cli->run('issues', 'widget');

        self::assertStringContainsString('!7, 1 patch', $cli->display());
    }

    // ------------------------------------------------- publish, the edges

    public function testPublishingRefusesAnIssueArgumentThatIsNotANodeId(): void
    {
        $cli = $this->cli();

        self::assertSame(ExitCode::INFRASTRUCTURE, $cli->run('publish', 'widget', 'abc'));
        self::assertStringContainsString('must be a drupal.org issue node id', $cli->display());
    }

    public function testPublishingRefusesWhenTheProjectCannotBeResolved(): void
    {
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()
            ->withEngine(FakeEngineAdapter::withEnvironment(self::environment()))
            ->withGitlab($this->gitlab(['/projects/' => self::json(['message' => 'gone'], 404)]));

        $exit = $cli->run('publish', 'widget', '3223746', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('Cannot resolve the GitLab project', $cli->display());
    }

    public function testPublishingReportsWhenTheBranchesMergeRequestsCannotBeListed(): void
    {
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()
            ->withEngine(FakeEngineAdapter::withEnvironment(self::environment()))
            ->withGitlab($this->gitlab(['source_branch=' => self::json(['message' => 'refused'], 403)]));

        $exit = $cli->run('publish', 'widget', '3223746', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('could not be listed', $cli->display());
    }

    /** A listing that is not a list of objects is a protocol fault, not a miss. */
    public function testAMalformedMergeRequestListingIsReportedRatherThanRead(): void
    {
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()
            ->withEngine(FakeEngineAdapter::withEnvironment(self::environment()))
            ->withGitlab($this->gitlab(['source_branch=' => self::json(['not' => 'a list'])]));

        $exit = $cli->run('publish', 'widget', '3223746', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('could not be listed', $cli->display());
    }

    public function testTheBranchAndTitleCanBeNamedAndDraftedExplicitly(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()->withEngine($engine)->withGitlab($this->gitlab([
            'source_branch=' => self::json([]),
            'merge_requests' => self::mrPayload(19, 'Draft: My own title'),
        ]));

        $exit = $cli->run(
            'publish',
            'widget',
            '3223746',
            '--version=11',
            '--branch=my-own-name',
            '--title=My own title',
            '--draft',
            '--target=2.0.x',
        );

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertSame(['my-own-name'], $engine->pushedBranches);
    }

    public function testPublishingWithoutATokenIsAnInfrastructureOutcome(): void
    {
        $this->withIssues([], 3223746, 'Fix the thing');
        $cli = $this->cli()->withEngine(FakeEngineAdapter::withEnvironment(self::environment()));

        $exit = $cli->run('publish', 'widget', '3223746', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('No GitLab token found', $cli->display());
    }

    public function testPublishingRefusesWhenTheIssueCannotBeRead(): void
    {
        $cli = $this->cli()
            ->withDrupalOrg(new DrupalOrgClient(new MockHttpClient(
                static fn (): MockResponse => new MockResponse('', ['http_code' => 404]),
            )))
            ->withEngine(FakeEngineAdapter::withEnvironment(self::environment()))
            ->withGitlab($this->gitlab([]));

        $exit = $cli->run('publish', 'widget', '3223746', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('could not be read', $cli->display());
    }
}

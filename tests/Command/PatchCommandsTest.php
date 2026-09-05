<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Adapter\BaseRefresh;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Adapter\Environment;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Patches\PatchRevision;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Workflow\ExitCode;

/**
 * `patch:apply` and `patch:check` end to end through the console.
 *
 * The point of these commands is that a patch contribution should cost a
 * maintainer what a merge request costs, so what is pinned here is the whole
 * resolution chain a maintainer actually drives: naming an issue, choosing
 * among its patches, and getting a verdict — plus the two places where
 * choosing wrongly would be expensive.
 */
final class PatchCommandsTest extends TestCase
{
    private const DIFF = "diff --git a/widget.module b/widget.module\n"
        . "--- a/widget.module\n+++ b/widget.module\n@@ -1 +1 @@\n-old\n+new\n";

    private ?CliHarness $cli = null;

    protected function tearDown(): void
    {
        $this->cli?->destroy();
    }

    private function cli(): CliHarness
    {
        if ($this->cli === null) {
            $this->cli = CliHarness::create('patch');
            $this->cli->registerModule('widget');
        }

        return $this->cli;
    }

    private static function environment(): Environment
    {
        return new Environment(
            moduleName: 'widget',
            coreMajor: '11',
            projectName: 'widget-11',
            projectPath: '/tmp/projects/widget-11',
            primaryUrl: 'https://widget-11.ddev.site',
            reused: false,
        );
    }

    private static function greenRun(): CheckRunResult
    {
        return new CheckRunResult([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'ok', 1.0),
            new CheckResult(CheckType::PhpCs, CheckStatus::Passed, 0, 'ok', 0.5),
        ]);
    }

    private static function redRun(): CheckRunResult
    {
        return new CheckRunResult([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'ok', 1.0),
            new CheckResult(CheckType::PhpCs, CheckStatus::Failed, 1, 'widget.module line 12: bad spacing', 0.5),
        ]);
    }

    /**
     * @param list<array{string, int}> $patches filename and upload timestamp
     *
     * @return array<string, mixed>
     */
    private static function issuePayload(array $patches, int $nid = 3597808, ?string $version = null): array
    {
        $files = [];
        foreach ($patches as $index => [$name, $timestamp]) {
            $files[] = ['file' => [
                'fid' => (string) (7000 + $index),
                'name' => $name,
                'url' => 'https://www.drupal.org/files/issues/' . $name,
                'timestamp' => (string) $timestamp,
            ]];
        }

        return [
            'nid' => $nid,
            'field_issue_version' => $version,
            'title' => 'Automated Drupal 12 compatibility fixes',
            'url' => 'https://www.drupal.org/node/' . $nid,
            'field_issue_status' => '8',
            'field_project' => ['machine_name' => 'widget'],
            'field_issue_files' => $files,
        ];
    }

    /**
     * @param list<array{string, int}> $patches
     */
    private function withIssue(array $patches, int $nid = 3597808, ?string $version = null): CliHarness
    {
        $payload = self::issuePayload($patches, $nid, $version);

        return $this->cli()->withDrupalOrg(new DrupalOrgClient(new MockHttpClient(
            static fn (): MockResponse => new MockResponse(
                json_encode($payload, \JSON_THROW_ON_ERROR),
                ['response_headers' => ['content-type' => 'application/json']],
            ),
        )));
    }

    private function withDownload(string $body = self::DIFF): CliHarness
    {
        return $this->cli()->withPatchDownloader(new MockHttpClient(
            static fn (): MockResponse => new MockResponse($body),
        ));
    }

    // ------------------------------------------------- the issue's version

    /**
     * A GitLab client answering with the project and its branches.
     *
     * @param list<string> $branches
     */
    private function withBranches(array $branches): CliHarness
    {
        $project = [
            'id' => 42,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
            'default_branch' => '1.0.x',
        ];
        $rows = array_map(static fn (string $b): array => ['name' => $b], $branches);

        return $this->cli()->withGitlab(new GitlabClient(
            new MockHttpClient(
                static function (string $method, string $url) use ($project, $rows): MockResponse {
                    $body = str_contains($url, '/repository/branches') ? $rows : $project;

                    return new MockResponse(
                        json_encode($body, \JSON_THROW_ON_ERROR),
                        ['response_headers' => ['content-type' => 'application/json']],
                    );
                },
            ),
            'test-token',
        ));
    }

    /**
     * The bug this closes: an issue filed against 2.0.0 carries patches cut
     * from 2.0.x, and upkeep applied them to whatever the clone had checked
     * out — 1.0.x — then reported "does not apply to 1.0.x". True, and
     * useless: the patch was never meant for 1.0.x.
     */
    public function testThePatchIsAppliedOntoTheBranchTheIssueIsFiledAgainst(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]], version: '2.0.0');
        $this->withDownload();
        $this->withBranches(['1.0.x', '2.0.x']);
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertSame('2.0.x', $engine->appliedPatches[0]->baseBranch);
        self::assertStringContainsString('Base branch: 2.0.x', $cli->display());
    }

    /**
     * The base is brought up to date by default, and `--no-update` is the only
     * way out of it.
     *
     * The default is the fix. A working copy is cloned once and its base then
     * sits still while drupal.org's moves on, and CI does not test your branch
     * — it tests your branch merged into the current tip of the target. A
     * check against a stale base can pass while CI fails, with nothing
     * anywhere saying the two looked at different code.
     */
    public function testTheBaseIsRefreshedByDefaultAndOnlyTheFlagOptsOut(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]], version: '2.0.0');
        $this->withDownload();
        $this->withBranches(['1.0.x', '2.0.x']);
        $cli = $this->cli()->withEngine($engine);

        self::assertSame(ExitCode::OK, $cli->run('patch:check', 'widget', '3597808', '--version=11'));
        self::assertSame([BaseRefresh::Update], $engine->baseRefreshes);
    }

    public function testNoUpdateReachesTheAdapterRatherThanBeingAcceptedAndIgnored(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]], version: '2.0.0');
        $this->withDownload();
        $this->withBranches(['1.0.x', '2.0.x']);
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11', '--no-update');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertSame([BaseRefresh::Skip], $engine->baseRefreshes);
    }

    /**
     * A version naming no real branch is said out loud and falls back. It is
     * not an error — plenty of issues carry a version nobody maintained, and
     * `x.y.z` appears in the wild by the dozen.
     */
    public function testAVersionMatchingNoBranchSaysSoAndFallsBack(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]], version: 'x.y.z');
        $this->withDownload();
        $this->withBranches(['1.0.x', '2.0.x']);
        $cli = $this->cli()->withEngine($engine);

        self::assertSame(ExitCode::OK, $cli->run('patch:check', 'widget', '3597808', '--version=11'));
        self::assertNull($engine->appliedPatches[0]->baseBranch, 'the adapter resolves it from the working copy');
        self::assertStringContainsString('matches no branch', $cli->display());
    }

    /**
     * No version on the issue asks GitLab nothing at all — the patch surface
     * stays usable without a token, and a lookup that cannot change the answer
     * is not worth a request.
     */
    public function testAnIssueWithNoVersionResolvesNothingAndAsksNobody(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload();
        $asked = false;
        $this->cli()->withGitlab(new GitlabClient(
            new MockHttpClient(static function () use (&$asked): MockResponse {
                $asked = true;

                return new MockResponse('{}', ['response_headers' => ['content-type' => 'application/json']]);
            }),
            'test-token',
        ));
        $cli = $this->cli()->withEngine($engine);

        self::assertSame(ExitCode::OK, $cli->run('patch:check', 'widget', '3597808', '--version=11'));
        self::assertNull($engine->appliedPatches[0]->baseBranch);
        self::assertFalse($asked, 'no version means no lookup');
    }

    /**
     * Every way the lookup can fail falls back to the working copy's base,
     * because none of them is a reason to refuse to check a patch. The point
     * of resolving the branch is to be right more often, not to add a way to
     * be stopped.
     *
     * @param callable(string): MockResponse $handler how GitLab answers
     */
    #[DataProvider('brokenGitlabAnswers')]
    public function testEveryGitlabFailureFallsBackToTheWorkingCopyBase(callable $handler): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]], version: '2.0.0');
        $this->withDownload();
        $this->cli()->withGitlab(new GitlabClient(
            new MockHttpClient(static fn (string $method, string $url): MockResponse => $handler($url)),
            'test-token',
        ));
        $cli = $this->cli()->withEngine($engine);

        self::assertSame(ExitCode::OK, $cli->run('patch:check', 'widget', '3597808', '--version=11'));
        self::assertNull($engine->appliedPatches[0]->baseBranch);
    }

    /**
     * @return iterable<string, array{callable(string): MockResponse}>
     */
    public static function brokenGitlabAnswers(): iterable
    {
        $project = static fn (): MockResponse => new MockResponse(
            json_encode([
                'id' => 42,
                'path' => 'widget',
                'path_with_namespace' => 'project/widget',
                'web_url' => 'https://git.drupalcode.org/project/widget',
            ], \JSON_THROW_ON_ERROR),
            ['response_headers' => ['content-type' => 'application/json']],
        );

        yield 'the project cannot be resolved' => [
            static fn (string $url): MockResponse => new MockResponse('', ['http_code' => 503]),
        ];

        yield 'the project resolves but its branches do not' => [
            static fn (string $url): MockResponse => str_contains($url, '/repository/branches')
                ? new MockResponse('', ['http_code' => 503])
                : $project(),
        ];

        yield 'the branch list comes back in a shape nobody expects' => [
            static fn (string $url): MockResponse => str_contains($url, '/repository/branches')
                ? new MockResponse(
                    '{"not":"a list"}',
                    ['response_headers' => ['content-type' => 'application/json']],
                )
                : $project(),
        ];
    }

    /**
     * No token, so no client: the patch surface stays usable without GitLab
     * credentials, which is most of why patches are worth supporting at all.
     */
    public function testWithoutAGitlabTokenTheLookupIsSkippedEntirely(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]], version: '2.0.0');
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        self::assertSame(ExitCode::OK, $cli->run('patch:check', 'widget', '3597808', '--version=11'));
        self::assertNull($engine->appliedPatches[0]->baseBranch);
    }

    // ----------------------------------------------------------- happy path

    public function testCheckRunsTheSuiteAgainstTheOnlyPatchOnTheIssue(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertCount(1, $engine->appliedPatches);
        self::assertSame('3597808-9-fix.patch', $engine->appliedPatches[0]->name);
        self::assertSame(3597808, $engine->appliedPatches[0]->issueNid);
        self::assertSame('patch-3597808', $engine->appliedPatches[0]->branchName());
        self::assertFileExists($engine->appliedPatches[0]->localPath);
        self::assertSame(self::DIFF, file_get_contents($engine->appliedPatches[0]->localPath));
        self::assertStringContainsString('All checks green', $cli->display());
    }

    /**
     * A failed check is a verdict on the contribution, so it exits 1 — the
     * "look at the subject" code — and names the issue, because the drupal.org
     * API is read-only and saying so is a browser action.
     */
    public function testAFailedCheckExitsOneAndPointsAtTheIssue(): void
    {
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload();
        $cli = $this->cli()->withEngine(
            FakeEngineAdapter::withPatchCheckRun(self::environment(), self::redRun()),
        );

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::FAILED, $exit, $cli->display());
        self::assertStringContainsString('1 check(s) failed', $cli->display());
        self::assertStringContainsString('bad spacing', $cli->display());
        self::assertStringContainsString('drupal.org/node/3597808', $cli->display());
    }

    public function testApplyStopsAfterApplyingAndPrintsTheEnvironmentPath(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->runSplittingStreams('patch:apply', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $cli->display() . $cli->errorDisplay());
        self::assertCount(1, $engine->appliedPatches);
        // stdout is the path alone, so `cd $(upkeep patch:apply ...)` works.
        self::assertSame('/tmp/projects/widget-11', trim($cli->display()));
        self::assertStringContainsString('patch:check widget 3597808', $cli->errorDisplay());
    }

    // ------------------------------------------------------------- choosing

    /**
     * Several patches, a terminal to ask at: the operator picks, and what
     * they picked is what gets applied — not the newest.
     */
    public function testTheOperatorIsAskedWhichPatchWhenSeveralAreAttached(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([
            ['3597808-4-first.patch', 1705000000],
            ['3597808-12-reroll.patch', 1705400000],
        ]);
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine)->setInputs(['1']);

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertStringContainsString('Which patch should be applied?', $cli->display());
        self::assertStringContainsString('[newest]', $cli->display());
        self::assertSame('3597808-4-first.patch', $engine->appliedPatches[0]->name);
    }

    /**
     * No terminal — a pipe, a cron, --no-interaction — takes the newest, and
     * says so. A scripted run that silently guessed would report a verdict on
     * a patch nobody named.
     */
    public function testANonInteractiveRunTakesTheNewestAndAnnouncesIt(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([
            ['3597808-4-first.patch', 1705000000],
            ['3597808-12-reroll.patch', 1705400000],
        ]);
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11', '--no-interaction');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertStringContainsString('taking the newest', $cli->display());
        self::assertSame('3597808-12-reroll.patch', $engine->appliedPatches[0]->name);
    }

    public function testLatestSkipsThePromptEntirely(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([
            ['3597808-4-first.patch', 1705000000],
            ['3597808-12-reroll.patch', 1705400000],
        ]);
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11', '--latest');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertStringNotContainsString('Which patch', $cli->display());
        self::assertSame('3597808-12-reroll.patch', $engine->appliedPatches[0]->name);
    }

    public function testFilePinsAnExactAttachment(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([
            ['3597808-4-first.patch', 1705000000],
            ['3597808-12-reroll.patch', 1705400000],
        ]);
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run(
            'patch:check',
            'widget',
            '3597808',
            '--version=11',
            '--file=3597808-4-first.patch',
        );

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertSame('3597808-4-first.patch', $engine->appliedPatches[0]->name);
    }

    /**
     * An unmatched --file is refused rather than quietly falling back: the
     * fallback would build an environment and report a verdict on code the
     * operator did not name.
     */
    public function testAnUnknownFileNameIsRefusedBeforeAnythingIsBuilt(): void
    {
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $cli = $this->cli()->withEngine(FakeEngineAdapter::failing(
            new AdapterException('the environment must not be built'),
        ));

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11', '--file=nope.patch');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('no patch named "nope.patch"', $cli->display());
        self::assertStringContainsString('3597808-9-fix.patch', $cli->display());
    }

    /**
     * --url covers the re-roll posted somewhere other than the issue: a fork,
     * a pipeline artefact. The issue is still named, because the branch, the
     * commit message and the report are keyed on it.
     */
    public function testAUrlOverridesTheIssuesOwnAttachments(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run(
            'patch:check',
            'widget',
            '3597808',
            '--version=11',
            '--url=https://example.test/reroll/my-fix.patch',
        );

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertSame('my-fix.patch', $engine->appliedPatches[0]->name);
        self::assertSame(3597808, $engine->appliedPatches[0]->issueNid);
    }

    /**
     * A URL that happens to be one of the issue's own attachments keeps that
     * attachment's metadata rather than degrading to a bare filename.
     */
    public function testAUrlMatchingAnAttachmentKeepsItsMetadata(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run(
            'patch:check',
            'widget',
            '3597808',
            '--version=11',
            '--url=https://www.drupal.org/files/issues/3597808-9-fix.patch',
        );

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertStringContainsString('comment 9', $cli->display());
    }

    // ------------------------------------------------------------- refusals

    public function testAnIssueWithNoPatchesIsRefused(): void
    {
        $this->withIssue([]);
        $cli = $this->cli()->withEngine(FakeEngineAdapter::withEnvironment(self::environment()));

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('no patch files attached', $cli->display());
    }

    public function testAnUnreadableIssueIsRefusedWithGuidance(): void
    {
        $cli = $this->cli()->withDrupalOrg(new DrupalOrgClient(new MockHttpClient(
            static fn (): MockResponse => new MockResponse('', ['http_code' => 404]),
        )));

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('could not be read', $cli->display());
    }

    /**
     * `-3` is absent deliberately: driven through real argv (as bin/upkeep
     * does), the console parses a leading dash as an option and refuses it
     * before the command sees an argument at all.
     *
     * @return iterable<string, array{string}>
     */
    public static function badIssueArguments(): iterable
    {
        yield 'not a number' => ['abc'];
        yield 'zero' => ['0'];
        yield 'a decimal' => ['1.5'];
        yield 'empty' => [''];
    }

    /**
     * The same rule the MR-IID argument has, for the same reason: node 0 is
     * not an issue, so it is refused rather than turned into a confusing miss.
     */
    #[\PHPUnit\Framework\Attributes\DataProvider('badIssueArguments')]
    public function testTheIssueArgumentMustBeAPositiveInteger(string $raw): void
    {
        $cli = $this->cli();

        $exit = $cli->run('patch:check', 'widget', $raw, '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('must be a drupal.org issue node id', $cli->display());
    }

    public function testAnUnregisteredModuleIsRefused(): void
    {
        $cli = $this->cli();

        $exit = $cli->run('patch:check', 'gadget', '3597808', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('not registered', $cli->display());
    }

    public function testAnUntrackedCoreVersionIsRefusedBeforeAnyNetworkCall(): void
    {
        $cli = $this->cli()->withDrupalOrg(new DrupalOrgClient(new MockHttpClient(
            static fn (): MockResponse => throw new \LogicException('drupal.org must not be consulted'),
        )));

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=9');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('does not track core version', $cli->display());
    }

    /**
     * A patch that no longer applies is an infrastructure outcome — no verdict
     * on the contribution was produced — but the message is a review finding,
     * because that is what it tells the maintainer.
     */
    public function testAPatchThatDoesNotApplyIsReportedAsNeedingAReRoll(): void
    {
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload();
        $cli = $this->cli()->withEngine(FakeEngineAdapter::withFailingPatchApply(
            self::environment(),
            new AdapterException('Patch "3597808-9-fix.patch" (issue #3597808) does not apply to "1.0.x" '
                . '— it needs a re-roll.'),
        ));

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('needs a re-roll', $cli->display());
    }

    public function testAnHtmlInterstitialIsRefusedRatherThanHandedToGit(): void
    {
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload('<!doctype html><html><body>Log in</body></html>');
        $cli = $this->cli()->withEngine(FakeEngineAdapter::withEnvironment(self::environment()));

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('does not look like a patch', $cli->display());
    }

    /**
     * A module with no test coverage, and a check the environment could not
     * offer, are neither passes nor failures — they are stated as what they
     * are and do not turn a clean patch into a red verdict.
     */
    public function testChecksWithNothingToRunAreReportedWithoutFailingThePatch(): void
    {
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload();
        $cli = $this->cli()->withEngine(FakeEngineAdapter::withPatchCheckRun(
            self::environment(),
            new CheckRunResult([
                new CheckResult(CheckType::PhpUnit, CheckStatus::NoTests, 0, '', 0.2),
                new CheckResult(CheckType::EsLint, CheckStatus::Unavailable, null, '', 0.0),
                new CheckResult(CheckType::PhpCs, CheckStatus::Passed, 0, 'ok', 0.5),
            ]),
        ));

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        $display = $cli->display();
        self::assertStringContainsString('no tests', $display);
        self::assertStringContainsString('unavailable', $display);
        // A check that never ran has no exit status to report.
        self::assertStringContainsString('-', $display);
        self::assertStringContainsString('All checks green', $display);
    }

    // --------------------------------------------------------------- caching

    /**
     * The verdict is cached where the dashboard reads it: under the patch
     * namespace, keyed by the patch's revision. The namespace is what keeps it
     * away from the fast-lane gate, which reads merge-request entries only.
     */
    public function testTheVerdictIsCachedUnderThePatchNamespace(): void
    {
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload();
        $cli = $this->cli()->withEngine(
            FakeEngineAdapter::withPatchCheckRun(self::environment(), self::redRun()),
        );

        $cli->run('patch:check', 'widget', '3597808', '--version=11');

        $cache = new ResultsCache($cli->cockpit . '/results');
        $revision = PatchRevision::of('https://www.drupal.org/files/issues/3597808-9-fix.patch');

        $cached = $cache->find('widget', ResultKey::patch(3597808), '11', $revision);
        self::assertNotNull($cached, 'the run must be recorded against the patch it checked');
        self::assertFalse($cached->result->allPassed());

        // The same number under the merge-request namespace is a different
        // subject entirely, and must not have been written.
        self::assertNull($cache->latest('widget', ResultKey::mergeRequest(3597808), '11'));
    }

    /**
     * Re-checking after a re-roll records a second entry rather than
     * overwriting the first: a different upload is a different subject, and
     * the dashboard decides which one is current.
     */
    public function testARerollIsRecordedAsItsOwnVerdict(): void
    {
        $first = 'https://www.drupal.org/files/issues/3597808-9-fix.patch';
        $second = 'https://example.test/reroll/3597808-12-fix.patch';

        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload();
        $cli = $this->cli()->withEngine(
            FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun()),
        );

        $cli->run('patch:check', 'widget', '3597808', '--version=11');
        $cli->run('patch:check', 'widget', '3597808', '--version=11', '--url=' . $second);

        $cache = new ResultsCache($cli->cockpit . '/results');
        self::assertNotNull($cache->find('widget', ResultKey::patch(3597808), '11', PatchRevision::of($first)));
        self::assertNotNull($cache->find('widget', ResultKey::patch(3597808), '11', PatchRevision::of($second)));
    }

    // -------------------------------------------------------------- fixture

    public function testAFixtureIsLoadedBetweenApplyingAndChecking(): void
    {
        $engine = FakeEngineAdapter::withPatchCheckRun(self::environment(), self::greenRun());
        $this->withIssue([['3597808-9-fix.patch', 1705400000]]);
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('patch:check', 'widget', '3597808', '--version=11', '--fixture=sample-content');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertSame(['sample-content'], $engine->loadedFixtures);
    }
}

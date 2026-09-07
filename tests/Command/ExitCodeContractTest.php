<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\VolumeProbe;
use Upkeep\Command\DevCommand;
use Upkeep\Command\EnvPathCommand;
use Upkeep\Command\ExecCommand;
use Upkeep\Command\InitCommand;
use Upkeep\Command\ModulesCommand;
use Upkeep\Command\PruneCommand;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Tests\Support\MockGitlab;
use Upkeep\Tests\Support\StubEngineAdapterFactory;
use Upkeep\Workflow\ExitCode;

/**
 * The CLI-wide exit-code contract, driven through the console.
 *
 * Every command answers with one of exactly three codes: 0 the command did
 * what was asked, 1 the work it supervised reported failure, 2 upkeep could
 * not do the job. These assertions are the contract itself — they are what
 * a script wrapping upkeep is entitled to rely on.
 */
final class ExitCodeContractTest extends TestCase
{
    private string $cockpit;

    /** Built on demand by the cases that drive whole invocations. */
    private ?CliHarness $cli = null;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-exitcode-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit . '/base-artifacts', 0o755, true);
        file_put_contents(
            $this->cockpit . '/registry.yml',
            "modules:\n  token:\n    project: project/token\n    core_versions: ['11']\n",
        );
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->cockpit));
        $this->cli?->destroy();
    }

    // --------------------------------------------------- through the console
    //
    // This section enters through the console entry point — argv parsing,
    // application wiring and all — rather than through CommandTester on one
    // constructed command, because the contract a script relies on is the
    // process exit status, and only a whole invocation produces one. The
    // per-command cases further down stay as they are: they pin failure modes
    // that need no application around them.

    /**
     * All three codes from the one command whose job is to produce a verdict:
     * green checks are 0, a red check is 1 (the supervised work failed, the
     * tool worked), and an MR that cannot be resolved is 2 (no verdict was
     * produced at all).
     */
    public function testACheckRunsVerdictIsItsExitCode(): void
    {
        $cli = $this->cli();
        $cli->registerModule('widget');
        $environment = new Environment('widget', '11', 'upkeep-widget-11', $cli->path('env'), 'https://x', true);

        $cli->withGitlab($this->openMr()->client());
        $cli->withEngine(FakeEngineAdapter::withCheckRun($environment, self::checkOutcome(CheckStatus::Passed, 0)));
        self::assertSame(ExitCode::OK, $cli->run('check', 'widget', '5'), $cli->display());
        self::assertStringContainsString('All checks green', $cli->display());

        $cli->withEngine(FakeEngineAdapter::withCheckRun($environment, self::checkOutcome(CheckStatus::Failed, 1)));
        self::assertSame(ExitCode::FAILED, $cli->run('check', 'widget', '5'), $cli->display());
        self::assertStringContainsString('1 check(s) failed', $cli->display());

        $cli->withGitlab(
            $this->openMr()
                ->route('/merge_requests/9', ['message' => '404 Not Found'], 404)
                ->client(),
        );
        self::assertSame(ExitCode::INFRASTRUCTURE, $cli->run('check', 'widget', '9'), $cli->display());
        self::assertStringContainsString('could not be resolved', $cli->display());
    }

    /**
     * "No credential" means no verdict was produced, which is 2 — everywhere.
     * It used to be 1 from six commands and 2 from check/review; a script
     * could not tell "your setup is wrong" from "the module is broken".
     *
     * @return iterable<string, array{list<string>}>
     */
    public static function commandsNeedingACredential(): iterable
    {
        yield 'api:probe' => [['api:probe', 'widget']];
        yield 'dashboard' => [['dashboard']];
        yield 'merge' => [['merge', '--fast-lane']];
        yield 'notes' => [['notes', 'widget']];
        yield 'issue' => [['issue', 'widget', '5']];
        yield 'needs-work' => [['needs-work', 'widget', '5']];
        yield 'modules:add' => [['modules:add']];
    }

    /** @param list<string> $argv */
    #[\PHPUnit\Framework\Attributes\DataProvider('commandsNeedingACredential')]
    public function testAMissingTokenIsAnInfrastructureFailure(array $argv): void
    {
        $cli = $this->cli();
        $cli->registerModule('widget');

        self::assertSame(ExitCode::INFRASTRUCTURE, $cli->run(...$argv), $cli->display());
        self::assertStringContainsString('No GitLab token found', $cli->display());
    }

    /**
     * `check` and `review` are deliberately absent from that list now.
     *
     * They only look, and drupalcode serves a public project's merge requests
     * and refs anonymously, so requiring a credential to read was upkeep's
     * restriction rather than GitLab's. They are covered where they can be
     * without a network call: GitlabClientFactoryTest for the degraded-mode
     * note, GitlabClientTest for the header and the refused writes, and
     * CommandSurfaceTest for which commands may take that path at all.
     *
     * Asserting it here would mean running a command that no longer
     * short-circuits, which is a live request to git.drupalcode.org from the
     * offline suite.
     */
    public function testReadOnlyCommandsAreNotOnTheCredentialList(): void
    {
        $needing = array_keys(iterator_to_array(self::commandsNeedingACredential()));

        self::assertNotContains('check', $needing);
        self::assertNotContains('review', $needing);
        self::assertContains('merge', $needing, 'merging still needs one');
        self::assertContains('needs-work', $needing, 'so does commenting');
    }

    /**
     * The one documented exception: `patches` without a token is a degraded
     * mode, not a failure. It says so once and still reports what it can, so
     * it exits 0 — a cron wrapper around it must not start alerting because
     * the maintainer has no PAT on that machine.
     */
    public function testPatchesWithoutATokenWarnsOnceAndStillExitsOk(): void
    {
        $cli = $this->cli();
        // Two modules: the credential guidance is resolved once for the run,
        // not re-printed for every module it could not cross-reference. Both
        // have to have something to report, because a module with no Needs
        // Review / RTBC issue never reaches for GitLab in the first place.
        $cli->registerModule('widget');
        $cli->registerModule('gadget');
        $cli->withDrupalOrg(new DrupalOrgClient(new MockHttpClient(
            static fn (): MockResponse => new MockResponse(
                json_encode(['list' => [[
                    'nid' => 3489012,
                    'title' => 'Add config schema for settings form',
                    'field_issue_status' => '8',
                ]]], \JSON_THROW_ON_ERROR),
                ['response_headers' => ['content-type' => 'application/json']],
            ),
        )));

        self::assertSame(ExitCode::OK, $cli->run('patches'), $cli->display());
        self::assertSame(
            1,
            substr_count($cli->display(), 'No GitLab token found'),
            'the degraded mode is announced once, not per module',
        );
    }

    /**
     * The merge command's two codes, from the same approved merge: 1 when
     * GitLab refused this merge (the supervised work failed — it used to exit
     * 0 unconditionally), and 2 when GitLab refused the credential, which is
     * the operator's setup and outranks any single merge request's verdict.
     *
     * Driven with a scripted "merge" answer because the exit code is only
     * reachable through the per-MR approval prompt, which is exactly what a
     * console-level harness has to be able to do.
     */
    public function testAMergeRefusedByGitlabExitsFailedAndARefusedCredentialExitsInfrastructure(): void
    {
        $cli = $this->cli();
        $cli->registerModule('widget');
        $this->cacheAPassingLocalResult($cli);

        $cli->withGitlab($this->readyAutoBotMr(405)->client());
        $cli->setInputs(['merge']);
        self::assertSame(ExitCode::FAILED, $cli->run('merge', '--fast-lane'), $cli->display());
        self::assertStringContainsString('Merge failed for widget !5', $cli->display());

        $cli->withGitlab($this->readyAutoBotMr(401)->client());
        $cli->setInputs(['merge']);
        self::assertSame(ExitCode::INFRASTRUCTURE, $cli->run('merge', '--fast-lane'), $cli->display());
    }

    /**
     * One bot merge request the fast-lane gate classifies READY-AUTO: not a
     * draft, mergeable, and with a green pipeline on its head commit — whose
     * merge endpoint answers with $mergeStatus.
     */
    private function readyAutoBotMr(int $mergeStatus): MockGitlab
    {
        $sha = 'abc123def456abc123def456abc123def456abcd';
        $mr = MockGitlab::mergeRequestPayload('widget', 5, [
            'title' => 'Automated Project Update Bot fixes',
            'author' => ['username' => 'Project-Update-Bot', 'id' => 66574],
            'source_branch' => 'project-update-bot-only',
            'sha' => $sha,
        ]);

        // Most specific first: the merge endpoint's URL contains the detail
        // endpoint's, and the first registered match wins.
        return MockGitlab::create()
            ->route('/merge_requests/5/merge', ['message' => 'refused by the test'], $mergeStatus)
            ->route('/merge_requests/5', $mr + [
                'head_pipeline' => [
                    'id' => 77,
                    'status' => 'success',
                    'sha' => $sha,
                    'web_url' => 'https://git.drupalcode.org/project/widget/-/pipelines/77',
                ],
            ])
            ->route('/merge_requests?', [$mr])
            ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget'));
    }

    private function cacheAPassingLocalResult(CliHarness $cli): void
    {
        (new ResultsCache($cli->cockpit . '/results'))->store(
            'widget',
            ResultKey::mergeRequest(5),
            '11',
            'abc123def456abc123def456abc123def456abcd',
            self::checkOutcome(CheckStatus::Passed, 0),
        );
    }

    private function cli(): CliHarness
    {
        return $this->cli ??= CliHarness::create('exitcode');
    }

    private function openMr(): MockGitlab
    {
        return MockGitlab::create()
            ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget'))
            ->route('/merge_requests/5', MockGitlab::mergeRequestPayload('widget', 5));
    }

    private static function checkOutcome(CheckStatus $status, int $exitCode): CheckRunResult
    {
        return new CheckRunResult([new CheckResult(CheckType::PhpUnit, $status, $exitCode, 'output', 0.5)]);
    }

    public function testSuccessfulWorkExitsOk(): void
    {
        $tester = new CommandTester(new ExecCommand($this->adapterFactory(sys_get_temp_dir())));
        $exit = $tester->execute([
            'module' => 'token',
            'cmd' => ['true'],
            '--cockpit' => $this->cockpit,
        ]);

        self::assertSame(ExitCode::OK, $exit);
    }

    /**
     * The behaviour change D2 mandates: `upkeep exec` no longer passes the
     * child's raw code through, so a child exiting 2 can never be mistaken
     * for an upkeep infrastructure failure.
     */
    public function testWrappedCommandFailureCollapsesToTheFailedCode(): void
    {
        foreach ([1, 2, 42, 127] as $childCode) {
            $tester = new CommandTester(new ExecCommand($this->adapterFactory(sys_get_temp_dir())));
            $exit = $tester->execute([
                'module' => 'token',
                'cmd' => ['bash', '-c', 'exit ' . $childCode],
                '--cockpit' => $this->cockpit,
            ]);

            self::assertSame(ExitCode::FAILED, $exit, sprintf('child exit %d', $childCode));
        }
    }

    public function testUnregisteredModuleIsAnInfrastructureFailure(): void
    {
        $tester = new CommandTester(new ExecCommand($this->adapterFactory(sys_get_temp_dir())));
        $exit = $tester->execute([
            'module' => 'nope',
            'cmd' => ['true'],
            '--cockpit' => $this->cockpit,
        ]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('not registered', $tester->getDisplay());
    }

    public function testUntrackedCoreVersionIsAnInfrastructureFailure(): void
    {
        $tester = new CommandTester(new DevCommand($this->adapterFactory(null)));
        $exit = $tester->execute([
            'module' => 'token',
            '--version' => '9',
            '--cockpit' => $this->cockpit,
        ]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
    }

    public function testEngineFailureIsAnInfrastructureFailure(): void
    {
        $adapter = FakeEngineAdapter::failing(new AdapterException('base artifacts missing'));
        $tester = new CommandTester(new DevCommand(new StubEngineAdapterFactory($adapter)));
        $exit = $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('base artifacts missing', $tester->getDisplay());
    }

    public function testMissingEnvironmentIsAnInfrastructureFailure(): void
    {
        $tester = new CommandTester(new EnvPathCommand($this->adapterFactory(null)));
        $exit = $tester->execute(['module' => 'token', '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
    }

    public function testMissingRegistryIsAnInfrastructureFailure(): void
    {
        $empty = $this->cockpit . '/empty';
        mkdir($empty, 0o755, true);

        $tester = new CommandTester(new ModulesCommand());
        $exit = $tester->execute(['--cockpit' => $empty]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('Module registry not found', $tester->getDisplay());
    }

    public function testMalformedRegistryIsAnInfrastructureFailure(): void
    {
        $broken = $this->cockpit . '/broken';
        mkdir($broken, 0o755, true);
        file_put_contents($broken . '/registry.yml', "modules:\n  token: [\n");

        $tester = new CommandTester(new ModulesCommand());
        $exit = $tester->execute(['--cockpit' => $broken]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
    }

    public function testBadUsageIsAnInfrastructureFailure(): void
    {
        $tester = new CommandTester(new PruneCommand(
            $this->adapterFactory(null),
            new VolumeProbe(static fn (array $command): ?string => null),
        ));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('exactly one prune scope', $tester->getDisplay());
    }

    public function testDryRunWithNothingToPruneExitsOk(): void
    {
        $tester = new CommandTester(new PruneCommand(
            $this->adapterFactory(null),
            new VolumeProbe(static fn (array $command): ?string => null),
        ));
        $exit = $tester->execute(['--all' => true, '--cockpit' => $this->cockpit]);

        self::assertSame(ExitCode::OK, $exit);
    }

    public function testRefusingToOverwriteAnExistingCockpitIsAnInfrastructureFailure(): void
    {
        $tester = new CommandTester(new InitCommand());
        $exit = $tester->execute(['dir' => $this->cockpit]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
    }

    private function adapterFactory(?string $envPath): StubEngineAdapterFactory
    {
        return new StubEngineAdapterFactory(FakeEngineAdapter::withEnvPath($envPath));
    }
}

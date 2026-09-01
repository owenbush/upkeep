<?php

declare(strict_types=1);

namespace Upkeep\Tests\Ui;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Ui\Jobs\Job;
use Upkeep\Ui\Jobs\JobAction;
use Upkeep\Ui\Jobs\JobLauncher;
use Upkeep\Ui\Jobs\JobStore;

/**
 * Backgrounded CLI runs: what the browser may ask for, how it is started, and
 * how its outcome is learned once nothing is watching it.
 */
final class JobsTest extends TestCase
{
    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/upkeep-ui-jobs-' . bin2hex(random_bytes(4));
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    private function store(?\Closure $clock = null): JobStore
    {
        return new JobStore($this->dir, $clock ?? static fn (): string => '2026-08-07T12:00:00+00:00');
    }

    private function job(string $id = 'a1b2c3d4e5f60718'): Job
    {
        return new Job(
            $id,
            'check widget !5',
            ['check', 'widget', '5'],
            Job::RUNNING,
            null,
            '2026-08-07T11:00:00+00:00',
            null,
        );
    }

    // ------------------------------------------------------------ whitelist

    /**
     * The property the whole UI's safety rests on: the browser names an action
     * and parameters, never an argument vector. Anything that is not one of
     * the three recipes produces nothing to run.
     *
     * @return iterable<string, array{string, array<string, string>}>
     */
    public static function refusedRequests(): iterable
    {
        yield 'an action that does not exist' => ['exec', ['module' => 'widget']];
        yield 'a shell attempt in the action' => ['check; rm -rf /', ['module' => 'widget']];
        yield 'a shell attempt in the module' => ['refresh', ['module' => 'widget; rm -rf /']];
        yield 'a path attempt in the module' => ['refresh', ['module' => '../../etc']];
        yield 'no module at all' => ['refresh', []];
        yield 'check without an MR' => ['check', ['module' => 'widget']];
        yield 'check with a non-numeric MR' => ['check', ['module' => 'widget', 'mr' => 'abc']];
        yield 'check with MR zero' => ['check', ['module' => 'widget', 'mr' => '0']];
        yield 'patch-check without an issue' => ['patch-check', ['module' => 'widget']];
        yield 'patch-check with a flag as the issue' => ['patch-check', ['module' => 'widget', 'issue' => '--help']];
        yield 'start without an issue' => ['start', ['module' => 'widget']];
        yield 'start with a shell attempt' => ['start', ['module' => 'widget', 'issue' => '1; rm -rf /']];
        yield 'publish without an issue' => ['publish', ['module' => 'widget']];
        yield 'publish with issue zero' => ['publish', ['module' => 'widget', 'issue' => '0']];
    }

    /**
     * @param array<string, string> $params
     */
    #[DataProvider('refusedRequests')]
    public function testOnlyWhitelistedRecipesEverBecomeACommandLine(string $action, array $params): void
    {
        self::assertNull(JobAction::build($action, $params));
    }

    /**
     * The issue loop, as the browser may ask for it. `start` is local and
     * resumes rather than resets, so a button that fires twice loses nothing;
     * `publish` is the one action that reaches outside the machine.
     */
    public function testTheIssueLoopIsAvailableToTheBrowser(): void
    {
        $start = JobAction::build('start', ['module' => 'widget', 'issue' => '3223746', 'core' => '11']);
        self::assertNotNull($start);
        self::assertSame(['start', 'widget', '3223746', '--version=11', '--no-interaction'], $start->argv);
        self::assertSame('start widget #3223746', $start->label);

        $publish = JobAction::build('publish', ['module' => 'widget', 'issue' => '3223746', 'core' => '11']);
        self::assertNotNull($publish);
        self::assertSame(['publish', 'widget', '3223746', '--version=11', '--no-interaction'], $publish->argv);
    }

    /**
     * `publish` opens a merge request; it is not, and must never become, the
     * merge call. Those are opposite acts.
     */
    public function testPublishIsNotAMergeInDisguise(): void
    {
        $publish = JobAction::build('publish', ['module' => 'widget', 'issue' => '3223746']);

        self::assertNotNull($publish);
        self::assertNotContains('merge', $publish->argv);
        self::assertNotContains('--fast-lane', $publish->argv);
        self::assertSame('publish', $publish->argv[0]);
    }

    public function testTheThreeActionsBuildTheCommandsTheirNamesPromise(): void
    {
        $check = JobAction::build('check', ['module' => 'widget', 'mr' => '5', 'core' => '11']);
        self::assertNotNull($check);
        self::assertSame(['check', 'widget', '5', '--version=11', '--no-interaction'], $check->argv);
        self::assertSame('check widget !5', $check->label);

        // --latest because there is nobody at a terminal to answer the picker;
        // the label still states what was run.
        $patch = JobAction::build('patch-check', ['module' => 'widget', 'issue' => '3597808', 'core' => '11']);
        self::assertNotNull($patch);
        self::assertSame(
            ['patch:check', 'widget', '3597808', '--version=11', '--latest', '--no-interaction'],
            $patch->argv,
        );

        $refresh = JobAction::build('refresh', ['module' => 'widget']);
        self::assertNotNull($refresh);
        self::assertSame(['dashboard', '--refresh=widget', '--no-interaction'], $refresh->argv);
    }

    /** An unusable core is dropped rather than passed through as a flag value. */
    public function testAnInvalidCoreIsOmittedRatherThanForwarded(): void
    {
        $action = JobAction::build('check', ['module' => 'widget', 'mr' => '5', 'core' => 'x; rm -rf /']);

        self::assertNotNull($action);
        self::assertSame(['check', 'widget', '5', '--no-interaction'], $action->argv);
    }

    /**
     * Merging is absent by design, not by oversight: the DA stance is one human
     * approval per merge, and a button that posts an action name is not the
     * per-MR prompt that earns it.
     */
    public function testMergingIsNotSomethingTheBrowserCanAskFor(): void
    {
        self::assertNull(JobAction::build('merge', ['module' => 'widget', 'mr' => '5']));
        self::assertNull(JobAction::build('merge --fast-lane', ['module' => 'widget']));
    }

    // ---------------------------------------------------------------- store

    public function testAJobRoundTripsThroughDisk(): void
    {
        $store = $this->store();
        $store->create($this->job());

        $found = $store->find('a1b2c3d4e5f60718');
        self::assertNotNull($found);
        self::assertSame('check widget !5', $found->label);
        self::assertSame(Job::RUNNING, $found->state);
        self::assertNull($found->exitCode);
    }

    /**
     * Ids are minted here, so anything of another shape came from a request —
     * and a request must never be able to name a path segment.
     */
    public function testAnIdThatWasNotMintedHereIsNotAPath(): void
    {
        $store = $this->store();

        self::assertFalse(JobStore::isId('../../etc/passwd'));
        self::assertFalse(JobStore::isId('nope'));
        self::assertTrue(JobStore::isId(JobStore::newId()));
        self::assertNull($store->find('../../etc/passwd'));
        self::assertNull($store->logPath('../../etc'));
        self::assertNull($store->exitPath('../../etc'));
        self::assertSame(['output' => '', 'offset' => 0, 'complete' => true], $store->readFrom('../..', 0));
    }

    public function testAMissingOrUnreadableRecordIsAMissNotACrash(): void
    {
        $store = $this->store();
        self::assertNull($store->find('a1b2c3d4e5f60718'));

        mkdir($this->dir . '/a1b2c3d4e5f60718', 0o700, true);
        file_put_contents($this->dir . '/a1b2c3d4e5f60718/meta.json', 'not json');
        self::assertNull($store->find('a1b2c3d4e5f60718'));

        file_put_contents($this->dir . '/a1b2c3d4e5f60718/meta.json', '"a string"');
        self::assertNull($store->find('a1b2c3d4e5f60718'));

        file_put_contents($this->dir . '/a1b2c3d4e5f60718/meta.json', '{"label":"no id"}');
        self::assertNull($store->find('a1b2c3d4e5f60718'));
    }

    /**
     * Nothing watches a running job: the request that started it returned long
     * ago and the server may have restarted. "Has it finished?" is answered by
     * the sentinel its own shell wrapper writes.
     */
    public function testAFinishedJobIsLearnedFromItsExitSentinel(): void
    {
        $store = $this->store();
        $store->create($this->job());
        file_put_contents($this->dir . '/a1b2c3d4e5f60718/exit', "1\n");

        $found = $store->find('a1b2c3d4e5f60718');
        self::assertNotNull($found);
        self::assertSame(Job::FAILED, $found->state);
        self::assertSame(1, $found->exitCode);
        self::assertSame('2026-08-07T12:00:00+00:00', $found->finishedAt);

        // Written back, so it is only read once.
        $meta = json_decode((string) file_get_contents($this->dir . '/a1b2c3d4e5f60718/meta.json'), true);
        self::assertIsArray($meta);
        self::assertSame(Job::FAILED, $meta['state'] ?? null);
    }

    /** A job with no exit code yet is running, by the same mapping. */
    public function testNoExitCodeYetIsTheRunningState(): void
    {
        self::assertSame(Job::RUNNING, Job::stateFor(null));
        self::assertFalse((new Job('x', 'l', [], Job::stateFor(null), null, 'now', null))->isFinished());
        self::assertTrue((new Job('x', 'l', [], Job::stateFor(0), 0, 'now', 'then'))->isFinished());
    }

    /** @return iterable<string, array{string, string}> */
    public static function exitSentinels(): iterable
    {
        yield 'did what was asked' => ['0', Job::SUCCEEDED];
        yield 'the work failed' => ['1', Job::FAILED];
        yield 'upkeep could not do the job' => ['2', Job::INFRASTRUCTURE];
        yield 'a signal' => ['130', Job::INFRASTRUCTURE];
    }

    /** The same three meanings the CLI's exit codes have, read back as states. */
    #[DataProvider('exitSentinels')]
    public function testTheExitCodeContractSurvivesTheRoundTrip(string $sentinel, string $expected): void
    {
        $store = $this->store();
        $store->create($this->job());
        file_put_contents($this->dir . '/a1b2c3d4e5f60718/exit', $sentinel);

        self::assertSame($expected, $store->find('a1b2c3d4e5f60718')?->state);
    }

    public function testAnUnreadableSentinelLeavesTheJobRunning(): void
    {
        $store = $this->store();
        $store->create($this->job());
        file_put_contents($this->dir . '/a1b2c3d4e5f60718/exit', 'not a number');

        self::assertSame(Job::RUNNING, $store->find('a1b2c3d4e5f60718')?->state);
    }

    /**
     * Offset-based reads are the one thing a browser can hold across a poll, a
     * refresh or a reconnect without the server remembering anything about it.
     */
    public function testOutputIsReadFromAnOffsetAndSaysWhereToResume(): void
    {
        $store = $this->store();
        $store->create($this->job());
        $log = $this->dir . '/a1b2c3d4e5f60718/output.log';

        file_put_contents($log, "first\n");
        $one = $store->readFrom('a1b2c3d4e5f60718', 0);
        self::assertSame("first\n", $one['output']);
        self::assertSame(6, $one['offset']);
        self::assertTrue($one['complete']);

        file_put_contents($log, "second\n", \FILE_APPEND);
        $two = $store->readFrom('a1b2c3d4e5f60718', $one['offset']);
        self::assertSame("second\n", $two['output'], 'only what is new');
        self::assertSame(13, $two['offset']);

        self::assertSame('', $store->readFrom('a1b2c3d4e5f60718', 13)['output'], 'nothing new');
    }

    /** A long check must not make each poll heavier than the last. */
    public function testAReadIsCappedSoPollingStaysCheap(): void
    {
        $store = $this->store();
        $store->create($this->job());
        file_put_contents(
            $this->dir . '/a1b2c3d4e5f60718/output.log',
            str_repeat('x', JobStore::MAX_CHUNK_BYTES * 2),
        );

        $chunk = $store->readFrom('a1b2c3d4e5f60718', 0);
        self::assertSame(JobStore::MAX_CHUNK_BYTES, \strlen($chunk['output']));
        self::assertFalse($chunk['complete'], 'more to come');
    }

    /** An offset past the end is a caller that has drifted, not an error. */
    public function testAnOffsetBeyondTheLogIsClampedRatherThanFailing(): void
    {
        $store = $this->store();
        $store->create($this->job());
        file_put_contents($this->dir . '/a1b2c3d4e5f60718/output.log', 'short');

        $chunk = $store->readFrom('a1b2c3d4e5f60718', 9_000);
        self::assertSame('', $chunk['output']);
        self::assertSame(5, $chunk['offset']);
    }

    public function testJobsAreListedNewestFirst(): void
    {
        $store = $this->store();
        $store->create(new Job('1111111111111111', 'old', [], Job::SUCCEEDED, 0, '2026-08-01T00:00:00+00:00', null));
        $store->create(new Job('2222222222222222', 'new', [], Job::SUCCEEDED, 0, '2026-08-05T00:00:00+00:00', null));
        // A directory without a readable record is skipped rather than fatal.
        mkdir($this->dir . '/3333333333333333', 0o700, true);
        file_put_contents($this->dir . '/3333333333333333/meta.json', 'rubbish');

        self::assertSame(['new', 'old'], array_map(static fn (Job $j): string => $j->label, $store->all()));
    }

    // --------------------------------------------------------------- launch

    /**
     * The wrapper is what lets a job outlive its parent: output appended to the
     * log, exit status written to the sentinel, nothing left watching.
     */
    public function testTheLaunchedCommandRedirectsOutputAndRecordsItsExitStatus(): void
    {
        $spawned = null;
        $launcher = new JobLauncher(
            $this->store(),
            '/opt/upkeep/bin/upkeep',
            '/home/owen/cockpit',
            static function (string $line) use (&$spawned): void {
                $spawned = $line;
            },
            static fn (): string => '2026-08-07T11:00:00+00:00',
        );

        $action = JobAction::build('check', ['module' => 'widget', 'mr' => '5', 'core' => '11']);
        self::assertNotNull($action);
        $job = $launcher->launch($action);

        self::assertSame(Job::RUNNING, $job->state);
        self::assertContains('--cockpit=/home/owen/cockpit', $job->argv);
        self::assertIsString($spawned);
        self::assertStringContainsString("'/opt/upkeep/bin/upkeep' 'check' 'widget' '5'", $spawned);
        self::assertStringContainsString('/output.log', $spawned);
        self::assertStringContainsString('printf %s $? >', $spawned);
        // Every word is quoted, so nothing in an argument can reach the shell.
        self::assertStringNotContainsString('; rm', $spawned);

        // The record exists before the process does, so a poll that arrives
        // first finds a job rather than a 404.
        self::assertNotNull($this->store()->find($job->id));
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Ui;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Command\UiCommand;
use Upkeep\Dashboard\DashboardCache;
use Upkeep\Dashboard\ModuleSnapshot;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Patches\PatchRevision;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;
use Upkeep\Ui\Assets;
use Upkeep\Ui\Http\Request;
use Upkeep\Ui\Http\Response;
use Upkeep\Ui\LaunchToken;
use Upkeep\Ui\StateBuilder;
use Upkeep\Ui\UiServer;
use Upkeep\Workflow\ExitCode;

/**
 * The rest of the UI surface: the token, the HTTP value objects, the state the
 * page renders, and how the server is started.
 */
final class UiSurfaceTest extends TestCase
{
    private string $cockpit;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-ui-surface-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit, 0o700, true);
        file_put_contents(
            $this->cockpit . '/registry.yml',
            "modules:\n  widget:\n    project: project/widget\n    core_versions: [\"10\", \"11\"]\n",
        );
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->cockpit));
    }

    // ---------------------------------------------------------------- token

    /**
     * Binding to loopback keeps the port off the network but not away from
     * other software on the machine, so the token is the actual barrier.
     */
    public function testTheTokenIsUnguessableAndFreshEveryRun(): void
    {
        $one = LaunchToken::mint();
        $two = LaunchToken::mint();

        self::assertSame(64, \strlen($one->value), '256 bits, hex-encoded');
        self::assertNotSame($one->value, $two->value);
        self::assertTrue($one->matches($one->value));
        self::assertFalse($one->matches($two->value));
        self::assertFalse($one->matches(null));
        self::assertFalse($one->matches(''));
        // A prefix must not pass: the comparison is over the whole value.
        self::assertFalse($one->matches(substr($one->value, 0, 32)));
    }

    public function testTheLaunchUrlCarriesTheTokenForTheBrowser(): void
    {
        $token = LaunchToken::of('abc123');

        self::assertSame('http://127.0.0.1:8721/?token=abc123', $token->launchUrl('127.0.0.1', 8721));
    }

    // ----------------------------------------------------------------- http

    /**
     * Superglobals are narrowed once, at the edge — the same discipline
     * ApiPayload applies to a decoded API body.
     */
    public function testARequestIsNarrowedOutOfWhateverTheServerHandsIt(): void
    {
        $request = Request::fromGlobals(
            ['REQUEST_METHOD' => 'POST', 'REQUEST_URI' => '/api/jobs/abc?offset=12'],
            ['offset' => '12', 'bad' => ['array'], 7 => 'unkeyed'],
            ['upkeep_ui' => 'tok'],
            '{"action":"check","mr":5}',
        );

        self::assertSame('POST', $request->method);
        self::assertSame('/api/jobs/abc', $request->path, 'the query string is not part of the path');
        self::assertSame(['offset' => '12'], $request->query, 'non-scalar and unkeyed values are dropped');
        self::assertSame('tok', $request->token());
        self::assertSame('abc', $request->segment(2));
        self::assertNull($request->segment(9));
        self::assertSame('check', $request->bodyString('action'));
        self::assertSame('5', $request->bodyString('mr'), 'numbers arrive as strings');
        self::assertNull($request->bodyString('absent'));
    }

    public function testARequestFromNothingUsableStillHasAShape(): void
    {
        $request = Request::fromGlobals([], [], [], 'not json');

        self::assertSame('GET', $request->method);
        self::assertSame('/', $request->path);
        self::assertNull($request->token());
        self::assertNull($request->bodyString('action'));
    }

    /**
     * The URL beats the cookie, and the order is load-bearing.
     *
     * A token in the query string is the operator deliberately presenting a
     * credential — they have just been handed a launch URL and followed it.
     * The cookie is only what an earlier visit left behind. With the
     * precedence the other way round a restart was unrecoverable: every fresh
     * launch URL arrived at a browser still holding the previous run's cookie,
     * which shadowed it, and the new link was refused as expired.
     */
    public function testTheUrlTokenBeatsAStaleCookie(): void
    {
        $request = Request::of('GET', '/', ['token' => 'from-url'], ['upkeep_ui' => 'stale']);

        self::assertSame('from-url', $request->token());
    }

    /** With no token in the URL, the cookie is what keeps the session going. */
    public function testTheCookieCarriesTheSessionOnceTheUrlHasBeenCleaned(): void
    {
        $request = Request::of('GET', '/api/state', [], ['upkeep_ui' => 'from-cookie']);

        self::assertSame('from-cookie', $request->token());
    }

    public function testAResponseIsDataRatherThanSideEffects(): void
    {
        $json = Response::json(['a' => 1], 202);
        self::assertSame(202, $json->status);
        self::assertSame('application/json', $json->headers['Content-Type']);
        self::assertStringContainsString('"a":1', $json->body);

        $text = Response::text('hello');
        self::assertStringContainsString('text/plain', $text->headers['Content-Type']);

        $tagged = Response::of(200, 'x')->withHeader('X-Thing', 'y');
        self::assertSame('y', $tagged->headers['X-Thing']);

        // A header value that is not scalar is dropped rather than rendered.
        self::assertArrayNotHasKey('Bad', Response::of(200, 'x', ['Bad' => ['a']])->headers);
    }

    /** A missing asset is a 404 page, not a server that will not start. */
    public function testAMissingAssetDegradesToNotFound(): void
    {
        $assets = new Assets($this->cockpit . '/no-such-dir');

        self::assertSame('', $assets->page());
        self::assertSame('', $assets->script());
        self::assertSame('', $assets->style());
        self::assertSame(404, Response::html($assets->page())->status);
    }

    public function testTheBundledAssetsAreTheOnesShipped(): void
    {
        $assets = Assets::bundled();

        self::assertStringContainsString('<title>upkeep</title>', $assets->page());
        self::assertStringContainsString('/api/state', $assets->script());
        self::assertStringContainsString('--accent', $assets->style());
    }

    // ---------------------------------------------------------------- state

    /**
     * The page is a second renderer over `Dashboard\RowFactory`, never a second
     * source of truth — so it cannot show a verdict the terminal disagrees
     * with.
     */
    public function testStateComesFromTheSameRowsTheTerminalPrints(): void
    {
        $url = 'https://example.test/a.patch';
        (new DashboardCache($this->cockpit . '/cache/dashboard'))->save('widget', new ModuleSnapshot(
            new \DateTimeImmutable(),
            ['id' => 42, 'path_with_namespace' => 'project/widget', 'path' => 'widget'],
            [[
                'iid' => 7,
                'title' => 'Issue #3467675: a change',
                'state' => 'opened',
                'author' => ['username' => 'alice', 'id' => 1],
                'source_branch' => '3467675-fix',
                'target_branch' => '1.0.x',
                'sha' => str_repeat('a', 40),
                'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/7',
                'diff_refs' => ['base_sha' => 'base', 'head_sha' => 'head'],
            ]],
            [],
            [[
                'nid' => 3597808,
                'title' => 'A patch issue',
                'url' => 'https://www.drupal.org/node/3597808',
                'field_issue_status' => '8',
                'field_project' => ['machine_name' => 'widget'],
                'field_issue_files' => [['file' => [
                    'filename' => 'a.patch',
                    'url' => $url,
                    'filesize' => '10',
                    'timestamp' => '1705400000',
                ]]],
            ]],
        ));

        (new ResultsCache($this->cockpit . '/results'))->store(
            'widget',
            ResultKey::patch(3597808),
            '11',
            PatchRevision::of($url),
            new CheckRunResult([new CheckResult(CheckType::PhpCs, CheckStatus::Failed, 1, 'bad', 0.1)]),
        );

        $state = (new StateBuilder(new Cockpit($this->cockpit)))->build();
        $module = self::arr(self::arr($state['modules'])[0]);
        $rows = array_map(self::arr(...), self::arr($module['rows']));
        $summary = self::arr($module['summary']);

        // One MR and one patch issue, each across two tracked cores.
        self::assertCount(4, $rows);
        self::assertSame(1, $summary['merge_requests']);
        self::assertSame(1, $summary['patch_issues']);

        $patchRows = array_values(array_filter($rows, static fn (array $r): bool => $r['kind'] === 'patch'));
        self::assertSame(3597808, $patchRows[0]['issue']);
        self::assertSame('https://www.drupal.org/node/3597808', $patchRows[0]['url']);
        self::assertSame(1, $patchRows[0]['patches']);
        self::assertSame('fail', $patchRows[1]['local'], 'the core-11 row carries its cached verdict');

        $mrRows = array_values(array_filter($rows, static fn (array $r): bool => $r['kind'] === 'mr'));
        self::assertSame(7, $mrRows[0]['mr']);
        self::assertFalse($mrRows[0]['ready_auto']);
        self::assertSame('widget', $module['module']);
        self::assertSame(['widget'], (new StateBuilder(new Cockpit($this->cockpit)))->moduleNames());
    }

    /** A module whose MRs could not be listed is still a visible row. */
    public function testAFailedModuleIsRenderedRatherThanOmitted(): void
    {
        (new DashboardCache($this->cockpit . '/cache/dashboard'))->save('widget', new ModuleSnapshot(
            new \DateTimeImmutable(),
            ['id' => 42, 'path_with_namespace' => 'project/widget', 'path' => 'widget'],
            [],
            [],
        ));

        $state = (new StateBuilder(new Cockpit($this->cockpit)))->build();
        $module = self::arr(self::arr($state['modules'])[0]);

        self::assertSame([], $module['rows']);
        self::assertSame(0, self::arr($module['summary'])['merge_requests']);
        self::assertNotNull($module['cached']);
    }

    /**
     * The queue the page's second view renders: every open issue, contribution
     * as a column. Built from the same snapshot as the contribution rows, so
     * the page cannot show an issue the terminal would not.
     */
    public function testTheIssueQueueIsExposedAlongsideTheContributionRows(): void
    {
        (new DashboardCache($this->cockpit . '/cache/dashboard'))->save('widget', new ModuleSnapshot(
            new \DateTimeImmutable(),
            ['id' => 42, 'path_with_namespace' => 'project/widget', 'path' => 'widget'],
            [[
                'iid' => 7,
                'title' => 'Issue #3467675: a change',
                'state' => 'opened',
                'author' => ['username' => 'alice', 'id' => 1],
                'source_branch' => '3467675-fix',
                'target_branch' => '1.0.x',
                'sha' => str_repeat('a', 40),
                'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/7',
                'diff_refs' => ['base_sha' => 'base', 'head_sha' => 'head'],
            ]],
            [],
            [
                // Active with nothing on it — invisible under the old scan, and
                // the row the queue view exists for.
                self::issuePayload(3611658, '1', 'Cache grows unbounded'),
                // Needs review, carried by the merge request above.
                self::issuePayload(3467675, '8', 'A change'),
                // RTBC with a patch.
                self::issuePayload(3398583, '14', 'Prevent dot aliases', [['p.patch', 'https://x.test/p.patch']]),
            ],
        ));

        $module = self::arr(self::arr((new StateBuilder(new Cockpit($this->cockpit)))->build()['modules'])[0]);
        $issues = array_map(self::arr(...), self::arr($module['issues']));

        self::assertCount(3, $issues, 'every open status, not only the contribution-shaped two');

        // Ordered most actionable first: what awaits a maintainer, then the
        // rest newest first — exactly as `upkeep issues` orders it.
        self::assertSame([3467675, 3398583, 3611658], array_column($issues, 'nid'));

        self::assertTrue($issues[2]['unclaimed'], 'the Active issue is unclaimed');
        self::assertFalse($issues[2]['awaits_maintainer']);
        self::assertSame(7, $issues[0]['mr'], 'the covered issue names its merge request');
        self::assertTrue($issues[0]['awaits_maintainer']);
        self::assertSame(1, $issues[1]['patches']);
        self::assertFalse($issues[1]['unclaimed']);
    }

    /**
     * The dashboard stayed contribution-shaped when the snapshot widened: an
     * issue nobody has contributed to is `upkeep issues`' subject, not a row
     * on a view about what is waiting for you.
     */
    public function testAnUnclaimedIssueIsInTheQueueButNotInTheContributionRows(): void
    {
        (new DashboardCache($this->cockpit . '/cache/dashboard'))->save('widget', new ModuleSnapshot(
            new \DateTimeImmutable(),
            ['id' => 42, 'path_with_namespace' => 'project/widget', 'path' => 'widget'],
            [],
            [],
            [self::issuePayload(3611658, '1', 'Cache grows unbounded')],
        ));

        $module = self::arr(self::arr((new StateBuilder(new Cockpit($this->cockpit)))->build()['modules'])[0]);

        self::assertCount(1, self::arr($module['issues']));
        self::assertSame([], $module['rows'], 'no patch, so no contribution row');
    }

    /**
     * @param list<array{string, string}> $patches
     *
     * @return array<string, mixed>
     */
    private static function issuePayload(int $nid, string $status, string $title, array $patches = []): array
    {
        return [
            'nid' => $nid,
            'title' => $title,
            'url' => 'https://www.drupal.org/node/' . $nid,
            'field_issue_status' => $status,
            'field_issue_priority' => '200',
            'field_project' => ['machine_name' => 'widget'],
            'field_issue_files' => array_map(
                static fn (array $p): array => ['file' => [
                    'filename' => $p[0],
                    'url' => $p[1],
                    'filesize' => '10',
                    'timestamp' => '1705400000',
                ]],
                $patches,
            ),
        ];
    }

    /** The page offers both views and the confirmation the outward act needs. */
    /**
     * The explanation has to be actionable on its own: its reader has no token,
     * so it cannot link them anywhere, and there is no way to re-print the
     * current one. It has to say to restart.
     */
    public function testTheExpiredPageSaysHowToGetAWorkingLink(): void
    {
        $page = Assets::bundled()->expiredPage();

        self::assertStringContainsString('Ctrl', $page);
        self::assertStringContainsString('upkeep ui', $page);
        self::assertStringContainsString('never written to disk', $page);
        // The recovery that does not need the terminal at all.
        self::assertStringContainsString('browser history', $page);
        // Self-contained: every asset it might link is behind the token its
        // reader does not have.
        self::assertStringNotContainsString('/app.css', $page);
        self::assertStringNotContainsString('/app.js', $page);
    }

    /**
     * The token stays in the address bar on purpose.
     *
     * Removing it read as tidier and stranded people: a restart mints a new
     * one, and a tab whose URL had been cleaned had nothing left to present
     * and no way to find the current link except the terminal it was printed
     * in. Left there, the link is always recoverable from history.
     */
    public function testTheClientDoesNotStripTheTokenFromTheAddressBar(): void
    {
        $script = Assets::bundled()->script();

        self::assertStringNotContainsString('replaceState', $script);
        self::assertStringNotContainsString('location.pathname', $script);
    }

    public function testTheShippedPageOffersBothViewsAndConfirmsPublishing(): void
    {
        $assets = Assets::bundled();

        self::assertStringContainsString('view-issues', $assets->page());
        self::assertStringContainsString('view-work', $assets->page());
        self::assertStringContainsString('<dialog id="confirm"', $assets->page());
        self::assertStringContainsString("action: 'start'", $assets->script());
        self::assertStringContainsString('confirmPublish', $assets->script());
        // Publishing goes through the dialog, never straight from the button.
        self::assertStringNotContainsString(
            "publish.addEventListener('click', () => startJob(",
            $assets->script(),
        );
    }

    // --------------------------------------------------------------- server

    public function testTheServerIsBoundToLoopbackAndRoutedThroughTheFrontController(): void
    {
        $server = new UiServer(LaunchToken::of('tok'), new Cockpit($this->cockpit), 9999);
        $arguments = $server->arguments();

        self::assertContains('-S', $arguments);
        self::assertContains('127.0.0.1:9999', $arguments, 'never 0.0.0.0');
        self::assertSame($server->router(), end($arguments));
        self::assertStringEndsWith('/bin/upkeep-ui-router.php', $server->router());
        self::assertStringEndsWith('/assets/ui', $server->documentRoot());
        self::assertStringEndsWith('/bin/upkeep', UiServer::binary());
    }

    /**
     * Both secrets travel in the environment, because a command line is
     * readable by every process on the machine.
     */
    public function testTheTokenAndTheCredentialTravelInTheEnvironment(): void
    {
        $previous = getenv(TokenResolver::DEFAULT_ENV_VAR);
        putenv(TokenResolver::DEFAULT_ENV_VAR . '=glpat-forwarded');

        try {
            $env = (new UiServer(LaunchToken::of('tok'), new Cockpit($this->cockpit), 9999))->environment();

            self::assertSame('tok', $env['UPKEEP_UI_TOKEN']);
            self::assertSame($this->cockpit, $env['UPKEEP_UI_COCKPIT']);
            self::assertStringEndsWith('/bin/upkeep', $env['UPKEEP_UI_BINARY']);
            // Forwarded deliberately: the children are upkeep itself, which
            // needs it and never prints it. Api redacts the captured log again
            // on the way out rather than trusting that.
            self::assertSame('glpat-forwarded', $env[TokenResolver::DEFAULT_ENV_VAR]);
            self::assertArrayHasKey('PHP_CLI_SERVER_WORKERS', $env);
        } finally {
            $previous === false
                ? putenv(TokenResolver::DEFAULT_ENV_VAR)
                : putenv(TokenResolver::DEFAULT_ENV_VAR . '=' . $previous);
        }
    }

    public function testNoCredentialInTheEnvironmentForwardsNothing(): void
    {
        $previous = getenv(TokenResolver::DEFAULT_ENV_VAR);
        putenv(TokenResolver::DEFAULT_ENV_VAR . '=');

        try {
            $env = (new UiServer(LaunchToken::of('tok'), new Cockpit($this->cockpit), 9999))->environment();

            self::assertArrayNotHasKey(TokenResolver::DEFAULT_ENV_VAR, $env);
        } finally {
            $previous === false
                ? putenv(TokenResolver::DEFAULT_ENV_VAR)
                : putenv(TokenResolver::DEFAULT_ENV_VAR . '=' . $previous);
        }
    }

    public function testTheDefaultRunnerIsARealProcessCall(): void
    {
        $server = new UiServer(LaunchToken::of('tok'), new Cockpit($this->cockpit), 9999);
        $runner = $server->runner();

        // Run something that is not a server, to prove the runner returns the
        // child's exit code rather than assuming success.
        self::assertSame(0, $runner(sys_get_temp_dir(), [\PHP_BINARY, '-r', 'exit(0);'], []));
        self::assertSame(3, $runner(sys_get_temp_dir(), [\PHP_BINARY, '-r', 'exit(3);'], []));
    }

    // -------------------------------------------------------------- command


    /**
     * Narrows a decoded value so the assertions work with a real array rather
     * than mixed — the wire format is deliberately untyped, so the tests do
     * the narrowing the handlers would.
     *
     * @return array<array-key, mixed>
     */
    private static function arr(mixed $value): array
    {
        self::assertIsArray($value);

        return $value;
    }

    private static function str(mixed $value): string
    {
        self::assertIsString($value);

        return $value;
    }

    private function ui(?\Closure $serve = null): CommandTester
    {
        // The port is reported free unless a test says otherwise, so no test
        // has to bind one to exercise the ordinary path.
        return new CommandTester(new UiCommand(
            $serve ?? static fn (): int => 0,
            static fn (): bool => false,
        ));
    }

    public function testTheCommandPrintsTheLaunchUrlAndStartsTheServer(): void
    {
        $captured = [];
        $tester = $this->ui(function (string $cwd, array $arguments, array $env) use (&$captured): int {
            $captured = ['cwd' => $cwd, 'arguments' => $arguments, 'env' => $env];

            return 0;
        });

        $exit = $tester->execute(['--cockpit' => $this->cockpit, '--no-open' => true, '--port' => '9310']);

        self::assertSame(ExitCode::OK, $exit, $tester->getDisplay());
        self::assertMatchesRegularExpression('#http://127\.0\.0\.1:9310/\?token=[0-9a-f]{64}#', $tester->getDisplay());
        self::assertStringContainsString('Ctrl-C', $tester->getDisplay());
        self::assertContains('127.0.0.1:9310', self::arr($captured['arguments']));
        self::assertSame(64, \strlen(self::str(self::arr($captured['env'])['UPKEEP_UI_TOKEN'])));
    }

    /**
     * The failure that made a stale link look like a bug: on a busy port the
     * server cannot bind, but the port keeps answering — from the previous run,
     * with the previous token. Printing a launch URL first hands the operator a
     * link that was dead the moment it was written.
     */
    public function testABusyPortIsRefusedBeforeAnyUrlIsPrinted(): void
    {
        $served = false;
        $tester = new CommandTester(new UiCommand(
            function () use (&$served): int {
                $served = true;

                return 0;
            },
            static fn (): bool => true,
        ));

        $exit = $tester->execute(['--cockpit' => $this->cockpit, '--no-open' => true, '--port' => '9320']);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertFalse($served, 'nothing may be started on a port that is taken');
        // Above all: no URL, because any URL printed here could not work.
        self::assertStringNotContainsString('token=', $tester->getDisplay());
        self::assertStringContainsString('already listening on 127.0.0.1:9320', $tester->getDisplay());
        self::assertStringContainsString('Ctrl-C', $tester->getDisplay());
        self::assertStringContainsString('--port=9321', $tester->getDisplay());
    }

    /**
     * The probe itself, against a real socket — the one thing here that cannot
     * be proven with a double, since its whole job is to ask the operating
     * system a question.
     */
    public function testThePortProbeAnswersForARealListener(): void
    {
        // Port 0 lets the OS choose a free one, so this cannot collide with
        // anything else on the machine running the suite.
        $listener = stream_socket_server('tcp://' . UiServer::HOST . ':0', $errno, $errstr);
        self::assertIsResource($listener, 'could not open a listening socket: ' . $errstr);

        $name = stream_socket_get_name($listener, false);
        self::assertIsString($name);
        $port = (int) substr($name, (int) strrpos($name, ':') + 1);

        self::assertTrue(UiServer::isPortInUse(UiServer::HOST, $port), 'something is plainly listening');

        fclose($listener);

        self::assertFalse(UiServer::isPortInUse(UiServer::HOST, $port), 'and nothing is once it closes');
    }

    /** A free port is the ordinary path, and still prints a working link. */
    public function testAFreePortProceedsAsBefore(): void
    {
        $tester = new CommandTester(new UiCommand(
            static fn (): int => 0,
            static fn (): bool => false,
        ));

        $exit = $tester->execute(['--cockpit' => $this->cockpit, '--no-open' => true, '--port' => '9321']);

        self::assertSame(ExitCode::OK, $exit);
        self::assertStringContainsString('token=', $tester->getDisplay());
    }

    /** A server that will not start is an infrastructure outcome, not a crash. */
    public function testAServerThatCannotStartExitsTwo(): void
    {
        $tester = $this->ui(static fn (): int => 1);

        self::assertSame(
            ExitCode::INFRASTRUCTURE,
            $tester->execute(['--cockpit' => $this->cockpit, '--no-open' => true]),
        );
    }

    /** @return iterable<string, array{string}> */
    public static function refusedPorts(): iterable
    {
        yield 'not a number' => ['http'];
        yield 'privileged' => ['80'];
        yield 'out of range' => ['70000'];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('refusedPorts')]
    public function testAPortThatIsNotOneIsRefused(string $port): void
    {
        $tester = $this->ui();
        $exit = $tester->execute(['--cockpit' => $this->cockpit, '--no-open' => true, '--port' => $port]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('--port must be a number', $tester->getDisplay());
    }

    /**
     * The browser is opened unless told not to. Driven through the real
     * BrowserOpener with a stub binary first on PATH, so no browser ever
     * launches and the platform call still runs.
     */
    public function testTheBrowserIsOpenedOnTheLaunchUrlUnlessSuppressed(): void
    {
        $bin = $this->cockpit . '/fake-bin';
        mkdir($bin, 0o700, true);
        $log = $this->cockpit . '/opened';
        foreach (['xdg-open', 'open'] as $name) {
            file_put_contents($bin . '/' . $name, "#!/bin/sh\nprintf '%s' \"$1\" >> " . escapeshellarg($log) . "\n");
            chmod($bin . '/' . $name, 0o700);
        }

        $previousPath = getenv('PATH');
        putenv('PATH=' . $bin . ':' . ($previousPath === false ? '/usr/bin:/bin' : $previousPath));

        try {
            $tester = $this->ui();
            $tester->execute(['--cockpit' => $this->cockpit, '--port' => '9311']);

            self::assertFileExists($log);
            self::assertStringContainsString('http://127.0.0.1:9311/?token=', (string) file_get_contents($log));
        } finally {
            $previousPath === false ? putenv('PATH') : putenv('PATH=' . $previousPath);
        }
    }

    public function testTheDefaultPortIsUsedWhenNoneIsGiven(): void
    {
        $tester = $this->ui();
        $tester->execute(['--cockpit' => $this->cockpit, '--no-open' => true]);

        self::assertStringContainsString(':' . UiServer::DEFAULT_PORT . '/', $tester->getDisplay());
    }
}

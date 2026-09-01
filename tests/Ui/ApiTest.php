<?php

declare(strict_types=1);

namespace Upkeep\Tests\Ui;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Security\SecretRedactor;
use Upkeep\Ui\Api;
use Upkeep\Ui\Assets;
use Upkeep\Ui\Http\Request;
use Upkeep\Ui\Http\Response;
use Upkeep\Ui\Jobs\Job;
use Upkeep\Ui\Jobs\JobLauncher;
use Upkeep\Ui\Jobs\JobStore;
use Upkeep\Ui\LaunchToken;
use Upkeep\Ui\StateBuilder;

/**
 * The whole request surface, driven as the pure function it is.
 *
 * No socket, because nothing here needs one: the front controller is the only
 * code that touches a superglobal or emits a byte, and everything worth
 * asserting happens above it.
 */
final class ApiTest extends TestCase
{
    private const TOKEN = 'a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1';

    private string $cockpit;

    /** @var list<string> */
    private array $spawned = [];

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-ui-api-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit, 0o700, true);
        file_put_contents(
            $this->cockpit . '/registry.yml',
            "modules:\n  widget:\n    project: project/widget\n    core_versions: [\"11\"]\n",
        );
        $this->spawned = [];
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->cockpit));
    }

    private function api(?SecretRedactor $redactor = null): Api
    {
        $cockpit = new Cockpit($this->cockpit);
        $store = new JobStore($cockpit->uiJobsPath(), static fn (): string => '2026-08-07T12:00:00+00:00');

        return new Api(
            LaunchToken::of(self::TOKEN),
            new StateBuilder($cockpit),
            $store,
            new JobLauncher(
                $store,
                '/opt/upkeep/bin/upkeep',
                $cockpit->root,
                function (string $line): void {
                    $this->spawned[] = $line;
                },
                static fn (): string => '2026-08-07T11:00:00+00:00',
            ),
            Assets::bundled(),
            $redactor ?? new SecretRedactor(),
        );
    }

    private function store(): JobStore
    {
        return new JobStore((new Cockpit($this->cockpit))->uiJobsPath());
    }

    /**
     * Narrows a decoded element so the assertions below work with a real array
     * rather than mixed.
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

    /** @return array<array-key, mixed> */
    private static function decode(Response $response): array
    {
        $decoded = json_decode($response->body, true);

        return \is_array($decoded) ? $decoded : [];
    }

    private static function authorised(string $method, string $path, string $body = '', string $query = ''): Request
    {
        parse_str($query, $parsed);
        /** @var array<string, string> $parsed */
        return Request::of($method, $path, $parsed, ['upkeep_ui' => self::TOKEN], $body);
    }

    // ------------------------------------------------------------------ auth

    /**
     * @return iterable<string, array{string}>
     */
    public static function protectedPaths(): iterable
    {
        // The page route is absent deliberately: a person navigating there is
        // the one case that gets an explanation rather than a flat refusal.
        // Its own tests are below.
        yield 'the script' => ['/app.js'];
        yield 'the stylesheet' => ['/app.css'];
        yield 'the state' => ['/api/state'];
        yield 'the job list' => ['/api/jobs'];
        yield 'one job' => ['/api/jobs/a1b2c3d4e5f60718'];
    }

    /**
     * There is no public prefix, not even for assets: a served asset is still
     * evidence that a credential-holding process is listening on this port.
     * And every refusal is identical, so nothing maps the surface.
     */
    #[DataProvider('protectedPaths')]
    public function testEveryPathIsBehindTheLaunchToken(string $path): void
    {
        $response = $this->api()->handle(Request::of('GET', $path));

        self::assertSame(404, $response->status);
        self::assertSame(['error' => 'Not found.'], self::decode($response));
    }

    /**
     * The one exception, and the reason it exists: three deliberate choices —
     * a per-run token, a page that strips it from the URL, and a refusal that
     * says nothing — combine into a tab left open across a restart that cannot
     * recover and cannot say why. Reported by a maintainer as a bare
     * `{"error":"Not found."}`.
     */
    public function testANavigationToThePageWithAStaleTokenIsExplainedRatherThanRefusedBlankly(): void
    {
        $stale = str_repeat('b', 64);
        $response = $this->api()->handle(Request::of('GET', '/', [], ['upkeep_ui' => $stale]));

        self::assertSame(401, $response->status);
        self::assertStringContainsString('text/html', $response->headers['Content-Type'] ?? '');
        self::assertStringContainsString('from a previous run', $response->body);
        self::assertStringContainsString('upkeep ui', $response->body);
    }

    /**
     * The explanation is self-contained, because every asset it might link is
     * behind the very token its reader does not have.
     */
    public function testTheExplanationNeedsNoAssetItCannotFetch(): void
    {
        $body = $this->api()->handle(Request::of('GET', '/'))->body;

        self::assertStringNotContainsString('/app.css', $body);
        self::assertStringNotContainsString('/app.js', $body);
        self::assertStringContainsString('<style>', $body);
    }

    /**
     * Only that one navigation is explained. The actions, the state and the
     * assets stay behind the same flat, identical 404, so nothing maps the
     * surface.
     */
    public function testNothingButThePageNavigationIsExplained(): void
    {
        $stale = ['upkeep_ui' => str_repeat('b', 64)];

        foreach (['/app.js', '/app.css', '/api/state', '/api/jobs'] as $path) {
            $response = $this->api()->handle(Request::of('GET', $path, [], $stale));
            self::assertSame(404, $response->status, $path);
            self::assertSame(['error' => 'Not found.'], self::decode($response), $path);
        }

        // A POST to the page route is not a person navigating to it.
        $posted = $this->api()->handle(Request::of('POST', '/', [], $stale));
        self::assertSame(404, $posted->status);
    }

    public function testAWrongTokenIsRefusedLikeAnAbsentOne(): void
    {
        $wrong = str_repeat('f', 64);
        $response = $this->api()->handle(Request::of('GET', '/api/state', ['token' => $wrong]));

        self::assertSame(404, $response->status);
    }

    /** The page is still protected — it serves the app to nobody without a token. */
    public function testThePageItselfIsStillNotServedWithoutAToken(): void
    {
        $response = $this->api()->handle(Request::of('GET', '/'));

        self::assertSame(401, $response->status);
        self::assertStringNotContainsString('<title>upkeep</title>', $response->body, 'not the app');
        self::assertStringNotContainsString('/app.js', $response->body);
    }

    public function testTheTokenIsAcceptedFromTheLaunchUrlOrTheCookie(): void
    {
        self::assertSame(200, $this->api()->handle(Request::of('GET', '/', ['token' => self::TOKEN]))->status);
        self::assertSame(200, $this->api()->handle(self::authorised('GET', '/'))->status);
    }

    /**
     * The one moment the token moves out of the URL, so it stops living in the
     * address bar, the history, and any screenshot of either.
     */
    public function testTheLaunchUrlSetsTheCookieAndThePlainPageDoesNot(): void
    {
        $launched = $this->api()->handle(Request::of('GET', '/', ['token' => self::TOKEN]));
        self::assertArrayHasKey('Set-Cookie', $launched->headers);
        self::assertStringContainsString('HttpOnly', $launched->headers['Set-Cookie']);
        self::assertStringContainsString('SameSite=Strict', $launched->headers['Set-Cookie']);

        self::assertArrayNotHasKey('Set-Cookie', $this->api()->handle(self::authorised('GET', '/'))->headers);
    }

    /**
     * A credential-holding process on a loopback port may not fetch, frame or
     * execute anything that did not come from itself.
     */
    public function testEveryResponseCarriesItsSecurityHeaders(): void
    {
        foreach (['/', '/app.js', '/api/state'] as $path) {
            $headers = $this->api()->handle(self::authorised('GET', $path))->headers;
            self::assertStringContainsString("default-src 'self'", $headers['Content-Security-Policy'] ?? '');
            self::assertStringContainsString("frame-ancestors 'none'", $headers['Content-Security-Policy'] ?? '');
            self::assertSame('nosniff', $headers['X-Content-Type-Options'] ?? '');
            self::assertSame('no-store', $headers['Cache-Control'] ?? '');
        }
    }

    // --------------------------------------------------------------- serving

    public function testTheAssetsAreServedWithTheirOwnContentTypes(): void
    {
        $page = $this->api()->handle(self::authorised('GET', '/'));
        self::assertStringContainsString('<title>upkeep</title>', $page->body);
        self::assertStringContainsString('text/html', $page->headers['Content-Type'] ?? '');

        self::assertSame(
            'text/javascript',
            $this->api()->handle(self::authorised('GET', '/app.js'))->headers['Content-Type'] ?? '',
        );
        self::assertSame(
            'text/css',
            $this->api()->handle(self::authorised('GET', '/app.css'))->headers['Content-Type'] ?? '',
        );
    }

    public function testAnUnknownPathIsAFourOhFour(): void
    {
        self::assertSame(404, $this->api()->handle(self::authorised('GET', '/nope'))->status);
        self::assertSame(404, $this->api()->handle(self::authorised('GET', '/api/nope'))->status);
    }

    /** State comes from the registry even when nothing has been fetched yet. */
    public function testStateNamesEveryRegisteredModuleEvenWithNoCache(): void
    {
        $state = self::decode($this->api()->handle(self::authorised('GET', '/api/state')));
        $modules = \is_array($state['modules'] ?? null) ? $state['modules'] : [];

        self::assertCount(1, $modules);
        self::assertSame('widget', self::arr($modules[0])['module']);
        // Said plainly, rather than rendered as a module with nothing open —
        // a different and much more comfortable claim.
        self::assertNull(self::arr($modules[0])['cached']);
        self::assertNull(self::arr($modules[0])['summary']);
    }

    // --------------------------------------------------------------- actions

    public function testStartingAJobReturnsItAcceptedAndSpawnsExactlyOneCommand(): void
    {
        $response = $this->api()->handle(self::authorised(
            'POST',
            '/api/jobs',
            json_encode(['action' => 'check', 'module' => 'widget', 'mr' => '5', 'core' => '11'], \JSON_THROW_ON_ERROR),
        ));

        self::assertSame(202, $response->status);
        $job = self::arr(self::decode($response)['job']);
        self::assertSame('check widget !5', $job['label']);
        self::assertSame(Job::RUNNING, $job['state']);
        self::assertCount(1, $this->spawned);
        self::assertStringContainsString("'check' 'widget' '5'", $this->spawned[0]);
    }

    /** @return iterable<string, array{string}> */
    public static function refusedBodies(): iterable
    {
        yield 'an action outside the whitelist' => ['{"action":"exec","module":"widget"}'];
        yield 'a shell attempt' => ['{"action":"refresh","module":"widget; rm -rf /"}'];
        yield 'raw argv' => ['{"argv":["exec","--","rm","-rf","/"]}'];
        yield 'a merge' => ['{"action":"merge","module":"widget","mr":"5"}'];
        yield 'nothing at all' => ['{}'];
        yield 'not even JSON' => ['<not json>'];
    }

    /**
     * A UI that accepted an argument vector would be a remote shell wearing a
     * dashboard, and the loopback interface is not a meaningful barrier to
     * that.
     */
    #[DataProvider('refusedBodies')]
    public function testARequestThatIsNotAWhitelistedRecipeStartsNothing(string $body): void
    {
        $response = $this->api()->handle(self::authorised('POST', '/api/jobs', $body));

        self::assertSame(422, $response->status);
        self::assertSame([], $this->spawned, 'nothing may be spawned');
    }

    public function testTheJobListIsServedNewestFirst(): void
    {
        $store = $this->store();
        $store->create(new Job('1111111111111111', 'old', [], Job::SUCCEEDED, 0, '2026-08-01T00:00:00+00:00', null));
        $store->create(new Job('2222222222222222', 'new', [], Job::SUCCEEDED, 0, '2026-08-05T00:00:00+00:00', null));

        $jobs = self::arr(self::decode($this->api()->handle(self::authorised('GET', '/api/jobs')))['jobs']);

        self::assertSame(['new', 'old'], array_column($jobs, 'label'));
    }

    // ------------------------------------------------------------- polling

    public function testAJobIsPolledFromAnOffsetAndReportsWhereToResume(): void
    {
        $store = $this->store();
        $store->create(new Job('a1b2c3d4e5f60718', 'check', [], Job::RUNNING, null, '2026-08-07T11:00:00+00:00', null));
        file_put_contents($this->cockpit . '/cache/ui/jobs/a1b2c3d4e5f60718/output.log', "line one\nline two\n");

        $first = self::decode($this->api()->handle(self::authorised('GET', '/api/jobs/a1b2c3d4e5f60718')));
        self::assertSame("line one\nline two\n", $first['output']);
        self::assertSame(18, $first['offset']);
        // Still running, so not complete however much output has been read.
        self::assertFalse($first['complete']);

        $second = self::decode($this->api()->handle(
            self::authorised('GET', '/api/jobs/a1b2c3d4e5f60718', '', 'offset=18'),
        ));
        self::assertSame('', $second['output']);
    }

    public function testAJobIsOnlyCompleteOnceItHasFinishedAndBeenFullyRead(): void
    {
        $store = $this->store();
        $store->create(new Job('a1b2c3d4e5f60718', 'check', [], Job::RUNNING, null, '2026-08-07T11:00:00+00:00', null));
        file_put_contents($this->cockpit . '/cache/ui/jobs/a1b2c3d4e5f60718/output.log', "done\n");
        file_put_contents($this->cockpit . '/cache/ui/jobs/a1b2c3d4e5f60718/exit', '0');

        $polled = self::decode($this->api()->handle(self::authorised('GET', '/api/jobs/a1b2c3d4e5f60718')));

        self::assertTrue($polled['complete']);
        self::assertSame(Job::SUCCEEDED, self::arr($polled['job'])['state']);
        self::assertSame(0, self::arr($polled['job'])['exit_code']);
    }

    /** A garbled offset reads from the start rather than being an error. */
    public function testANonNumericOffsetIsTreatedAsTheStart(): void
    {
        $store = $this->store();
        $store->create(new Job('a1b2c3d4e5f60718', 'check', [], Job::RUNNING, null, '2026-08-07T11:00:00+00:00', null));
        file_put_contents($this->cockpit . '/cache/ui/jobs/a1b2c3d4e5f60718/output.log', 'hello');

        $polled = self::decode($this->api()->handle(
            self::authorised('GET', '/api/jobs/a1b2c3d4e5f60718', '', 'offset=../../etc'),
        ));

        self::assertSame('hello', $polled['output']);
    }

    public function testAnUnknownJobIsAFourOhFour(): void
    {
        self::assertSame(404, $this->api()->handle(self::authorised('GET', '/api/jobs/a1b2c3d4e5f60718'))->status);
        self::assertSame(404, $this->api()->handle(self::authorised('GET', '/api/jobs/../../etc'))->status);
    }

    /**
     * The captured log is the one artefact of the whole system written by a
     * credentialled process and then handed to a browser, so it is redacted on
     * the way out even though the job is upkeep, which does not print its own
     * token.
     */
    public function testCapturedOutputIsRedactedOnItsWayToTheBrowser(): void
    {
        $store = $this->store();
        $store->create(new Job('a1b2c3d4e5f60718', 'check', [], Job::RUNNING, null, '2026-08-07T11:00:00+00:00', null));
        file_put_contents(
            $this->cockpit . '/cache/ui/jobs/a1b2c3d4e5f60718/output.log',
            "using glpat-SECRETVALUE12345 to authenticate\n",
        );

        // Seeded exactly as the front controller seeds it: from the credential
        // this process was given, which is the one the job inherited.
        $api = $this->api(new SecretRedactor('glpat-SECRETVALUE12345'));
        $polled = self::decode($api->handle(self::authorised('GET', '/api/jobs/a1b2c3d4e5f60718')));

        self::assertStringNotContainsString('glpat-SECRETVALUE12345', self::str($polled['output']));
        self::assertStringContainsString('to authenticate', self::str($polled['output']), 'the rest survives');
    }
}

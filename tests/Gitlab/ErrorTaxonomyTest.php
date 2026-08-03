<?php

declare(strict_types=1);

namespace Upkeep\Tests\Gitlab;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\Exception\ServerException;
use Symfony\Component\HttpClient\Exception\TimeoutException;
use Symfony\Component\HttpClient\Exception\TransportException;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\EndpointClosed;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\MalformedResponse;
use Upkeep\Gitlab\NotFound;
use Upkeep\Gitlab\Project;
use Upkeep\Gitlab\RateLimited;
use Upkeep\Gitlab\RequestRejected;
use Upkeep\Gitlab\ResourceMissing;
use Upkeep\Gitlab\TransportError;
use Upkeep\Gitlab\Unauthorized;

/**
 * The status/transport condition -> failure type table, proven by driving the
 * real condition through MockHttpClient. Nothing here instantiates a failure
 * type directly: a type that cannot be reached from a real condition is a
 * type this client has no business declaring.
 */
final class ErrorTaxonomyTest extends TestCase
{
    private const TOKEN = 'glpat-secret-token-value-123';

    /** @var list<string> */
    private array $requested = [];

    /**
     * @param list<MockResponse|\Throwable> $responses
     */
    private function client(array $responses): GitlabClient
    {
        $this->requested = [];
        $queue = $responses;
        $factory = function (string $method, string $url) use (&$queue): MockResponse {
            $this->requested[] = $url;
            $next = array_shift($queue);
            if ($next === null) {
                $this->fail('Unexpected extra HTTP request: ' . $method . ' ' . $url);
            }
            if ($next instanceof \Throwable) {
                throw $next;
            }

            return $next;
        };

        return new GitlabClient(
            new MockHttpClient($factory),
            self::TOKEN,
            'https://git.drupalcode.org/api/v4',
            'https://git.drupalcode.org',
        );
    }

    /**
     * @param array<string, mixed>|list<mixed> $payload
     * @param array<string, string> $headers
     */
    private static function json(array $payload, int $status = 200, array $headers = []): MockResponse
    {
        return new MockResponse(json_encode($payload, JSON_THROW_ON_ERROR), [
            'http_code' => $status,
            'response_headers' => ['content-type' => 'application/json'] + $headers,
        ]);
    }

    private static function projectModel(): Project
    {
        return Project::fromApi([
            'id' => 181714,
            'path' => 'conditions_helper',
            'path_with_namespace' => 'project/conditions_helper',
            'name' => 'Conditions Helper',
            'web_url' => 'https://git.drupalcode.org/project/conditions_helper',
        ]);
    }

    /**
     * @return array<string, array{int, class-string<ApiFailure>, string}>
     */
    public static function statusMappingProvider(): array
    {
        return [
            '401 rejected credential' => [401, Unauthorized::class, '401'],
            '403 closed endpoint' => [403, EndpointClosed::class, '403'],
            '404 absent resource' => [404, NotFound::class, '404'],
            '429 rate limited' => [429, RateLimited::class, 'rate-limited'],
            '400 bad request' => [400, RequestRejected::class, '400'],
            '405 method not allowed' => [405, RequestRejected::class, '405'],
            '500 server error' => [500, RequestRejected::class, '500'],
            '502 bad gateway' => [502, RequestRejected::class, '502'],
        ];
    }

    /**
     * @param class-string<ApiFailure> $expected
     */
    #[DataProvider('statusMappingProvider')]
    public function testHttpStatusMapsToExactlyOneFailureType(int $status, string $expected, string $shortCode): void
    {
        $client = $this->client([self::json(['message' => 'nope'], $status)]);

        $result = $client->project('conditions_helper');

        $this->assertInstanceOf($expected, $result);
        $this->assertSame($status, $result->status);
        $this->assertSame($shortCode, $result->shortCode());
        $this->assertSame('https://git.drupalcode.org/project/conditions_helper', $result->browserUrl);
        $this->assertStringNotContainsString(self::TOKEN, $result->message);
    }

    public function testUnauthorizedAdvisesTheCredentialAndNotTheBrowser(): void
    {
        $client = $this->client([self::json(['message' => '401 Unauthorized'], 401)]);

        $result = $client->project('conditions_helper');

        $this->assertInstanceOf(Unauthorized::class, $result);
        $this->assertStringContainsString('UPKEEP_GITLAB_TOKEN', $result->message);
        $this->assertStringContainsString('drupal-pat', $result->message);
        $this->assertStringNotContainsString('Use the browser instead', $result->message);
        $this->assertStringNotContainsString(self::TOKEN, $result->message);
    }

    public function testEndpointClosedIsThreeOhThreeOnlyAndKeepsTheBrowserFallback(): void
    {
        $client = $this->client([self::json(['message' => '403 Forbidden'], 403)]);

        $result = $client->project('conditions_helper');

        $this->assertInstanceOf(EndpointClosed::class, $result);
        $this->assertSame(403, $result->status);
        $this->assertStringContainsString('Use the browser instead', $result->message);
    }

    public function testHttpNotFoundSaysHttpFourOhFour(): void
    {
        $client = $this->client([self::json(['message' => '404 Project Not Found'], 404)]);

        $result = $client->project('no_such_module');

        $this->assertInstanceOf(NotFound::class, $result);
        $this->assertStringContainsString('HTTP 404', $result->message);
    }

    public function testDomainLevelTagMissIsResourceMissingAndNeverClaimsAnHttpStatus(): void
    {
        // The tags() call SUCCEEDS; the requested tag simply is not in it.
        $client = $this->client([
            self::json([
                ['name' => '1.0.0', 'target' => 'bbb222', 'commit' => ['id' => 'bbb222']],
            ]),
        ]);

        $result = $client->mergedSinceTag(self::projectModel(), '9.9.9');

        $this->assertInstanceOf(ResourceMissing::class, $result);
        $this->assertNull($result->status, 'no HTTP failure occurred, so no status may be claimed');
        $this->assertSame('missing', $result->shortCode());
        $this->assertFalse($result->isTransient(), 'an absent tag will still be absent on a retry');
        $this->assertStringNotContainsString('404', $result->message);
        $this->assertStringContainsString('9.9.9', $result->message);
        $this->assertSame('https://git.drupalcode.org/project/conditions_helper/-/tags', $result->browserUrl);
    }

    /**
     * When the tag lookup itself fails at the HTTP level, that failure must
     * propagate unchanged — it must not be flattened into "tag not found".
     */
    public function testATagLookupFailurePropagatesInsteadOfBecomingAMiss(): void
    {
        $client = $this->client([self::json(['message' => '403 Forbidden'], 403)]);

        $result = $client->mergedSinceTag(self::projectModel(), '1.0.1');

        $this->assertInstanceOf(EndpointClosed::class, $result);
        $this->assertSame(403, $result->status);
    }

    public function testSuccessStatusWithNonJsonBodyIsMalformedResponse(): void
    {
        $client = $this->client([new MockResponse('<html>gateway interstitial</html>', ['http_code' => 200])]);

        $result = $client->project('conditions_helper');

        $this->assertInstanceOf(MalformedResponse::class, $result);
        $this->assertSame(200, $result->status);
        $this->assertSame('malformed', $result->shortCode());
        $this->assertStringNotContainsString(self::TOKEN, $result->message);
    }

    public function testSuccessStatusWithScalarJsonBodyIsMalformedResponse(): void
    {
        $client = $this->client([new MockResponse('"just a string"', ['http_code' => 200])]);

        $result = $client->project('conditions_helper');

        $this->assertInstanceOf(MalformedResponse::class, $result);
        $this->assertSame(200, $result->status);
    }

    public function testNetworkFailureIsTransportErrorWithNoStatus(): void
    {
        $client = $this->client([new MockResponse('', ['error' => 'DNS resolution failed'])]);

        $result = $client->project('conditions_helper');

        $this->assertInstanceOf(TransportError::class, $result);
        $this->assertNull($result->status);
        $this->assertSame('transport', $result->shortCode());
        $this->assertStringContainsString('DNS resolution failed', $result->message);
        $this->assertStringNotContainsString(self::TOKEN, $result->message);
    }

    /**
     * @return array<string, array{\Throwable}>
     */
    public static function symfonyExceptionProvider(): array
    {
        return [
            'transport' => [new TransportException('connection reset by peer')],
            'timeout' => [new TimeoutException('idle timeout reached')],
            'server' => [new ServerException(new MockResponse('boom', ['http_code' => 500]))],
        ];
    }

    /**
     * Totality: the documented "never throws for HTTP-level outcomes" contract
     * must hold for EVERY symfony/http-client contract exception, not just the
     * two the client used to catch.
     */
    #[DataProvider('symfonyExceptionProvider')]
    public function testNoRawSymfonyHttpClientExceptionEscapes(\Throwable $thrown): void
    {
        $client = $this->client([$thrown]);

        $result = $client->project('conditions_helper');

        $this->assertInstanceOf(TransportError::class, $result);
        $this->assertStringNotContainsString(self::TOKEN, $result->message);
    }

    public function testNoRawSymfonyHttpClientExceptionEscapesFromAWriteEither(): void
    {
        $client = $this->client([new TimeoutException('idle timeout reached')]);

        $result = $client->merge(self::projectModel(), 2);

        $this->assertInstanceOf(TransportError::class, $result);
    }

    /**
     * @return array<string, array{MockResponse|\Throwable, class-string<ApiFailure>}>
     */
    public static function transientFailureProvider(): array
    {
        return [
            'rate limited' => [self::json([], 429), RateLimited::class],
            'server error' => [self::json(['message' => 'oops'], 503), RequestRejected::class],
            'malformed body' => [new MockResponse('<html>', ['http_code' => 200]), MalformedResponse::class],
            'transport' => [new MockResponse('', ['error' => 'connection reset']), TransportError::class],
        ];
    }

    /**
     * @param class-string<ApiFailure> $expected
     */
    #[DataProvider('transientFailureProvider')]
    public function testTransientFailuresAreNotMemoized(MockResponse|\Throwable $first, string $expected): void
    {
        $client = $this->client([$first, self::json([
            'id' => 181714,
            'path' => 'conditions_helper',
            'path_with_namespace' => 'project/conditions_helper',
            'name' => 'Conditions Helper',
            'web_url' => 'https://git.drupalcode.org/project/conditions_helper',
        ])]);

        $failed = $client->project('conditions_helper');
        $recovered = $client->project('conditions_helper');

        $this->assertInstanceOf($expected, $failed);
        $this->assertTrue($failed->isTransient());
        $this->assertInstanceOf(Project::class, $recovered);
        $this->assertCount(2, $this->requested, 'a transient failure must not be memoized for the rest of the run');
    }

    /**
     * @return array<string, array{int, class-string<ApiFailure>}>
     */
    public static function stableFailureProvider(): array
    {
        return [
            'unauthorized' => [401, Unauthorized::class],
            'endpoint closed' => [403, EndpointClosed::class],
            'not found' => [404, NotFound::class],
        ];
    }

    /**
     * @param class-string<ApiFailure> $expected
     */
    #[DataProvider('stableFailureProvider')]
    public function testStableFailuresStayMemoized(int $status, string $expected): void
    {
        $client = $this->client([self::json(['message' => 'nope'], $status)]);

        $first = $client->project('conditions_helper');
        $second = $client->project('conditions_helper');

        $this->assertInstanceOf($expected, $first);
        $this->assertFalse($first->isTransient());
        $this->assertSame($first, $second);
        $this->assertCount(1, $this->requested, 'a stable failure is still worth memoizing (rate-limit friendliness)');
    }

    public function testMembershipPaginationTreatsANonListResponseAsMalformedRatherThanLoopingForever(): void
    {
        // An HTTP 200 error object: neither a failure nor an empty page, so
        // the old unbounded loop never terminated.
        $client = $this->client([self::json(['message' => 'something went wrong'])]);

        $result = $client->membershipProjects();

        $this->assertInstanceOf(MalformedResponse::class, $result);
        $this->assertCount(1, $this->requested);
    }

    public function testACollectionRowThatIsNotAJsonObjectIsMalformedRatherThanAFatal(): void
    {
        // A list arrived, but one of its entries is not the JSON object the
        // endpoint promises. That is a protocol fault of the same family as a
        // JSON object where a list was promised, and it is caught at the
        // boundary rather than becoming a TypeError inside a model.
        $client = $this->client([self::json([['name' => '1.0.0', 'target' => 'bbb222'], 'not-an-object'])]);

        $result = $client->tags(self::projectModel());

        $this->assertInstanceOf(MalformedResponse::class, $result);
        $this->assertSame('malformed', $result->shortCode());
        $this->assertStringContainsString('tags', $result->message);
        $this->assertStringNotContainsString(self::TOKEN, $result->message);
    }

    public function testAMembershipPageRowThatIsNotAJsonObjectIsMalformed(): void
    {
        $client = $this->client([self::json([42])]);

        $result = $client->membershipProjects();

        $this->assertInstanceOf(MalformedResponse::class, $result);
        $this->assertStringContainsString('projects', $result->message);
        $this->assertCount(1, $this->requested, 'a malformed page ends the pagination immediately');
    }

    public function testMembershipPaginationIsCapped(): void
    {
        $page = [[
            'id' => 1,
            'path' => 'conditions_helper',
            'path_with_namespace' => 'project/conditions_helper',
            'name' => 'Conditions Helper',
            'web_url' => 'https://git.drupalcode.org/project/conditions_helper',
        ]];
        // A server that never returns an empty page must still terminate.
        $http = new MockHttpClient(function (string $method, string $url) use ($page): MockResponse {
            $this->requested[] = $url;

            return new MockResponse(json_encode($page, JSON_THROW_ON_ERROR), [
                'http_code' => 200,
                'response_headers' => ['content-type' => 'application/json'],
            ]);
        });
        $this->requested = [];

        $result = (new GitlabClient($http, self::TOKEN))->membershipProjects();

        $this->assertInstanceOf(MalformedResponse::class, $result);
        $this->assertSame(GitlabClient::MAX_PAGES, \count($this->requested));
    }

    /**
     * Every concrete failure type reachable above answers the two accessors
     * the command layer dispatches on — no reflection, no default arm.
     */
    public function testEveryConcreteFailureTypeIsSealedUnderApiFailure(): void
    {
        $concrete = [
            Unauthorized::class,
            EndpointClosed::class,
            NotFound::class,
            ResourceMissing::class,
            RateLimited::class,
            RequestRejected::class,
            MalformedResponse::class,
            TransportError::class,
        ];

        foreach ($concrete as $class) {
            $reflection = new \ReflectionClass($class);
            $this->assertTrue($reflection->isFinal(), $class . ' must be final');
            $this->assertTrue($reflection->isSubclassOf(ApiFailure::class), $class . ' must extend ApiFailure');
        }

        $this->assertTrue((new \ReflectionClass(ApiFailure::class))->isAbstract());
    }
}

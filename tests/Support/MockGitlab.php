<?php

declare(strict_types=1);

namespace Upkeep\Tests\Support;

use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Gitlab\GitlabClient;

/**
 * A GitLab client over a mocked HTTP transport, for tests that drive whole
 * commands.
 *
 * Routing is by URL substring, first registered match wins, so a test
 * declares only the endpoints its scenario reaches. An unrouted request
 * raises instead of returning an empty body: a command that suddenly calls
 * one more endpoint should fail loudly here rather than quietly take the
 * "API failure" branch and still look green.
 *
 * Requests are recorded so a test can assert that something was *not*
 * fetched — for the command tests, usually that no network call happened at
 * all because validation rejected the invocation first.
 */
final class MockGitlab
{
    /** @var list<array{needle: string, payload: array<array-key, mixed>, status: int}> */
    private array $routes = [];

    /** @var list<array{method: string, url: string}> every request the client made */
    public array $requests = [];

    public static function create(): self
    {
        return new self();
    }

    /**
     * @param string                  $urlNeedle a substring of the URL to match
     * @param array<array-key, mixed> $payload   the JSON body to answer with
     */
    public function route(string $urlNeedle, array $payload, int $status = 200): self
    {
        $this->routes[] = ['needle' => $urlNeedle, 'payload' => $payload, 'status' => $status];

        return $this;
    }

    public function client(string $token = 'glpat-fake-token-never-real'): GitlabClient
    {
        $factory = function (string $method, string $url): MockResponse {
            $this->requests[] = ['method' => $method, 'url' => $url];

            foreach ($this->routes as $route) {
                if (str_contains($url, $route['needle'])) {
                    return new MockResponse(
                        json_encode($route['payload'], \JSON_THROW_ON_ERROR),
                        [
                            'http_code' => $route['status'],
                            'response_headers' => ['content-type' => 'application/json'],
                        ],
                    );
                }
            }

            throw new \LogicException('Unrouted request in test: ' . $method . ' ' . $url);
        };

        return new GitlabClient(new MockHttpClient($factory), $token);
    }

    /**
     * A GitLab project payload for a drupalcode module project.
     *
     * @return array<string, mixed>
     */
    public static function projectPayload(string $module, int $id = 4242): array
    {
        return [
            'id' => $id,
            'path' => $module,
            'path_with_namespace' => 'project/' . $module,
            'name' => ucfirst($module),
            'web_url' => 'https://git.drupalcode.org/project/' . $module,
        ];
    }

    /**
     * An open merge-request payload; $overrides wins over every default.
     *
     * @param array<string, mixed> $overrides
     *
     * @return array<string, mixed>
     */
    public static function mergeRequestPayload(string $module, int $iid = 5, array $overrides = []): array
    {
        return $overrides + [
            'iid' => $iid,
            'title' => 'Issue #3489012: Add config schema for the settings form',
            'state' => 'opened',
            'draft' => false,
            'author' => ['username' => 'alice', 'id' => 100],
            'source_branch' => '3489012-add-config-schema',
            'target_branch' => '2.0.x',
            'detailed_merge_status' => 'mergeable',
            'sha' => 'abc123def456abc123def456abc123def456abcd',
            'web_url' => 'https://git.drupalcode.org/project/' . $module . '/-/merge_requests/' . $iid,
        ];
    }
}

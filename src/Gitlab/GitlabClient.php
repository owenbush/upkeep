<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

use Symfony\Contracts\HttpClient\Exception\TransportExceptionInterface;
use Symfony\Contracts\HttpClient\HttpClientInterface;

/**
 * Read/merge client for the git.drupalcode.org GitLab REST API (v4).
 *
 * - Authenticates with the maintainer's Git-access PAT via the PRIVATE-TOKEN
 *   header. The token is held privately and is never echoed into any result,
 *   message, or log output produced by this class.
 * - Built for a block-by-default instance: HTTP-level outcomes are returned as
 *   typed values (never thrown) — see ApiFailure.
 * - Rate-limit friendly: GET responses are memoized per client instance
 *   (= per command invocation), so the same resource is never fetched twice
 *   within one run.
 */
final class GitlabClient
{
    /**
     * Per-invocation GET memoization, keyed by full request URL. The client
     * lives for a single command run, so entries are never stale within the
     * consistency window the CLI cares about, and no resource is fetched
     * twice in one run (rate-limit friendliness).
     *
     * @var array<string, array|ApiFailure>
     */
    private array $getCache = [];

    public function __construct(
        private readonly HttpClientInterface $http,
        #[\SensitiveParameter] private readonly string $token,
        private readonly string $apiBase = 'https://git.drupalcode.org/api/v4',
        private readonly string $browserBase = 'https://git.drupalcode.org',
    ) {
    }

    /**
     * Look up a project by module name ("conditions_helper") or full
     * namespaced path ("project/conditions_helper").
     */
    public function project(string $moduleOrPath): Project|ApiFailure
    {
        $path = str_contains($moduleOrPath, '/') ? $moduleOrPath : 'project/' . $moduleOrPath;
        $data = $this->get(
            $this->apiBase . '/projects/' . rawurlencode($path),
            $this->browserBase . '/' . $path,
        );

        return $data instanceof ApiFailure ? $data : Project::fromApi($data);
    }

    /**
     * List open merge requests for a project.
     */
    public function openMergeRequests(Project $project): MergeRequestList|ApiFailure
    {
        $data = $this->get(
            $this->apiBase . '/projects/' . $project->id . '/merge_requests?state=opened&scope=all&per_page=100',
            $project->webUrl . '/-/merge_requests',
        );

        return $data instanceof ApiFailure ? $data : MergeRequestList::fromApi($data);
    }

    /**
     * Fetch a single merge request; includes head_pipeline and
     * detailed_merge_status.
     */
    public function mergeRequest(Project $project, int $iid): MergeRequest|ApiFailure
    {
        $data = $this->get(
            $this->apiBase . '/projects/' . $project->id . '/merge_requests/' . $iid,
            $this->mergeRequestBrowserUrl($project, $iid),
        );

        return $data instanceof ApiFailure ? $data : MergeRequest::fromApi($data);
    }

    /**
     * Head pipeline of a merge request, or null when the MR has none.
     * Sourced from the single-MR endpoint (memoized together with
     * mergeRequest(), so combined use costs one request).
     */
    public function headPipeline(Project $project, int $iid): Pipeline|ApiFailure|null
    {
        $mr = $this->mergeRequest($project, $iid);

        return $mr instanceof ApiFailure ? $mr : $mr->headPipeline;
    }

    /**
     * Repository tags, newest first (GitLab default ordering).
     *
     * @return list<Tag>|ApiFailure
     */
    public function tags(Project $project): array|ApiFailure
    {
        $data = $this->get(
            $this->apiBase . '/projects/' . $project->id . '/repository/tags',
            $project->webUrl . '/-/tags',
        );

        return $data instanceof ApiFailure
            ? $data
            : array_map(Tag::fromApi(...), array_values($data));
    }

    /**
     * Merge requests merged (well: in merged state, updated) since a moment —
     * the release-notes source.
     */
    public function mergedSince(Project $project, \DateTimeImmutable $since): MergeRequestList|ApiFailure
    {
        $query = http_build_query([
            'state' => 'merged',
            'scope' => 'all',
            'per_page' => 100,
            'updated_after' => $since->format(\DateTimeInterface::ATOM),
        ]);
        $data = $this->get(
            $this->apiBase . '/projects/' . $project->id . '/merge_requests?' . $query,
            $project->webUrl . '/-/merge_requests?state=merged',
        );

        return $data instanceof ApiFailure ? $data : MergeRequestList::fromApi($data);
    }

    /**
     * Merge requests merged since the given tag was created. Resolves the
     * tag's commit date via tags(), then delegates to mergedSince(). An
     * unknown tag is a typed NotFound, not an exception.
     */
    public function mergedSinceTag(Project $project, string $tagName): MergeRequestList|ApiFailure
    {
        $tags = $this->tags($project);
        if ($tags instanceof ApiFailure) {
            return $tags;
        }
        foreach ($tags as $tag) {
            if ($tag->name === $tagName && $tag->createdAt !== null) {
                return $this->mergedSince($project, $tag->createdAt);
            }
        }

        return new NotFound($project->webUrl . '/-/tags');
    }

    /**
     * Merge exactly one merge request.
     *
     * Deliberately a single-action call: one Project, one IID, one PUT — there
     * is no batch variant at any layer of this client. That enforces the
     * DA-policy stance (one human-approved action at a time) structurally.
     *
     * $expectedHeadSha, when given, is passed as the API's "sha" guard so the
     * merge is refused if the branch moved since the human reviewed it.
     *
     * Note: this endpoint is unverified on git.drupalcode.org (block-by-default
     * instance); a 403 EndpointClosed carrying the MR's browser URL is an
     * expected outcome and the caller's degraded path.
     */
    public function merge(Project $project, int $iid, ?string $expectedHeadSha = null): MergeRequest|ApiFailure
    {
        $data = $this->request(
            'PUT',
            $this->apiBase . '/projects/' . $project->id . '/merge_requests/' . $iid . '/merge',
            ['json' => $expectedHeadSha === null ? new \stdClass() : ['sha' => $expectedHeadSha]],
            $this->mergeRequestBrowserUrl($project, $iid),
        );

        return $data instanceof ApiFailure ? $data : MergeRequest::fromApi($data);
    }

    private function mergeRequestBrowserUrl(Project $project, int $iid): string
    {
        return $project->webUrl . '/-/merge_requests/' . $iid;
    }

    /**
     * Perform a GET and decode the JSON body, or return a typed failure.
     */
    private function get(string $url, string $browserUrl): array|ApiFailure
    {
        return $this->getCache[$url] ??= $this->request('GET', $url, [], $browserUrl);
    }

    /**
     * Perform a request and decode the JSON body, or return a typed failure.
     * Never throws for HTTP-level outcomes and never places the token in a
     * message: the token travels only in the PRIVATE-TOKEN header.
     */
    private function request(string $method, string $url, array $extraOptions, string $browserUrl): array|ApiFailure
    {
        try {
            $response = $this->http->request($method, $url, $extraOptions + [
                'headers' => ['PRIVATE-TOKEN' => $this->token],
            ]);
            $status = $response->getStatusCode();
            if ($status === 401 || $status === 403) {
                return new EndpointClosed($status, $browserUrl);
            }
            if ($status === 404) {
                return new NotFound($browserUrl);
            }
            if ($status === 429) {
                $retryAfter = $response->getHeaders(false)['retry-after'][0] ?? null;

                return new RateLimited(is_numeric($retryAfter) ? (int) $retryAfter : null);
            }
            $body = $response->getContent(false);
            if ($status >= 400) {
                return new TransportError($this->httpErrorMessage($status, $body, $browserUrl), $status);
            }

            $decoded = json_decode($body, true, 512, JSON_THROW_ON_ERROR);
            if (!\is_array($decoded)) {
                return new TransportError(sprintf('Unexpected non-JSON-object response (HTTP %d) from %s', $status, $url), $status);
            }

            return $decoded;
        } catch (TransportExceptionInterface $e) {
            return new TransportError('HTTP transport failure: ' . $e->getMessage());
        } catch (\JsonException $e) {
            return new TransportError(sprintf('Undecodable response body from %s: %s', $url, $e->getMessage()));
        }
    }

    /**
     * Build a status-bearing error message from a GitLab error body without
     * ever echoing credentials (only the body's "message"/"error" field is
     * quoted, never request headers).
     */
    private function httpErrorMessage(int $status, string $body, string $browserUrl): string
    {
        $detail = null;
        try {
            $decoded = json_decode($body, true, 512, JSON_THROW_ON_ERROR);
            if (\is_array($decoded)) {
                $detail = $decoded['message'] ?? $decoded['error'] ?? null;
                if (\is_array($detail)) {
                    $detail = implode('; ', array_map(strval(...), $detail));
                }
            }
        } catch (\JsonException) {
            // Non-JSON error body: report the status only.
        }

        return sprintf(
            'GitLab rejected the request (HTTP %d)%s. Browser fallback: %s',
            $status,
            \is_string($detail) && $detail !== '' ? ': ' . $detail : '',
            $browserUrl,
        );
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

use Symfony\Contracts\HttpClient\Exception\ExceptionInterface as HttpClientExceptionInterface;
use Symfony\Contracts\HttpClient\HttpClientInterface;

/**
 * Read/merge client for the git.drupalcode.org GitLab REST API (v4).
 *
 * - Authenticates with the maintainer's Git-access PAT via the PRIVATE-TOKEN
 *   header. The token is held privately and is never echoed into any result,
 *   message, or log output produced by this class.
 * - Built for a block-by-default instance: every failure mode — HTTP status,
 *   unusable body, or no response at all — is returned as one of the sealed
 *   ApiFailure subtypes. Nothing escapes as a symfony/http-client exception;
 *   see ApiFailure for the full condition table.
 * - Rate-limit friendly: GET responses are memoized per client instance
 *   (= per command invocation), so the same resource is never fetched twice
 *   within one run.
 */
final class GitlabClient
{
    /**
     * Pagination guard. `membershipProjects()` trusts the server to eventually
     * hand back an empty page; this bounds what "eventually" may mean so a
     * misbehaving endpoint cannot spin forever issuing requests.
     */
    public const MAX_PAGES = 50;

    /** Idle timeout: the longest gap tolerated between response chunks. */
    private const IDLE_TIMEOUT = 15.0;

    /**
     * Total per-request cap. Every call here is a small JSON document or a
     * single merge; none has a legitimate reason to take a minute, and an
     * unbounded one is indistinguishable from a hang.
     */
    private const MAX_DURATION = 60.0;

    /**
     * Per-invocation GET memoization, keyed by full request URL. The client
     * lives for a single command run, so entries are never stale within the
     * consistency window the CLI cares about, and no resource is fetched
     * twice in one run (rate-limit friendliness).
     *
     * Successes and STABLE failures only: a transient failure (rate limit,
     * network blip, gateway interstitial) must not fail that resource for the
     * rest of the run — see ApiFailure::isTransient().
     *
     * @var array<string, array<array-key, mixed>|ApiFailure>
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
     * A copy of this client with an empty GET-memoization cache.
     *
     * Within one run, GETs are intentionally memoized (rate-limit
     * friendliness) — but the freshness re-check immediately before a
     * fast-lane merge must observe the MR as it is NOW, not as memoized at
     * row-assembly time. That re-check goes through a fresh() copy; the
     * original instance's cache is left untouched.
     */
    public function fresh(): self
    {
        $copy = clone $this;
        $copy->getCache = [];

        return $copy;
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
        $url = $this->apiBase . '/projects/' . $project->id
            . '/merge_requests?state=opened&scope=all&per_page=100';
        $browserUrl = $project->webUrl . '/-/merge_requests';
        $data = $this->get($url, $browserUrl);
        if ($data instanceof ApiFailure) {
            return $data;
        }
        $rows = self::objectRows($data, 'merge requests', $url, $browserUrl);

        return $rows instanceof ApiFailure ? $rows : MergeRequestList::fromApi($rows);
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
     * Every project the token holder is a member of — for a Drupal.org
     * maintainer, their maintained projects. Paginates until an empty page;
     * callers filter namespaces (contrib modules live under `project/`).
     *
     * Only two things end the loop legitimately: a typed failure, or an empty
     * page. Anything else — a JSON object where a list was promised, or a
     * server that never stops handing back full pages — is a protocol fault
     * and ends as MalformedResponse rather than as an unbounded request loop.
     *
     * @return list<Project>|ApiFailure
     */
    public function membershipProjects(): array|ApiFailure
    {
        $projects = [];
        $browserUrl = $this->browserBase . '/dashboard/projects';
        for ($page = 1; $page <= self::MAX_PAGES; ++$page) {
            $url = $this->apiBase . '/projects?membership=true&simple=true&per_page=100&page=' . $page;
            $data = $this->get($url, $browserUrl);
            if ($data instanceof ApiFailure) {
                return $data;
            }
            $rows = self::objectRows($data, 'projects', $url, $browserUrl);
            if ($rows instanceof ApiFailure) {
                return $rows;
            }
            if ($rows === []) {
                return $projects;
            }
            foreach ($rows as $item) {
                $projects[] = Project::fromApi($item);
            }
        }

        return new MalformedResponse(
            sprintf(
                'Project membership pagination did not reach an empty page within %d pages; giving up.',
                self::MAX_PAGES,
            ),
            null,
            $browserUrl,
        );
    }

    /**
     * Repository tags, newest first (GitLab default ordering).
     *
     * @return list<Tag>|ApiFailure
     */
    public function tags(Project $project): array|ApiFailure
    {
        $url = $this->apiBase . '/projects/' . $project->id . '/repository/tags';
        $browserUrl = $project->webUrl . '/-/tags';
        $data = $this->get($url, $browserUrl);
        if ($data instanceof ApiFailure) {
            return $data;
        }
        $rows = self::objectRows($data, 'tags', $url, $browserUrl);

        return $rows instanceof ApiFailure ? $rows : array_map(Tag::fromApi(...), $rows);
    }

    /**
     * Merge requests merged (well: in merged state, updated) since a moment —
     * the release-notes source.
     *
     * Known limitation: GitLab's list API cannot filter on merge date, so
     * this filters `state=merged` by `updated_after`. Every MR merged after
     * $since is included (merging bumps updated_at), but an MR merged BEFORE
     * $since and touched afterwards (a comment, a relabel) appears too — the
     * result is a superset keyed on update time, not merge time. Acceptable
     * here because the output is a release-notes DRAFT that a human reviews
     * before publishing; pinned by GitlabClientTest's mergedSince tests.
     */
    public function mergedSince(Project $project, \DateTimeImmutable $since): MergeRequestList|ApiFailure
    {
        $query = http_build_query([
            'state' => 'merged',
            'scope' => 'all',
            'per_page' => 100,
            'updated_after' => $since->format(\DateTimeInterface::ATOM),
        ]);
        $url = $this->apiBase . '/projects/' . $project->id . '/merge_requests?' . $query;
        $browserUrl = $project->webUrl . '/-/merge_requests?state=merged';
        $data = $this->get($url, $browserUrl);
        if ($data instanceof ApiFailure) {
            return $data;
        }
        $rows = self::objectRows($data, 'merge requests', $url, $browserUrl);

        return $rows instanceof ApiFailure ? $rows : MergeRequestList::fromApi($rows);
    }

    /**
     * Merge requests merged since the given tag was created. Resolves the
     * tag's commit date via tags(), then delegates to mergedSince().
     *
     * An unknown tag is a domain-level miss, not an HTTP one: the tag list
     * came back fine, it just does not contain that tag. It is therefore
     * ResourceMissing (status null) and never NotFound, so no message ever
     * claims an HTTP 404 that did not happen.
     */
    public function mergedSinceTag(Project $project, string $tagName): MergeRequestList|ApiFailure
    {
        $tags = $this->tags($project);
        if ($tags instanceof ApiFailure) {
            return $tags;
        }
        $browserUrl = $project->webUrl . '/-/tags';
        foreach ($tags as $tag) {
            if ($tag->name !== $tagName) {
                continue;
            }
            if ($tag->createdAt === null) {
                return new MalformedResponse(
                    sprintf(
                        'Tag "%s" in %s carries no commit date, so "merged since that tag" cannot be resolved.',
                        $tagName,
                        $project->pathWithNamespace,
                    ),
                    null,
                    $browserUrl,
                );
            }

            return $this->mergedSince($project, $tag->createdAt);
        }

        return new ResourceMissing(sprintf('tag "%s"', $tagName), $project->pathWithNamespace, $browserUrl);
    }

    /**
     * Post a note (comment) on a merge request.
     */
    public function postNote(Project $project, int $iid, string $body): true|ApiFailure
    {
        $data = $this->request(
            'POST',
            $this->apiBase . '/projects/' . $project->id . '/merge_requests/' . $iid . '/notes',
            ['json' => ['body' => $body]],
            $this->mergeRequestBrowserUrl($project, $iid),
        );

        return $data instanceof ApiFailure ? $data : true;
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
     * The collection half of the boundary: narrow a decoded body into the list
     * of JSON objects a collection endpoint promises, or say why it is not one.
     *
     * Two structural faults are caught here, both of which a model's tolerant
     * field narrowing (see ApiPayload) cannot paper over: a JSON object where a
     * list was promised — the shape that used to make pagination loop forever —
     * and a list carrying an entry that is not an object, which used to reach a
     * model as a scalar and fatal. After this, every row handed to a
     * `fromApi()` is a real array and nothing inward sees `mixed`.
     *
     * @param array<array-key, mixed> $data
     * @param string $what plural noun for the collection, e.g. "tags"
     * @return list<array<array-key, mixed>>|MalformedResponse
     */
    private static function objectRows(
        array $data,
        string $what,
        string $url,
        string $browserUrl,
    ): array|MalformedResponse {
        if (!array_is_list($data)) {
            return new MalformedResponse(
                sprintf('Expected a list of %s from %s, got a JSON object.', $what, $url),
                200,
                $browserUrl,
            );
        }

        $rows = [];
        foreach ($data as $index => $item) {
            if (!\is_array($item)) {
                return new MalformedResponse(
                    sprintf(
                        'Expected the list of %s from %s to hold JSON objects; entry %d is %s.',
                        $what,
                        $url,
                        $index,
                        get_debug_type($item),
                    ),
                    200,
                    $browserUrl,
                );
            }
            $rows[] = $item;
        }

        return $rows;
    }

    /**
     * Perform a GET and decode the JSON body, or return a typed failure.
     *
     * Memoizes successes and stable failures. A transient failure is returned
     * but NOT stored: a rate limit or a network blip midway through a long
     * dashboard run must not turn into a permanent verdict for that resource.
     *
     * @return array<array-key, mixed>|ApiFailure
     */
    private function get(string $url, string $browserUrl): array|ApiFailure
    {
        if (\array_key_exists($url, $this->getCache)) {
            return $this->getCache[$url];
        }

        $result = $this->request('GET', $url, [], $browserUrl);
        if (!$result instanceof ApiFailure || !$result->isTransient()) {
            $this->getCache[$url] = $result;
        }

        return $result;
    }

    /**
     * Perform a request and decode the JSON body, or return a typed failure.
     *
     * Total by construction: every symfony/http-client contract exception is
     * caught, so the documented "never throws for HTTP-level outcomes" holds
     * for redirection/client/server/decoding failures too, not just transport
     * ones. The token never reaches a message — it travels only in the
     * PRIVATE-TOKEN header, and error text is taken from the response body.
     *
     * @param array<string, mixed> $extraOptions
     * @return array<array-key, mixed>|ApiFailure
     */
    private function request(string $method, string $url, array $extraOptions, string $browserUrl): array|ApiFailure
    {
        try {
            $response = $this->http->request($method, $url, $extraOptions + [
                'headers' => ['PRIVATE-TOKEN' => $this->token],
                // Symfony's `timeout` is the *idle* timeout — the gap allowed
                // between chunks — so on its own it bounds nothing: a response
                // trickling a byte just inside it stays alive indefinitely.
                // Without both, a stalled git.drupalcode.org hangs the command
                // with no output and no way to tell it apart from slow work.
                'timeout' => self::IDLE_TIMEOUT,
                'max_duration' => self::MAX_DURATION,
            ]);
            $status = $response->getStatusCode();
            if ($status === 429) {
                $retryAfter = $response->getHeaders(false)['retry-after'][0] ?? null;

                return new RateLimited(is_numeric($retryAfter) ? (int) $retryAfter : null, $browserUrl);
            }
            if ($status >= 400) {
                return match ($status) {
                    401 => new Unauthorized($browserUrl),
                    403 => new EndpointClosed($browserUrl),
                    404 => new NotFound($browserUrl),
                    default => new RequestRejected(
                        $status,
                        self::errorDetail($response->getContent(false)),
                        $browserUrl,
                    ),
                };
            }

            $body = $response->getContent(false);
            $decoded = json_decode($body, true);
            if (!\is_array($decoded)) {
                return new MalformedResponse(
                    sprintf(
                        'Unusable response body (HTTP %d) from %s: %s',
                        $status,
                        $url,
                        json_last_error() === JSON_ERROR_NONE
                            ? 'expected a JSON object or array, got ' . get_debug_type($decoded)
                            : json_last_error_msg(),
                    ),
                    $status,
                    $browserUrl,
                );
            }

            return $decoded;
        } catch (HttpClientExceptionInterface $e) {
            return new TransportError('HTTP transport failure: ' . $e->getMessage(), $browserUrl);
        }
    }

    /**
     * The human-readable complaint out of a GitLab error body, if it has one.
     * Only the body's "message"/"error" field is read — never request headers,
     * so no credential can be quoted back.
     */
    private static function errorDetail(string $body): ?string
    {
        $decoded = json_decode($body, true);
        if (!\is_array($decoded)) {
            return null;
        }

        $detail = $decoded['message'] ?? $decoded['error'] ?? null;
        if (\is_array($detail)) {
            // GitLab returns either a string or a list of complaints. Only the
            // scalar entries are quotable; a nested structure has no sensible
            // one-line rendering and is dropped rather than printed as "Array".
            $parts = [];
            foreach ($detail as $entry) {
                if (\is_scalar($entry)) {
                    $parts[] = (string) $entry;
                }
            }
            $detail = implode('; ', $parts);
        }

        return \is_string($detail) && $detail !== '' ? $detail : null;
    }
}

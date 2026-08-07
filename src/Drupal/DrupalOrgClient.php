<?php

declare(strict_types=1);

namespace Upkeep\Drupal;

use Symfony\Contracts\HttpClient\HttpClientInterface;
use Symfony\Contracts\HttpClient\ResponseInterface;

/**
 * Read-only client for the drupal.org REST API (Drupal 7 Services).
 *
 * The API is public and requires no authentication for reads. Fetched issues
 * are memoized per instance (same model as GitlabClient).
 *
 * **Failures are absorbed but never silent.** Every request that does not
 * answer is recorded in warnings(), because this client's failure mode is to
 * return *less data*, not an error: a dropped attachment makes an issue look
 * like it carries fewer patches, and a truncated listing page makes a project
 * look like it has fewer issues. Both are indistinguishable from the truth at
 * the call site, and both are exactly the under-reporting the patch surface
 * exists to prevent. The caller renders the warnings; nothing here throws.
 *
 * **Attachment lookups run concurrently.** api-d7 returns every attachment as
 * a bare reference, so an issue listing costs one request per distinct file,
 * and drupal.org's origin is slow when its Varnish cache is cold — measured at
 * ~530ms per request against ~20ms warm. Serialised, a busy module took ~52s;
 * at MAX_CONCURRENT in flight the same lookups take a few seconds.
 */
final class DrupalOrgClient
{
    /**
     * How many attachment lookups may be in flight at once.
     *
     * Deliberately modest. api-d7 publishes no rate-limit headers and no
     * documented quota, so there is no budget to read and stay inside — which
     * makes politeness the only available policy. Eight was measured as taking
     * essentially all of the available speedup (13x on cold lookups) while
     * staying well inside what the endpoint answers cleanly.
     */
    public const MAX_CONCURRENT = 8;

    /**
     * A 429 is retried once, after at most this long. Anything demanding a
     * longer wait is reported rather than slept through: a CLI that appears to
     * hang is worse than one that says it was throttled.
     */
    private const MAX_RETRY_AFTER_SECONDS = 10;

    /** @var array<int, ?Issue> */
    private array $cache = [];

    /** @var array<string, ?int> project machine name => node id, misses kept */
    private array $projectNids = [];

    /** @var array<int, array<array-key, mixed>|null> file id => file resource, misses kept */
    private array $fileDetails = [];

    /** @var list<string> what this client could not read, in the order it happened */
    private array $warnings = [];

    /** @var \Closure(int): void */
    private readonly \Closure $sleeper;

    /**
     * @param ?\Closure(int): void $sleeper the throttle-retry wait; injected in
     *                                      tests so no suite ever really sleeps
     */
    public function __construct(
        private readonly HttpClientInterface $http,
        private readonly string $apiBase = 'https://www.drupal.org/api-d7',
        ?\Closure $sleeper = null,
    ) {
        $this->sleeper = $sleeper ?? static function (int $seconds): void {
            sleep($seconds);
        };
    }

    /**
     * Everything this client failed to read during the run.
     *
     * Never empty *and* complete at the same time: if there are warnings, the
     * data returned is a subset of what drupal.org holds, and any count derived
     * from it is a lower bound.
     *
     * @return list<string>
     */
    public function warnings(): array
    {
        return $this->warnings;
    }

    private function warn(string $message): void
    {
        $this->warnings[] = $message;
    }

    public function issue(int $nid): ?Issue
    {
        return $this->cache[$nid] ??= $this->fetch($nid);
    }

    /**
     * Every issue of a project in the given statuses.
     *
     * Filtering is by the project's node id, not its machine name: api-d7
     * exposes `field_project_machine_name` on *project* nodes only, so asking
     * the issue listing for one matches nothing at all — an empty result that
     * reads exactly like "this project has no issues in that status". The
     * machine name is therefore resolved to a node id first (once per project
     * per run), and the listing is filtered on `field_project`.
     *
     * @param list<IssueStatus> $statuses
     * @return list<Issue>
     */
    public function projectIssues(string $machineName, array $statuses): array
    {
        $projectNid = $this->projectNid($machineName);
        if ($projectNid === null) {
            return [];
        }

        $issues = [];
        foreach ($statuses as $status) {
            foreach ($this->fetchProjectIssues($projectNid, $status) as $issue) {
                $issues[$issue->nid] = $issue;
            }
        }

        return array_values($issues);
    }

    /** The node id of a project, by machine name; memoized, misses included. */
    private function projectNid(string $machineName): ?int
    {
        if (!\array_key_exists($machineName, $this->projectNids)) {
            $this->projectNids[$machineName] = $this->fetchProjectNid($machineName);
        }

        return $this->projectNids[$machineName];
    }

    private function fetchProjectNid(string $machineName): ?int
    {
        // Deliberately unfiltered by node type: only project nodes carry
        // field_project_machine_name, and a maintainer's registry may name a
        // theme or a distribution as readily as a module.
        $data = $this->getJson(
            sprintf('%s/node.json?field_project_machine_name=%s', $this->apiBase, urlencode($machineName)),
            sprintf('the drupal.org project "%s"', $machineName),
        );

        $list = $data['list'] ?? null;
        if (!\is_array($list)) {
            return null;
        }

        foreach ($list as $entry) {
            if (!\is_array($entry)) {
                continue;
            }
            $nid = (new ApiPayload($entry))->intOrNull('nid');
            if ($nid !== null) {
                return $nid;
            }
        }

        return null;
    }

    private function fetch(int $nid): ?Issue
    {
        $data = $this->getJson(
            sprintf('%s/node/%d.json', $this->apiBase, $nid),
            sprintf('issue #%d', $nid),
        );
        if ($data === []) {
            return null;
        }

        $this->prefetchAttachments([$data]);

        return Issue::fromApi($this->withResolvedFiles($data));
    }

    /**
     * One status's issue listing, paginated.
     *
     * A page that cannot be read ends the listing — there is no way to skip
     * past it and stay in order — but it says so, because "the rest of this
     * project's issues" silently missing is the failure this whole surface
     * exists to prevent.
     *
     * @return list<Issue>
     */
    private function fetchProjectIssues(int $projectNid, IssueStatus $status): array
    {
        $issues = [];
        $page = 0;

        do {
            $url = sprintf(
                '%s/node.json?type=project_issue&field_project=%d&field_issue_status=%d&page=%d',
                $this->apiBase,
                $projectNid,
                $status->value,
                $page,
            );

            $data = $this->getJson($url, sprintf('%s issues, page %d', $status->shortLabel(), $page + 1), 15);
            if ($data === []) {
                if ($page > 0) {
                    $this->warn(sprintf(
                        'Stopped reading %s issues after page %d; the listing is incomplete.',
                        $status->shortLabel(),
                        $page,
                    ));
                }
                break;
            }

            $list = $data['list'] ?? [];
            if (!\is_array($list) || $list === []) {
                break;
            }

            // Every attachment on the page, resolved in one concurrent sweep,
            // so building the issues below costs no further requests.
            $items = array_values(array_filter($list, \is_array(...)));
            $this->prefetchAttachments($items);

            foreach ($items as $item) {
                $issue = Issue::fromApi($this->withResolvedFiles($item));
                if ($issue !== null) {
                    $issues[] = $issue;
                    $this->cache[$issue->nid] = $issue;
                }
            }

            $hasMore = isset($data['next']) && $data['next'] !== '';
            ++$page;
        } while ($hasMore && $page < 10);

        return $issues;
    }

    /**
     * Resolve every not-yet-known attachment across a batch of issue payloads,
     * a bounded number of requests at a time.
     *
     * @param list<array<array-key, mixed>> $items
     */
    private function prefetchAttachments(array $items): void
    {
        $wanted = [];
        foreach ($items as $item) {
            foreach (self::attachmentIds($item) as $fid) {
                if (!\array_key_exists($fid, $this->fileDetails)) {
                    $wanted[$fid] = true;
                }
            }
        }

        foreach (array_chunk(array_keys($wanted), self::MAX_CONCURRENT) as $chunk) {
            $this->fetchFileBatch($chunk, true);
        }
    }

    /**
     * One in-flight batch of file lookups.
     *
     * Concurrency comes from *creating* the requests before consuming any:
     * symfony/http-client starts each one immediately and multiplexes them, so
     * consuming them in order below still overlaps the waiting. Deliberately
     * not stream(): that yields HTTP errors by throwing out of the generator,
     * where a per-response catch cannot reach them, and one 404 attachment
     * would take the whole batch down with it.
     *
     * @param list<int> $fids
     * @param bool      $mayRetry whether a throttled batch may be slept off once
     */
    private function fetchFileBatch(array $fids, bool $mayRetry): void
    {
        $responses = [];
        foreach ($fids as $fid) {
            try {
                $responses[$fid] = $this->http->request(
                    'GET',
                    sprintf('%s/file/%d.json', $this->apiBase, $fid),
                    self::requestOptions(10),
                );
            } catch (\Throwable $e) {
                $this->recordFileMiss($fid, self::reason($e));
            }
        }

        $throttled = [];
        $retryAfter = 0;

        foreach ($responses as $fid => $response) {
            try {
                $status = $response->getStatusCode();
                if ($status === 429) {
                    $throttled[] = $fid;
                    $retryAfter = max($retryAfter, self::retryAfterSeconds($response));
                    continue;
                }
                if ($status !== 200) {
                    $this->recordFileMiss($fid, sprintf('HTTP %d', $status));
                    continue;
                }

                $decoded = json_decode($response->getContent(false), true, 512, \JSON_THROW_ON_ERROR);
                if (\is_array($decoded) && $decoded !== []) {
                    $this->fileDetails[$fid] = $decoded;
                    continue;
                }
                $this->recordFileMiss($fid, 'the response was not a file object');
            } catch (\Throwable $e) {
                $this->recordFileMiss($fid, self::reason($e));
            }
        }

        if ($throttled === []) {
            return;
        }

        if (!$mayRetry || $retryAfter > self::MAX_RETRY_AFTER_SECONDS) {
            foreach ($throttled as $fid) {
                $this->recordFileMiss($fid, 'drupal.org is rate-limiting this run (HTTP 429)');
            }

            return;
        }

        $this->warn(sprintf(
            'drupal.org returned HTTP 429 for %d attachment lookup(s); waiting %ds and retrying once.',
            \count($throttled),
            max(1, $retryAfter),
        ));
        ($this->sleeper)(max(1, $retryAfter));
        $this->fetchFileBatch($throttled, false);
    }

    /**
     * Only ever called after getStatusCode() has already answered 429, so the
     * headers are known to be readable and need no second guard.
     */
    private static function retryAfterSeconds(ResponseInterface $response): int
    {
        $header = $response->getHeaders(false)['retry-after'][0] ?? null;

        return is_numeric($header) ? max(1, (int) $header) : 1;
    }

    private function recordFileMiss(int $fid, string $reason): void
    {
        $this->fileDetails[$fid] = null;
        $this->warn(sprintf(
            'Attachment %d could not be read (%s); an issue may report fewer patches than it has.',
            $fid,
            $reason,
        ));
    }

    private static function reason(\Throwable $e): string
    {
        $message = trim($e->getMessage());

        return $message === '' ? $e::class : $message;
    }

    /**
     * The file ids an issue payload references, in either field shape.
     *
     * @param array<array-key, mixed> $item
     * @return list<int>
     */
    private static function attachmentIds(array $item): array
    {
        $attachments = $item['field_issue_files'] ?? null;
        if (!\is_array($attachments)) {
            return [];
        }

        $envelope = $attachments['und'] ?? null;
        $entries = \is_array($envelope) ? $envelope : $attachments;

        $fids = [];
        foreach ($entries as $entry) {
            if (!\is_array($entry)) {
                continue;
            }
            $file = $entry['file'] ?? null;
            if (!\is_array($file) || isset($file['name']) || isset($file['filename'])) {
                continue;
            }
            $payload = new ApiPayload($file);
            $fid = $payload->intOrNull('id') ?? $payload->intOrNull('fid');
            if ($fid !== null) {
                $fids[] = $fid;
            }
        }

        return $fids;
    }

    /**
     * An issue payload with its attachments dereferenced.
     *
     * api-d7 never inlines an attachment. Both the issue listing and the
     * single-node endpoint return each one as a bare reference —
     * `{"file": {"uri": ".../file/7128077", "id": "7128077", "resource":
     * "file"}}` — carrying no name, no URL and no timestamp. Read as-is, every
     * issue on drupal.org has zero attachments, which is why a patch report
     * built on them had nothing to report. The name that decides whether an
     * attachment is a patch at all lives one request away, on the file
     * resource, so that request is made here — once per distinct file per run.
     *
     * Attachments that already carry their own detail are left alone, so a
     * payload from a cached snapshot (which stores the resolved shape) costs
     * nothing.
     *
     * @param array<array-key, mixed> $data
     * @return array<array-key, mixed>
     */
    private function withResolvedFiles(array $data): array
    {
        $attachments = $data['field_issue_files'] ?? null;
        if (!\is_array($attachments)) {
            return $data;
        }

        // The Drupal 7 field-language envelope ({"und": [...]}) or a plain
        // list, depending on the endpoint; both are rewritten in place.
        $envelope = $attachments['und'] ?? null;
        $entries = \is_array($envelope) ? $envelope : $attachments;

        $resolved = [];
        foreach ($entries as $entry) {
            $resolved[] = \is_array($entry) ? $this->resolveAttachment($entry) : $entry;
        }

        $data['field_issue_files'] = \is_array($envelope) ? ['und' => $resolved] : $resolved;

        return $data;
    }

    /**
     * @param array<array-key, mixed> $entry
     * @return array<array-key, mixed>
     */
    private function resolveAttachment(array $entry): array
    {
        $file = $entry['file'] ?? null;
        if (!\is_array($file)) {
            return $entry;
        }

        // Already resolved — a snapshot payload, or a shape that inlines the
        // detail. Nothing to fetch.
        if (isset($file['name']) || isset($file['filename'])) {
            return $entry;
        }

        $payload = new ApiPayload($file);
        $fid = $payload->intOrNull('id') ?? $payload->intOrNull('fid');
        if ($fid === null) {
            return $entry;
        }

        // prefetchAttachments() has already visited every attachment this
        // payload references — successes and misses alike — so the memo holds
        // an entry for each; null is a recorded miss, not an unasked question.
        $detail = $this->fileDetails[$fid] ?? null;

        // A file that cannot be read stays a reference rather than becoming a
        // half-built attachment: IssueFile::fromApi drops it, and an issue
        // reports one attachment fewer instead of one nameless one.
        return $detail === null ? $entry : ['file' => $detail] + $entry;
    }

    /**
     * Request options every call shares.
     *
     * `max_duration` is the load-bearing one. Symfony's `timeout` is the *idle*
     * timeout — the gap allowed between chunks — so on its own it bounds
     * nothing: a response trickling a byte just inside it keeps the request
     * alive indefinitely. api-d7 sits behind a cache whose origin is slow when
     * cold, which is exactly the shape that produces long dribbling responses,
     * so a total cap is set as well.
     *
     * @return array<string, mixed>
     */
    private static function requestOptions(float $idleTimeout): array
    {
        return [
            'headers' => ['Accept' => 'application/json'],
            'timeout' => $idleTimeout,
            'max_duration' => $idleTimeout * 4,
        ];
    }

    /**
     * A GET whose every failure mode — status, transport, unusable body —
     * reads as the empty array, and is reported. This client absorbs failures
     * by contract: the caller gets less data, never an exception, but never
     * less data *silently*.
     *
     * @return array<array-key, mixed>
     */
    private function getJson(string $url, string $what, float $idleTimeout = 10): array
    {
        try {
            $response = $this->http->request('GET', $url, self::requestOptions($idleTimeout));

            $status = $response->getStatusCode();
            if ($status === 429) {
                $this->warn(sprintf('drupal.org is rate-limiting this run; %s was not read (HTTP 429).', $what));

                return [];
            }
            if ($status !== 200) {
                $this->warn(sprintf('%s could not be read (HTTP %d).', ucfirst($what), $status));

                return [];
            }

            $data = json_decode($response->getContent(false), true, 512, \JSON_THROW_ON_ERROR);
            if (!\is_array($data)) {
                $this->warn(sprintf('%s came back in a shape this client cannot read.', ucfirst($what)));

                return [];
            }

            return $data;
        } catch (\Throwable $e) {
            $this->warn(sprintf('%s could not be read (%s).', ucfirst($what), self::reason($e)));

            return [];
        }
    }
}

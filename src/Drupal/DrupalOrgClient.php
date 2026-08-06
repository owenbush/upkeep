<?php

declare(strict_types=1);

namespace Upkeep\Drupal;

use Symfony\Contracts\HttpClient\HttpClientInterface;

/**
 * Read-only client for the drupal.org REST API (Drupal 7 Services).
 *
 * The API is public and requires no authentication for reads. Fetched issues
 * are memoized per instance (same model as GitlabClient). Failures are
 * absorbed: the caller gets null and decides how to degrade.
 */
final class DrupalOrgClient
{
    /** @var array<int, ?Issue> */
    private array $cache = [];

    /** @var array<string, ?int> project machine name => node id, misses kept */
    private array $projectNids = [];

    /** @var array<int, array<array-key, mixed>|null> file id => file resource, misses kept */
    private array $fileDetails = [];

    public function __construct(
        private readonly HttpClientInterface $http,
        private readonly string $apiBase = 'https://www.drupal.org/api-d7',
    ) {
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
        $data = $this->getJson(sprintf(
            '%s/node.json?field_project_machine_name=%s',
            $this->apiBase,
            urlencode($machineName),
        ));

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
        try {
            $response = $this->http->request('GET', sprintf('%s/node/%d.json', $this->apiBase, $nid), [
                'headers' => ['Accept' => 'application/json'],
                'timeout' => 10,
            ]);

            if ($response->getStatusCode() !== 200) {
                return null;
            }

            $data = json_decode($response->getContent(false), true, 512, \JSON_THROW_ON_ERROR);
            if (!\is_array($data)) {
                return null;
            }

            return Issue::fromApi($this->withResolvedFiles($data));
        } catch (\Throwable) {
            return null;
        }
    }

    /** @return list<Issue> */
    private function fetchProjectIssues(int $projectNid, IssueStatus $status): array
    {
        $issues = [];
        $page = 0;

        do {
            try {
                $url = sprintf(
                    '%s/node.json?type=project_issue&field_project=%d&field_issue_status=%d&page=%d',
                    $this->apiBase,
                    $projectNid,
                    $status->value,
                    $page,
                );

                $response = $this->http->request('GET', $url, [
                    'headers' => ['Accept' => 'application/json'],
                    'timeout' => 15,
                ]);

                if ($response->getStatusCode() !== 200) {
                    break;
                }

                $data = json_decode($response->getContent(false), true, 512, \JSON_THROW_ON_ERROR);
                if (!\is_array($data)) {
                    break;
                }

                $list = $data['list'] ?? [];
                if (!\is_array($list) || $list === []) {
                    break;
                }

                foreach ($list as $item) {
                    if (!\is_array($item)) {
                        continue;
                    }
                    $issue = Issue::fromApi($this->withResolvedFiles($item));
                    if ($issue !== null) {
                        $issues[] = $issue;
                        $this->cache[$issue->nid] = $issue;
                    }
                }

                $hasMore = isset($data['next']) && $data['next'] !== '';
                ++$page;
            } catch (\Throwable) {
                break;
            }
        } while ($hasMore && $page < 10);

        return $issues;
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

        $detail = $this->fileDetail($fid);

        // A file that cannot be read stays a reference rather than becoming a
        // half-built attachment: IssueFile::fromApi drops it, and an issue
        // reports one attachment fewer instead of one nameless one.
        return $detail === null ? $entry : ['file' => $detail] + $entry;
    }

    /** @return array<array-key, mixed>|null */
    private function fileDetail(int $fid): ?array
    {
        if (!\array_key_exists($fid, $this->fileDetails)) {
            $detail = $this->getJson(sprintf('%s/file/%d.json', $this->apiBase, $fid));
            $this->fileDetails[$fid] = $detail === [] ? null : $detail;
        }

        return $this->fileDetails[$fid];
    }

    /**
     * A GET whose every failure mode — status, transport, unusable body —
     * reads as the empty array. This client absorbs failures by contract: the
     * caller gets less data, never an exception.
     *
     * @return array<array-key, mixed>
     */
    private function getJson(string $url): array
    {
        try {
            $response = $this->http->request('GET', $url, [
                'headers' => ['Accept' => 'application/json'],
                'timeout' => 10,
            ]);

            if ($response->getStatusCode() !== 200) {
                return [];
            }

            $data = json_decode($response->getContent(false), true, 512, \JSON_THROW_ON_ERROR);

            return \is_array($data) ? $data : [];
        } catch (\Throwable) {
            return [];
        }
    }
}

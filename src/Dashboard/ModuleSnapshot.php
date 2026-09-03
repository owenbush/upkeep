<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Drupal\Issue;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Project;

/**
 * Cached remote state for one module: the GitLab project, its open MRs
 * (with detail/pipeline data), and any resolved drupal.org issues.
 *
 * Stores raw API payloads so deserialization goes through the same fromApi()
 * path as a live fetch — no separate serialization contract to maintain.
 */
final readonly class ModuleSnapshot
{
    /**
     * @param array<array-key, mixed>                  $projectData raw GitLab project API payload
     * @param list<array<array-key, mixed>>            $mrData      raw GitLab MR detail API payloads
     * @param array<int, array<array-key, mixed>|null> $issueData   raw drupal.org issue API payloads keyed by nid
     *                                                              (a null entry records a lookup that failed)
     * @param list<array<array-key, mixed>>            $patchIssueData raw drupal.org payloads for the module's
     *                                                                 Needs Review / RTBC issues, with their
     *                                                                 attachments already dereferenced
     */
    public function __construct(
        public \DateTimeImmutable $fetchedAt,
        public array $projectData,
        public array $mrData,
        public array $issueData,
        public array $patchIssueData = [],
        /**
         * Merged merge requests, kept apart from the open ones because they
         * make no row of their own — they answer "has this already landed?"
         * about an issue that is still open, which for a Project Update Bot
         * compatibility issue is the normal state and unknowable otherwise.
         *
         * @var list<array<array-key, mixed>>
         */
        public array $mergedMrData = [],
        /**
         * Source-project id => issue nid, from GitlabClient::issueForkNids().
         * The only thing that pairs a bot merge request to its issue.
         *
         * @var array<int, int>
         */
        public array $forkNids = [],
    ) {
    }

    /**
     * The Needs Review / RTBC issues this module had when the snapshot was
     * taken, for the dashboard's patch rows.
     *
     * Empty for a snapshot written before patch rows existed, which reads as
     * "this module contributed no patch rows" — the pre-existing dashboard,
     * until the next --refresh. Cheaper and less surprising than silently
     * going to the network from a command whose whole promise is that a cached
     * run costs nothing.
     *
     * @return list<Issue>
     */
    public function patchIssues(): array
    {
        $issues = [];
        foreach ($this->patchIssueData as $data) {
            $issue = Issue::fromApi($data);
            if ($issue !== null) {
                $issues[] = $issue;
            }
        }

        return $issues;
    }

    public function project(): Project
    {
        return Project::fromApi($this->projectData);
    }

    /** @return list<MergeRequest> */
    public function mergeRequests(): array
    {
        return array_map(MergeRequest::fromApi(...), $this->mrData);
    }

    /**
     * The merged merge requests, as models.
     *
     * @return list<MergeRequest>
     */
    public function mergedMergeRequests(): array
    {
        return array_map(
            static fn (array $row): MergeRequest => MergeRequest::fromApi($row),
            $this->mergedMrData,
        );
    }

    public function issue(int $nid): ?Issue
    {
        $data = $this->issueData[$nid] ?? null;

        return $data !== null ? Issue::fromApi($data) : null;
    }

    public function toJson(): string
    {
        return json_encode([
            'fetched_at' => $this->fetchedAt->format(\DateTimeInterface::ATOM),
            'project' => $this->projectData,
            'merge_requests' => $this->mrData,
            'issues' => $this->issueData,
            'patch_issues' => $this->patchIssueData,
            'merged_merge_requests' => $this->mergedMrData,
            'fork_nids' => $this->forkNids,
        ], \JSON_THROW_ON_ERROR | \JSON_PRETTY_PRINT);
    }

    public static function fromJson(string $json): ?self
    {
        try {
            $data = json_decode($json, true, 512, \JSON_THROW_ON_ERROR);
        } catch (\JsonException) {
            return null;
        }

        if (!\is_array($data)) {
            return null;
        }

        // A cache file is untrusted input like any other: it is on disk, it
        // outlives the format that wrote it, and a half-shaped one must read
        // as "no cache" (the caller then refetches) rather than reach the
        // models as mixed.
        $fetchedAtRaw = $data['fetched_at'] ?? null;
        $projectData = $data['project'] ?? null;
        $mrData = $data['merge_requests'] ?? null;
        if (!\is_string($fetchedAtRaw) || !\is_array($projectData) || !\is_array($mrData)) {
            return null;
        }

        try {
            $fetchedAt = new \DateTimeImmutable($fetchedAtRaw);
        } catch (\Exception) {
            return null;
        }

        $patchIssues = $data['patch_issues'] ?? null;
        $mergedMrs = $data['merged_merge_requests'] ?? null;

        // Absent in a snapshot written before landings were tracked: an older
        // cache reads as "nothing known to have merged", which is the previous
        // behaviour rather than a wrong claim.
        return new self(
            $fetchedAt,
            $projectData,
            self::payloadList($mrData),
            self::payloadsByNid($data['issues'] ?? null),
            \is_array($patchIssues) ? self::payloadList($patchIssues) : [],
            \is_array($mergedMrs) ? self::payloadList($mergedMrs) : [],
            self::forkMap($data['fork_nids'] ?? null),
        );
    }

    /**
     * @return array<int, int> source-project id => issue nid
     */
    private static function forkMap(mixed $raw): array
    {
        if (!\is_array($raw)) {
            return [];
        }

        $map = [];
        foreach ($raw as $projectId => $nid) {
            if (is_numeric($projectId) && \is_int($nid)) {
                $map[(int) $projectId] = $nid;
            }
        }

        return $map;
    }

    /**
     * @param array<array-key, mixed> $raw
     *
     * @return list<array<array-key, mixed>>
     */
    private static function payloadList(array $raw): array
    {
        $payloads = [];
        foreach ($raw as $entry) {
            if (\is_array($entry)) {
                $payloads[] = $entry;
            }
        }

        return $payloads;
    }

    /**
     * Issue payloads keyed by node id. A null entry is meaningful — it records
     * that the issue was looked up and the lookup failed — so it is kept,
     * while an unusable key or a non-object payload is dropped.
     *
     * @return array<int, array<array-key, mixed>|null>
     */
    private static function payloadsByNid(mixed $raw): array
    {
        if (!\is_array($raw)) {
            return [];
        }

        $payloads = [];
        foreach ($raw as $nid => $entry) {
            if (!\is_int($nid) || ($entry !== null && !\is_array($entry))) {
                continue;
            }
            $payloads[$nid] = $entry;
        }

        return $payloads;
    }

    public function ageLabel(\DateTimeImmutable $now): string
    {
        $seconds = $now->getTimestamp() - $this->fetchedAt->getTimestamp();
        if ($seconds < 60) {
            return 'just now';
        }
        if ($seconds < 3600) {
            return sprintf('%dm ago', intdiv($seconds, 60));
        }
        if ($seconds < 86400) {
            return sprintf('%dh ago', intdiv($seconds, 3600));
        }

        return sprintf('%dd ago', intdiv($seconds, 86400));
    }
}

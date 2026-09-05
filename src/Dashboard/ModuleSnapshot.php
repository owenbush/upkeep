<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Drupal\Issue;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Project;

/**
 * Cached remote state for one module: the GitLab project, its open and merged
 * merge requests, its open drupal.org issues, the fork-to-issue map that pairs
 * the two, and what each branch declares about core.
 *
 * Stores raw API payloads so deserialization goes through the same fromApi()
 * path as a live fetch — no separate serialization contract to maintain.
 *
 * It used to carry a second issue collection as well, keyed by nid, fetched one
 * request at a time for every issue any merge request mentioned. That existed
 * to put a number in the dashboard's ISSUE cell. A row *is* an issue now and
 * takes its issue from the open-issue scan already here, so the second
 * collection — 155 requests per pathauto refresh, plus an attachment lookup
 * per file on each — read by nothing.
 */
final readonly class ModuleSnapshot
{
    /**
     * @param array<array-key, mixed>       $projectData    raw GitLab project API payload
     * @param list<array<array-key, mixed>> $mrData         raw GitLab MR detail API payloads
     * @param list<array<array-key, mixed>> $patchIssueData raw drupal.org payloads for the module's open
     *                                                      issues, with their attachments already
     *                                                      dereferenced
     */
    public function __construct(
        public \DateTimeImmutable $fetchedAt,
        public array $projectData,
        public array $mrData,
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
        /**
         * Branch name => its `core_version_requirement`, read from the
         * branch's info.yml.
         *
         * Which cores a row's evidence is worth gathering on. A *branch*
         * supports several at once, and not always the ones the registry
         * tracks — checking a branch on a core it does not declare produces a
         * failure that says nothing about the module.
         *
         * A branch absent from this map is "cannot tell", never "supports
         * nothing": the file may not exist on that branch (token's
         * 691078-field-tokens has no info.yml at all), the fetch may have
         * failed, or the snapshot may predate this field. Every one of those
         * falls back to the tracked set whole, which is what the dashboard did
         * before any of this.
         *
         * **array-key, not string**, and not by choice: PHP stores a
         * numeric-looking key as an int, so a branch named "11" arrives here
         * as 11 however the caller writes it.
         *
         * @var array<array-key, string>
         */
        public array $coreConstraints = [],
        /**
         * Merge request iid => the SHA of its `/merge` ref.
         *
         * The revision each merge request's local evidence is about. upkeep
         * checks the branch merged into the current tip of its target, and
         * that tree moves when *either* side does — so a result keyed on the
         * head SHA alone would read as current after the target gained a
         * commit. An iid missing here has no merge ref (GitLab could not merge
         * it, normally a conflict) or comes from a snapshot written before
         * this existed; both fall back to the head SHA, which is what the
         * adapter checks out in that case too.
         *
         * @var array<array-key, string>
         */
        public array $mergeRefShas = [],
    ) {
    }

    /**
     * The open issues this module had when the snapshot was taken — the
     * dashboard's rows, and `upkeep issues`' queue.
     *
     * Empty for a snapshot written before they were stored, which reads as
     * "this module contributed no rows" — the pre-existing dashboard, until
     * the next --refresh. Cheaper and less surprising than silently going to
     * the network from a command whose whole promise is that a cached run
     * costs nothing.
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

    public function toJson(): string
    {
        return json_encode([
            'fetched_at' => $this->fetchedAt->format(\DateTimeInterface::ATOM),
            'project' => $this->projectData,
            'merge_requests' => $this->mrData,
            'patch_issues' => $this->patchIssueData,
            'merged_merge_requests' => $this->mergedMrData,
            'fork_nids' => $this->forkNids,
            'core_constraints' => $this->coreConstraints,
            'merge_ref_shas' => $this->mergeRefShas,
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
            \is_array($patchIssues) ? self::payloadList($patchIssues) : [],
            \is_array($mergedMrs) ? self::payloadList($mergedMrs) : [],
            self::forkMap($data['fork_nids'] ?? null),
            self::constraintMap($data['core_constraints'] ?? null),
            self::shaMap($data['merge_ref_shas'] ?? null),
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
     * Merge request iid => merge-ref SHA, from an untrusted cache file.
     *
     * @return array<array-key, string>
     */
    private static function shaMap(mixed $raw): array
    {
        if (!\is_array($raw)) {
            return [];
        }

        $map = [];
        foreach ($raw as $iid => $sha) {
            if (is_numeric($iid) && \is_string($sha) && preg_match('/^[0-9a-f]{7,64}$/', $sha) === 1) {
                $map[(int) $iid] = $sha;
            }
        }

        return $map;
    }

    /**
     * Branch name => core constraint, from an untrusted cache file.
     *
     * @return array<array-key, string>
     */
    private static function constraintMap(mixed $raw): array
    {
        if (!\is_array($raw)) {
            return [];
        }

        $map = [];
        foreach ($raw as $branch => $constraint) {
            if (\is_string($constraint) && $constraint !== '') {
                $map[$branch] = $constraint;
            }
        }

        return $map;
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

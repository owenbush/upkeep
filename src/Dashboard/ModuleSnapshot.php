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
     * @param array              $projectData  raw GitLab project API payload
     * @param list<array>        $mrData       raw GitLab MR detail API payloads
     * @param array<int, ?array> $issueData    raw drupal.org issue API payloads keyed by nid (null = fetch failed)
     */
    public function __construct(
        public \DateTimeImmutable $fetchedAt,
        public array $projectData,
        public array $mrData,
        public array $issueData,
    ) {
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
        ], \JSON_THROW_ON_ERROR | \JSON_PRETTY_PRINT);
    }

    public static function fromJson(string $json): ?self
    {
        try {
            $data = json_decode($json, true, 512, \JSON_THROW_ON_ERROR);
        } catch (\JsonException) {
            return null;
        }

        if (!\is_array($data) || !isset($data['fetched_at'], $data['project'], $data['merge_requests'])) {
            return null;
        }

        try {
            $fetchedAt = new \DateTimeImmutable((string) $data['fetched_at']);
        } catch (\DateMalformedStringException) {
            return null;
        }

        return new self(
            $fetchedAt,
            $data['project'],
            $data['merge_requests'],
            $data['issues'] ?? [],
        );
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

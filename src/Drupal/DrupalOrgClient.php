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
     * @param list<IssueStatus> $statuses
     * @return list<Issue>
     */
    public function projectIssues(string $machineName, array $statuses): array
    {
        $issues = [];
        foreach ($statuses as $status) {
            foreach ($this->fetchProjectIssues($machineName, $status) as $issue) {
                $issues[$issue->nid] = $issue;
            }
        }

        return array_values($issues);
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

            return Issue::fromApi($data);
        } catch (\Throwable) {
            return null;
        }
    }

    /** @return list<Issue> */
    private function fetchProjectIssues(string $machineName, IssueStatus $status): array
    {
        $issues = [];
        $page = 0;

        do {
            try {
                $url = sprintf(
                    '%s/node.json?type=project_issue&field_project_machine_name=%s&field_issue_status=%d&page=%d',
                    $this->apiBase,
                    urlencode($machineName),
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
                    $issue = Issue::fromApi($item);
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
}

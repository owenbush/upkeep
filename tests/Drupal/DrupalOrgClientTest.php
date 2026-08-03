<?php

declare(strict_types=1);

namespace Upkeep\Tests\Drupal;

use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\IssueStatus;

final class DrupalOrgClientTest extends TestCase
{
    /**
     * @param array<array-key, mixed> $overrides fields replacing the defaults below
     *
     * @return array<array-key, mixed> a drupal.org api-d7 issue node payload
     */
    private static function issuePayload(array $overrides = []): array
    {
        return $overrides + [
            'nid' => 3467675,
            'title' => 'Make URL field required by default',
            'url' => 'https://www.drupal.org/project/widget/issues/3467675',
            'field_issue_status' => '8',
            'field_issue_priority' => '200',
            'field_issue_version' => '2.0.x-dev',
            'field_issue_component' => 'Code',
            'field_issue_category' => '1',
            'field_project' => ['machine_name' => 'widget', 'uri' => 'https://www.drupal.org/api-d7/node/12345'],
        ];
    }

    public function testFetchesAndParsesAnIssue(): void
    {
        $client = new DrupalOrgClient(new MockHttpClient(
            new MockResponse(json_encode(self::issuePayload(), \JSON_THROW_ON_ERROR)),
        ));

        $issue = $client->issue(3467675);

        self::assertNotNull($issue);
        self::assertSame(3467675, $issue->nid);
        self::assertSame('Make URL field required by default', $issue->title);
        self::assertSame(IssueStatus::NeedsReview, $issue->status);
        self::assertSame('https://www.drupal.org/project/widget/issues/3467675', $issue->url);
        self::assertSame('widget', $issue->project);
        self::assertSame(200, $issue->priority);
        self::assertSame('Normal', $issue->priorityLabel());
        self::assertSame('2.0.x-dev', $issue->version);
        self::assertSame('Code', $issue->component);
        self::assertSame('Bug report', $issue->category);
    }

    public function testMemoizesPerNid(): void
    {
        $calls = 0;
        $client = new DrupalOrgClient(new MockHttpClient(function () use (&$calls) {
            ++$calls;
            return new MockResponse(json_encode(self::issuePayload(), \JSON_THROW_ON_ERROR));
        }));

        $first = $client->issue(3467675);
        $second = $client->issue(3467675);

        self::assertSame($first, $second);
        self::assertSame(1, $calls, 'second call should return cached result');
    }

    public function testReturnsNullOn404(): void
    {
        $client = new DrupalOrgClient(new MockHttpClient(
            new MockResponse('', ['http_code' => 404]),
        ));

        self::assertNull($client->issue(9999999));
    }

    public function testReturnsNullOnMalformedJson(): void
    {
        $client = new DrupalOrgClient(new MockHttpClient(
            new MockResponse('not json at all'),
        ));

        self::assertNull($client->issue(1));
    }

    public function testReturnsNullOnUnknownStatusId(): void
    {
        $client = new DrupalOrgClient(new MockHttpClient(
            new MockResponse(json_encode(self::issuePayload(['field_issue_status' => '999']), \JSON_THROW_ON_ERROR)),
        ));

        self::assertNull($client->issue(3467675));
    }

    public function testRtbcStatus(): void
    {
        $client = new DrupalOrgClient(new MockHttpClient(
            new MockResponse(json_encode(self::issuePayload(['field_issue_status' => '14']), \JSON_THROW_ON_ERROR)),
        ));

        $issue = $client->issue(3467675);
        self::assertNotNull($issue);
        self::assertSame(IssueStatus::Rtbc, $issue->status);
        self::assertSame('Reviewed & tested by the community', $issue->status->label());
    }

    public function testProjectIssuesReturnsIssuesFromListing(): void
    {
        $issue1 = self::issuePayload(['nid' => 100, 'field_issue_status' => '8']);
        $issue2 = self::issuePayload(['nid' => 200, 'field_issue_status' => '8']);

        $client = new DrupalOrgClient(new MockHttpClient(
            new MockResponse(json_encode(['list' => [$issue1, $issue2]], \JSON_THROW_ON_ERROR)),
        ));

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertCount(2, $issues);
        self::assertSame(100, $issues[0]->nid);
        self::assertSame(200, $issues[1]->nid);
    }

    public function testProjectIssuesMergesMultipleStatuses(): void
    {
        $needsReview = self::issuePayload(['nid' => 100, 'field_issue_status' => '8']);
        $rtbc = self::issuePayload(['nid' => 200, 'field_issue_status' => '14']);

        $calls = 0;
        $factory = function () use ($needsReview, $rtbc, &$calls): MockResponse {
            ++$calls;
            $list = $calls === 1 ? [$needsReview] : [$rtbc];

            return new MockResponse(json_encode(['list' => $list], \JSON_THROW_ON_ERROR));
        };

        $client = new DrupalOrgClient(new MockHttpClient($factory));
        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview, IssueStatus::Rtbc]);

        self::assertCount(2, $issues);
        self::assertSame(2, $calls);
    }

    public function testProjectIssuesReturnsEmptyOnHttpFailure(): void
    {
        $client = new DrupalOrgClient(new MockHttpClient(
            new MockResponse('', ['http_code' => 500]),
        ));

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);
        self::assertSame([], $issues);
    }

    public function testProjectIssuesDeduplicatesByNid(): void
    {
        $issue = self::issuePayload(['nid' => 100, 'field_issue_status' => '8']);

        $client = new DrupalOrgClient(new MockHttpClient(
            new MockResponse(json_encode(['list' => [$issue, $issue]], \JSON_THROW_ON_ERROR)),
        ));

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);
        self::assertCount(1, $issues);
    }

    public function testProjectIssuesCachesIndividualIssues(): void
    {
        $issue = self::issuePayload(['nid' => 100, 'field_issue_status' => '8']);

        $calls = 0;
        $factory = function () use ($issue, &$calls): MockResponse {
            ++$calls;

            return new MockResponse(json_encode(['list' => [$issue]], \JSON_THROW_ON_ERROR));
        };

        $client = new DrupalOrgClient(new MockHttpClient($factory));
        $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        $cached = $client->issue(100);
        self::assertNotNull($cached);
        self::assertSame(100, $cached->nid);
        self::assertSame(1, $calls, 'individual issue() should return the cached entry from projectIssues()');
    }
}

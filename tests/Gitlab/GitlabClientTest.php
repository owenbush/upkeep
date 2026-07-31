<?php

declare(strict_types=1);

namespace Upkeep\Tests\Gitlab;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Gitlab\EndpointClosed;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\NotFound;
use Upkeep\Gitlab\Pipeline;
use Upkeep\Gitlab\PipelineStatus;
use Upkeep\Gitlab\Project;
use Upkeep\Gitlab\RateLimited;
use Upkeep\Gitlab\Tag;
use Upkeep\Gitlab\TransportError;

final class GitlabClientTest extends TestCase
{
    private const TOKEN = 'glpat-secret-token-value-123';

    /** @var list<array{method: string, url: string, options: array}> */
    private array $requests = [];

    /**
     * @param list<MockResponse> $responses
     */
    private function client(array $responses): GitlabClient
    {
        $this->requests = [];
        $queue = $responses;
        $factory = function (string $method, string $url, array $options) use (&$queue): MockResponse {
            $this->requests[] = ['method' => $method, 'url' => $url, 'options' => $options];
            $response = array_shift($queue);
            if ($response === null) {
                $this->fail('Unexpected extra HTTP request: ' . $method . ' ' . $url);
            }

            return $response;
        };

        return new GitlabClient(
            new MockHttpClient($factory),
            self::TOKEN,
            'https://git.drupalcode.org/api/v4',
            'https://git.drupalcode.org',
        );
    }

    private static function json(array $payload, int $status = 200, array $headers = []): MockResponse
    {
        return new MockResponse(json_encode($payload, JSON_THROW_ON_ERROR), [
            'http_code' => $status,
            'response_headers' => ['content-type' => 'application/json'] + $headers,
        ]);
    }

    private static function projectPayload(): array
    {
        return [
            'id' => 181714,
            'path' => 'conditions_helper',
            'path_with_namespace' => 'project/conditions_helper',
            'name' => 'Conditions Helper',
            'web_url' => 'https://git.drupalcode.org/project/conditions_helper',
        ];
    }

    private static function botMrPayload(array $overrides = []): array
    {
        return $overrides + [
            'id' => 999001,
            'iid' => 2,
            'title' => 'Draft: Automated Project Update Bot fixes',
            'state' => 'opened',
            'draft' => true,
            'source_branch' => 'project-update-bot-only',
            'target_branch' => '1.0.x',
            'sha' => 'ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12',
            'detailed_merge_status' => 'draft_status',
            'author' => ['id' => 66574, 'username' => 'Project-Update-Bot', 'name' => 'project update bot'],
            'web_url' => 'https://git.drupalcode.org/project/field_visibility_conditions/-/merge_requests/2',
        ];
    }

    private function projectModel(): Project
    {
        return Project::fromApi(self::projectPayload());
    }

    public function testProjectLookupReturnsTypedProject(): void
    {
        $client = $this->client([self::json(self::projectPayload())]);

        $project = $client->project('conditions_helper');

        $this->assertInstanceOf(Project::class, $project);
        $this->assertSame(181714, $project->id);
        $this->assertSame('project/conditions_helper', $project->pathWithNamespace);
        $this->assertSame('https://git.drupalcode.org/project/conditions_helper', $project->webUrl);

        $this->assertCount(1, $this->requests);
        $this->assertSame('GET', $this->requests[0]['method']);
        $this->assertSame(
            'https://git.drupalcode.org/api/v4/projects/project%2Fconditions_helper',
            $this->requests[0]['url'],
        );
        $this->assertContains('PRIVATE-TOKEN: ' . self::TOKEN, $this->requests[0]['options']['headers']);
    }

    public function testOpenMergeRequestsParsesBotGateFields(): void
    {
        $human = self::botMrPayload([
            'id' => 999002,
            'iid' => 5,
            'title' => 'Fix the widget',
            'draft' => false,
            'source_branch' => '3471234-fix-the-widget',
            'detailed_merge_status' => 'mergeable',
            'sha' => 'ffee0011ffee0011ffee0011ffee0011ffee0011',
            'author' => ['id' => 42, 'username' => 'a-human', 'name' => 'A Human'],
        ]);
        $client = $this->client([self::json([self::botMrPayload(), $human])]);

        $list = $client->openMergeRequests($this->projectModel());

        $this->assertNotInstanceOf(\Upkeep\Gitlab\ApiFailure::class, $list);
        $this->assertCount(2, $list);
        $mrs = $list->all();

        $bot = $mrs[0];
        $this->assertInstanceOf(MergeRequest::class, $bot);
        $this->assertSame(2, $bot->iid);
        $this->assertSame('Draft: Automated Project Update Bot fixes', $bot->title);
        $this->assertSame('Project-Update-Bot', $bot->authorUsername);
        $this->assertSame(66574, $bot->authorId);
        $this->assertSame('project-update-bot-only', $bot->sourceBranch);
        $this->assertTrue($bot->draft);
        $this->assertSame('draft_status', $bot->detailedMergeStatus);
        $this->assertSame('ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12', $bot->headSha);
        $this->assertSame('opened', $bot->state);

        $this->assertSame('a-human', $mrs[1]->authorUsername);
        $this->assertFalse($mrs[1]->draft);

        $this->assertSame(
            'https://git.drupalcode.org/api/v4/projects/181714/merge_requests?state=opened&scope=all&per_page=100',
            $this->requests[0]['url'],
        );
    }

    public function testForbiddenReadReturnsEndpointClosedWithStatusAndBrowserFallback(): void
    {
        $client = $this->client([self::json(['message' => '403 Forbidden'], 403)]);

        $result = $client->openMergeRequests($this->projectModel());

        $this->assertInstanceOf(EndpointClosed::class, $result);
        $this->assertSame(403, $result->status);
        $this->assertSame(
            'https://git.drupalcode.org/project/conditions_helper/-/merge_requests',
            $result->browserUrl,
        );
        $this->assertStringNotContainsString(self::TOKEN, $result->message);
    }

    public function testTooManyRequestsReturnsRateLimitedWithRetryAfter(): void
    {
        $client = $this->client([
            self::json(['message' => '429 Too Many Requests'], 429, ['retry-after' => '30']),
        ]);

        $result = $client->project('conditions_helper');

        $this->assertInstanceOf(RateLimited::class, $result);
        $this->assertSame(30, $result->retryAfterSeconds);
        $this->assertStringContainsString('Rate limited', $result->message);
        $this->assertStringContainsString('retry', strtolower($result->message));
    }

    public function testRateLimitedWithoutRetryAfterHeaderStillTyped(): void
    {
        $client = $this->client([self::json([], 429)]);

        $result = $client->project('conditions_helper');

        $this->assertInstanceOf(RateLimited::class, $result);
        $this->assertNull($result->retryAfterSeconds);
    }

    public function testMissingProjectReturnsNotFoundWithBrowserUrl(): void
    {
        $client = $this->client([self::json(['message' => '404 Project Not Found'], 404)]);

        $result = $client->project('no_such_module');

        $this->assertInstanceOf(NotFound::class, $result);
        $this->assertSame('https://git.drupalcode.org/project/no_such_module', $result->browserUrl);
    }

    public function testNetworkFailureReturnsTransportErrorWithoutToken(): void
    {
        $client = $this->client([new MockResponse('', ['error' => 'DNS resolution failed for git.drupalcode.org'])]);

        $result = $client->project('conditions_helper');

        $this->assertInstanceOf(TransportError::class, $result);
        $this->assertStringContainsString('DNS resolution failed', $result->message);
        $this->assertStringNotContainsString(self::TOKEN, $result->message);
    }

    public function testRepeatGetForSameResourceIsServedFromMemoizationCache(): void
    {
        $client = $this->client([self::json(self::projectPayload())]);

        $first = $client->project('conditions_helper');
        $second = $client->project('conditions_helper');

        $this->assertCount(1, $this->requests, 'a repeated GET for the same URL must not hit the transport again');
        $this->assertInstanceOf(Project::class, $first);
        $this->assertInstanceOf(Project::class, $second);
        $this->assertSame($first->id, $second->id);
    }

    public function testFreshReturnsAClientThatRefetchesInsteadOfServingMemoizedData(): void
    {
        // The freshness re-check before a fast-lane merge must observe the
        // MR as it is NOW, not as it was memoized at row-assembly time.
        $client = $this->client([
            self::json(self::projectPayload()),
            self::json(self::projectPayload()),
        ]);

        $first = $client->project('conditions_helper');
        $second = $client->fresh()->project('conditions_helper');

        $this->assertCount(2, $this->requests, 'fresh() must bypass the original instance\'s GET memoization');
        $this->assertInstanceOf(Project::class, $first);
        $this->assertInstanceOf(Project::class, $second);
        // The original instance's cache is untouched by the fresh copy.
        $client->project('conditions_helper');
        $this->assertCount(2, $this->requests);
    }

    public function testDistinctResourcesAreNotServedFromCache(): void
    {
        $client = $this->client([
            self::json(self::projectPayload()),
            self::json([self::botMrPayload()]),
        ]);

        $project = $client->project('conditions_helper');
        $this->assertInstanceOf(Project::class, $project);
        $client->openMergeRequests($project);

        $this->assertCount(2, $this->requests);
    }

    public function testMalformedJsonBodyReturnsTransportError(): void
    {
        $client = $this->client([new MockResponse('<html>gateway timeout</html>', ['http_code' => 200])]);

        $result = $client->project('conditions_helper');

        $this->assertInstanceOf(TransportError::class, $result);
        $this->assertStringNotContainsString(self::TOKEN, $result->message);
    }

    public function testSingleMergeRequestCarriesHeadPipeline(): void
    {
        $payload = self::botMrPayload([
            'head_pipeline' => [
                'id' => 555777,
                'status' => 'success',
                'sha' => 'ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12',
                'web_url' => 'https://git.drupalcode.org/project/conditions_helper/-/pipelines/555777',
            ],
        ]);
        $client = $this->client([self::json($payload)]);

        $mr = $client->mergeRequest($this->projectModel(), 2);

        $this->assertInstanceOf(MergeRequest::class, $mr);
        $this->assertInstanceOf(Pipeline::class, $mr->headPipeline);
        $this->assertSame(555777, $mr->headPipeline->id);
        $this->assertSame(PipelineStatus::Success, $mr->headPipeline->status);
        $this->assertSame(
            'https://git.drupalcode.org/api/v4/projects/181714/merge_requests/2',
            $this->requests[0]['url'],
        );
    }

    public function testHeadPipelineIsNullWhenMergeRequestHasNone(): void
    {
        $client = $this->client([self::json(self::botMrPayload(['head_pipeline' => null]))]);

        $result = $client->headPipeline($this->projectModel(), 2);

        $this->assertNull($result);
    }

    public function testHeadPipelineIsExtractedFromMergedRequestFetch(): void
    {
        $client = $this->client([
            self::json(self::botMrPayload([
                'head_pipeline' => ['id' => 1, 'status' => 'failed', 'sha' => 'abc', 'web_url' => 'https://example.org/p/1'],
            ])),
        ]);

        $pipeline = $client->headPipeline($this->projectModel(), 2);

        $this->assertInstanceOf(Pipeline::class, $pipeline);
        $this->assertSame(PipelineStatus::Failed, $pipeline->status);
    }

    private static function tagsPayload(): array
    {
        return [
            [
                'name' => '1.0.1',
                'target' => 'aaa111',
                'commit' => ['id' => 'aaa111', 'created_at' => '2026-05-01T10:00:00.000+00:00'],
            ],
            [
                'name' => '1.0.0',
                'target' => 'bbb222',
                'commit' => ['id' => 'bbb222', 'created_at' => '2026-01-15T09:30:00.000+00:00'],
            ],
        ];
    }

    public function testTagsParsesNameShaAndDate(): void
    {
        $client = $this->client([self::json(self::tagsPayload())]);

        $tags = $client->tags($this->projectModel());

        $this->assertIsArray($tags);
        $this->assertCount(2, $tags);
        $this->assertContainsOnlyInstancesOf(Tag::class, $tags);
        $this->assertSame('1.0.1', $tags[0]->name);
        $this->assertSame('aaa111', $tags[0]->commitSha);
        $this->assertSame('2026-05-01', $tags[0]->createdAt?->format('Y-m-d'));
        $this->assertSame(
            'https://git.drupalcode.org/api/v4/projects/181714/repository/tags',
            $this->requests[0]['url'],
        );
    }

    public function testMergedSinceDateQueriesMergedMrsWithUpdatedAfter(): void
    {
        // Pins the documented limitation (see GitlabClient::mergedSince):
        // GitLab cannot filter on merge date, so the boundary is
        // `updated_after` — a superset keyed on update time. If this query
        // shape ever changes, the release-notes semantics changed with it.
        $client = $this->client([self::json([self::botMrPayload(['state' => 'merged'])])]);

        $list = $client->mergedSince($this->projectModel(), new \DateTimeImmutable('2026-05-01T10:00:00+00:00'));

        $this->assertInstanceOf(\Upkeep\Gitlab\MergeRequestList::class, $list);
        $this->assertSame('merged', $list->first()?->state);
        $this->assertSame(
            'https://git.drupalcode.org/api/v4/projects/181714/merge_requests?state=merged&scope=all&per_page=100&updated_after=2026-05-01T10%3A00%3A00%2B00%3A00',
            $this->requests[0]['url'],
        );
    }

    public function testMergedSinceTagResolvesTagDateFirst(): void
    {
        $client = $this->client([
            self::json(self::tagsPayload()),
            self::json([self::botMrPayload(['state' => 'merged'])]),
        ]);

        $list = $client->mergedSinceTag($this->projectModel(), '1.0.1');

        $this->assertInstanceOf(\Upkeep\Gitlab\MergeRequestList::class, $list);
        $this->assertCount(2, $this->requests);
        $this->assertStringContainsString('updated_after=2026-05-01T10%3A00%3A00', $this->requests[1]['url']);
    }

    public function testMergedSinceUnknownTagIsTypedNotFound(): void
    {
        $client = $this->client([self::json(self::tagsPayload())]);

        $result = $client->mergedSinceTag($this->projectModel(), '9.9.9');

        $this->assertInstanceOf(NotFound::class, $result);
        $this->assertSame('https://git.drupalcode.org/project/conditions_helper/-/tags', $result->browserUrl);
    }

    public function testMergeSendsSinglePutWithShaGuard(): void
    {
        $client = $this->client([
            self::json(self::botMrPayload([
                'state' => 'merged',
                'title' => 'Automated Project Update Bot fixes',
                'draft' => false,
            ])),
        ]);

        $result = $client->merge(
            $this->projectModel(),
            2,
            expectedHeadSha: 'ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12',
        );

        $this->assertInstanceOf(MergeRequest::class, $result);
        $this->assertSame('merged', $result->state);

        $this->assertCount(1, $this->requests, 'merge must be exactly one request — no batching affordance');
        $this->assertSame('PUT', $this->requests[0]['method']);
        $this->assertSame(
            'https://git.drupalcode.org/api/v4/projects/181714/merge_requests/2/merge',
            $this->requests[0]['url'],
        );
        $this->assertSame(
            ['sha' => 'ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12'],
            json_decode($this->requests[0]['options']['body'], true),
        );
        $this->assertContains('PRIVATE-TOKEN: ' . self::TOKEN, $this->requests[0]['options']['headers']);
    }

    public function testMergeWithoutShaGuardSendsEmptyJsonObject(): void
    {
        $client = $this->client([self::json(self::botMrPayload(['state' => 'merged']))]);

        $result = $client->merge($this->projectModel(), 2);

        $this->assertInstanceOf(MergeRequest::class, $result);
        $body = $this->requests[0]['options']['body'] ?? '';
        $this->assertSame([], (array) json_decode(\is_string($body) ? $body : '', true));
    }

    public function testMergeForbiddenIsEndpointClosedWithMergeRequestBrowserUrl(): void
    {
        $client = $this->client([self::json(['message' => '403 Forbidden'], 403)]);

        $result = $client->merge($this->projectModel(), 2);

        $this->assertInstanceOf(EndpointClosed::class, $result);
        $this->assertSame(403, $result->status);
        $this->assertSame(
            'https://git.drupalcode.org/project/conditions_helper/-/merge_requests/2',
            $result->browserUrl,
        );
    }

    public function testMergeRejectionIsTypedTransportErrorWithStatus(): void
    {
        $client = $this->client([
            self::json(['message' => '405 Method Not Allowed: MR is a draft'], 405),
        ]);

        $result = $client->merge($this->projectModel(), 2);

        $this->assertInstanceOf(TransportError::class, $result);
        $this->assertSame(405, $result->status);
        $this->assertStringContainsString('HTTP 405', $result->message);
        $this->assertStringNotContainsString(self::TOKEN, $result->message);
    }

    #[DataProvider('pipelineStatusProvider')]
    public function testPipelineStatusMapping(string $apiStatus, PipelineStatus $expected): void
    {
        $this->assertSame($expected, PipelineStatus::fromApi($apiStatus));
    }

    public static function pipelineStatusProvider(): array
    {
        return [
            ['success', PipelineStatus::Success],
            ['failed', PipelineStatus::Failed],
            ['running', PipelineStatus::Running],
            ['pending', PipelineStatus::Pending],
            ['created', PipelineStatus::Created],
            ['canceled', PipelineStatus::Canceled],
            ['skipped', PipelineStatus::Skipped],
            ['manual', PipelineStatus::Manual],
            ['scheduled', PipelineStatus::Scheduled],
            ['waiting_for_resource', PipelineStatus::WaitingForResource],
            ['preparing', PipelineStatus::Preparing],
            ['some-future-status', PipelineStatus::Unknown],
        ];
    }
}

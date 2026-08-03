<?php

declare(strict_types=1);

namespace Upkeep\Tests\Workflow;

use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Cockpit\Module;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Workflow\MrContextResolver;
use Upkeep\Workflow\WorkflowException;

final class MrContextResolverTest extends TestCase
{
    /**
     * @param list<MockResponse> $responses
     */
    private static function resolver(array $responses = []): MrContextResolver
    {
        $client = new GitlabClient(
            new MockHttpClient($responses),
            'glpat-test-token',
            'https://git.drupalcode.org/api/v4',
            'https://git.drupalcode.org',
        );

        return new MrContextResolver([
            'conditions_helper' => new Module('conditions_helper', 'project/conditions_helper', ['10', '11']),
            'field_visibility_conditions' => new Module(
                'field_visibility_conditions',
                'project/field_visibility_conditions',
                ['11'],
            ),
        ], $client);
    }

    private static function json(array $payload, int $status = 200): MockResponse
    {
        return new MockResponse(json_encode($payload, JSON_THROW_ON_ERROR), [
            'http_code' => $status,
            'response_headers' => ['content-type' => 'application/json'],
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

    private static function mrPayload(array $overrides = []): array
    {
        return $overrides + [
            'id' => 999001,
            'iid' => 2,
            'title' => 'Fix the thing',
            'state' => 'opened',
            'draft' => false,
            'author' => ['username' => 'maintainer', 'id' => 42],
            'source_branch' => 'fix-thing',
            'target_branch' => '1.0.x',
            'detailed_merge_status' => 'mergeable',
            'sha' => 'abc123def456',
            'web_url' => 'https://git.drupalcode.org/project/conditions_helper/-/merge_requests/2',
        ];
    }

    public function testUnknownModuleIsRejectedNamingKnownModules(): void
    {
        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessageMatches('/not registered.*conditions_helper.*field_visibility_conditions/s');

        self::resolver()->resolve('nope_module', 1, null);
    }

    public function testOmittedCoreVersionDefaultsToFirstListedInRegistry(): void
    {
        $context = self::resolver([
            self::json(self::projectPayload()),
            self::json(self::mrPayload()),
        ])->resolve('conditions_helper', 2, null);

        self::assertSame('10', $context->coreMajor);
        self::assertSame('conditions_helper', $context->module->name);
        self::assertSame(2, $context->mergeRequest->iid);
        self::assertSame('abc123def456', $context->mergeRequest->headSha);
        self::assertSame(181714, $context->project->id);
    }

    public function testExplicitTrackedCoreVersionIsUsed(): void
    {
        $context = self::resolver([
            self::json(self::projectPayload()),
            self::json(self::mrPayload()),
        ])->resolve('conditions_helper', 2, '11');

        self::assertSame('11', $context->coreMajor);
    }

    public function testUntrackedCoreVersionIsRejectedListingTrackedOnes(): void
    {
        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessageMatches('/core version "9".*tracks.*10.*11/s');

        // Fails before any HTTP request: no responses queued.
        self::resolver()->resolve('conditions_helper', 2, '9');
    }

    public function testMissingMergeRequestIsRejectedWithBrowserFallback(): void
    {
        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessageMatches('/MR !77.*conditions_helper.*not found/is');

        self::resolver([
            self::json(self::projectPayload()),
            self::json(['message' => '404 Not Found'], 404),
        ])->resolve('conditions_helper', 77, null);
    }

    public function testNonOpenMergeRequestIsRejected(): void
    {
        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessageMatches('/MR !2.*merged.*open/is');

        self::resolver([
            self::json(self::projectPayload()),
            self::json(self::mrPayload(['state' => 'merged'])),
        ])->resolve('conditions_helper', 2, null);
    }

    public function testProjectLookupFailureIsSurfacedAsWorkflowError(): void
    {
        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessageMatches('/conditions_helper.*HTTP 404/is');

        self::resolver([
            self::json(['message' => '404 Project Not Found'], 404),
        ])->resolve('conditions_helper', 2, null);
    }
}

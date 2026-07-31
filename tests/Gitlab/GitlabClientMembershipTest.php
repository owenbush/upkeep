<?php

declare(strict_types=1);

namespace Upkeep\Tests\Gitlab;

use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Gitlab\EndpointClosed;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\Project;

final class GitlabClientMembershipTest extends TestCase
{
    public function testMembershipProjectsPaginatesUntilAnEmptyPage(): void
    {
        $pages = [
            new MockResponse(json_encode([
                ['id' => 1, 'path' => 'conditions_helper', 'path_with_namespace' => 'project/conditions_helper', 'name' => 'Conditions Helper', 'web_url' => 'https://git.drupalcode.org/project/conditions_helper'],
                ['id' => 2, 'path' => 'sandbox_thing', 'path_with_namespace' => 'sandbox/sandbox_thing', 'name' => 'Sandbox', 'web_url' => 'https://git.drupalcode.org/sandbox/sandbox_thing'],
            ])),
            new MockResponse(json_encode([
                ['id' => 3, 'path' => 'token_or', 'path_with_namespace' => 'project/token_or', 'name' => 'Token OR', 'web_url' => 'https://git.drupalcode.org/project/token_or'],
            ])),
            new MockResponse(json_encode([])),
        ];
        $requested = [];
        $http = new MockHttpClient(function (string $method, string $url) use (&$pages, &$requested) {
            $requested[] = $url;
            return array_shift($pages);
        });

        $client = new GitlabClient($http, 'token');
        $projects = $client->membershipProjects();

        self::assertIsArray($projects);
        self::assertCount(3, $projects);
        self::assertContainsOnlyInstancesOf(Project::class, $projects);
        self::assertSame(['project/conditions_helper', 'sandbox/sandbox_thing', 'project/token_or'], array_map(
            static fn (Project $p): string => $p->pathWithNamespace,
            $projects,
        ));
        self::assertCount(3, $requested);
        self::assertStringContainsString('membership=true', $requested[0]);
        self::assertStringContainsString('page=1', $requested[0]);
        self::assertStringContainsString('page=3', $requested[2]);
    }

    public function testMembershipProjectsForbiddenIsTypedEndpointClosed(): void
    {
        $client = new GitlabClient(new MockHttpClient(new MockResponse('', ['http_code' => 403])), 'token');
        $result = $client->membershipProjects();

        self::assertInstanceOf(EndpointClosed::class, $result);
        self::assertSame(403, $result->status);
    }
}

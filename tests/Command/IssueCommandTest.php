<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Command\IssueCommand;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Gitlab\GitlabClient;

final class IssueCommandTest extends TestCase
{
    private string $cockpit;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-issue-cmd-test-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit, 0o755, true);
        file_put_contents($this->cockpit . '/registry.yml', <<<YAML
        modules:
          widget:
            project: project/widget
            core_versions: ["11"]
        YAML);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->cockpit));
    }

    private static function json(array $payload, int $status = 200): MockResponse
    {
        return new MockResponse(json_encode($payload, \JSON_THROW_ON_ERROR), [
            'http_code' => $status,
            'response_headers' => ['content-type' => 'application/json'],
        ]);
    }

    private function gitlabClient(array $mrOverrides = []): GitlabClient
    {
        $project = [
            'id' => 42,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'name' => 'Widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ];

        $mr = $mrOverrides + [
            'iid' => 7,
            'title' => 'Issue #3467675: Make URL field required',
            'state' => 'opened',
            'draft' => false,
            'author' => ['username' => 'alice', 'id' => 100],
            'source_branch' => '3467675-make-url-required',
            'target_branch' => '2.0.x',
            'sha' => 'abc123',
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/7',
        ];

        $factory = static function (string $method, string $url) use ($project, $mr): MockResponse {
            if (str_contains($url, '/merge_requests/7')) {
                return self::json($mr);
            }
            if (str_contains($url, '/projects/')) {
                return self::json($project);
            }
            throw new \LogicException('Unrouted: ' . $url);
        };

        return new GitlabClient(new MockHttpClient($factory), 'test-token');
    }

    private function drupalClient(array $issueOverrides = []): DrupalOrgClient
    {
        $issue = $issueOverrides + [
            'nid' => 3467675,
            'title' => 'Make URL field required by default',
            'url' => 'https://www.drupal.org/project/widget/issues/3467675',
            'field_issue_status' => '8',
            'field_issue_priority' => '200',
            'field_issue_version' => '2.0.x-dev',
            'field_issue_component' => 'Code',
            'field_issue_category' => '1',
            'field_project' => ['machine_name' => 'widget'],
        ];

        return new DrupalOrgClient(new MockHttpClient(new MockResponse(json_encode($issue))));
    }

    public function testShowsIssueDetailsFromMrTitle(): void
    {
        $tester = new CommandTester(new IssueCommand($this->gitlabClient(), $this->drupalClient()));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('Issue #3467675', $display);
        self::assertStringContainsString('Make URL field required', $display);
        self::assertStringContainsString('Needs review', $display);
        self::assertStringContainsString('drupal.org', $display);
    }

    public function testExtractsIssueFromBranchWhenTitleHasNoReference(): void
    {
        $gitlab = $this->gitlabClient([
            'title' => 'Some fix without issue reference',
            'source_branch' => '3467675-some-fix',
        ]);

        $tester = new CommandTester(new IssueCommand($gitlab, $this->drupalClient()));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('Issue #3467675', $tester->getDisplay());
    }

    public function testFailsWhenNoIssueReferenceFound(): void
    {
        $gitlab = $this->gitlabClient([
            'title' => 'Bot update',
            'source_branch' => 'project-update-bot-only',
        ]);

        $tester = new CommandTester(new IssueCommand($gitlab, $this->drupalClient()));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(1, $exit);
        self::assertStringContainsString('No issue number found', $tester->getDisplay());
    }

    public function testShowsUrlEvenWhenDrupalOrgApiFails(): void
    {
        $drupal = new DrupalOrgClient(new MockHttpClient(
            new MockResponse('', ['http_code' => 500]),
        ));

        $tester = new CommandTester(new IssueCommand($this->gitlabClient(), $drupal));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('Issue #3467675', $display);
        self::assertStringContainsString('drupal.org/node/3467675', $display);
    }

    public function testFailsForUnregisteredModule(): void
    {
        $tester = new CommandTester(new IssueCommand($this->gitlabClient(), $this->drupalClient()));
        $exit = $tester->execute([
            'module' => 'nope',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(1, $exit);
        self::assertStringContainsString('not registered', $tester->getDisplay());
    }

    public function testShowsRtbcStatus(): void
    {
        $tester = new CommandTester(new IssueCommand(
            $this->gitlabClient(),
            $this->drupalClient(['field_issue_status' => '14']),
        ));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('Reviewed & tested by the community', $tester->getDisplay());
    }
}

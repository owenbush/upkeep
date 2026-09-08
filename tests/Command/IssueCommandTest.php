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
use Upkeep\Workflow\ExitCode;

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

    /** @param array<array-key, mixed> $payload single object payload or a list of them */
    private static function json(array $payload, int $status = 200): MockResponse
    {
        return new MockResponse(json_encode($payload, \JSON_THROW_ON_ERROR), [
            'http_code' => $status,
            'response_headers' => ['content-type' => 'application/json'],
        ]);
    }

    /** @param array<string, mixed> $mrOverrides */
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

    /** @param array<string, mixed> $issueOverrides */
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

        return new DrupalOrgClient(
            new MockHttpClient(new MockResponse(json_encode($issue, \JSON_THROW_ON_ERROR))),
        );
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

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
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

    /**
     * @return iterable<string, array{array<string, int>, string}>
     */
    public static function fatalLookupFailures(): iterable
    {
        yield 'the project cannot be resolved' => [['/projects/' => 404], 'Could not resolve project'];
        yield 'the merge request cannot be fetched' => [['/merge_requests/7' => 403], 'Could not fetch MR !7'];
    }

    /**
     * Both lookups are prerequisites for finding an issue at all, so either
     * failing means there is no issue to show or open: exit 2 naming which
     * lookup failed and its status, rather than a browser sent nowhere.
     *
     * @param array<string, int> $failing URL substring => HTTP status to answer with
     */
    #[\PHPUnit\Framework\Attributes\DataProvider('fatalLookupFailures')]
    public function testAFailedLookupIsAnInfrastructureFailureNamingIt(array $failing, string $expected): void
    {
        $factory = static function (string $method, string $url) use ($failing): MockResponse {
            foreach ($failing as $needle => $status) {
                if (str_contains($url, $needle)) {
                    return self::json(['message' => 'refused by the test'], $status);
                }
            }
            if (str_contains($url, '/merge_requests/7')) {
                return self::json(['iid' => 7, 'title' => 'Issue #3467675: x', 'source_branch' => 'x']);
            }

            return self::json(['id' => 42, 'path_with_namespace' => 'project/widget']);
        };

        $tester = new CommandTester(new IssueCommand(
            new GitlabClient(new MockHttpClient($factory), 'test-token'),
            $this->drupalClient(),
        ));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit, $tester->getDisplay());
        self::assertStringContainsString($expected, $tester->getDisplay());
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

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        // The registry is a watchlist now, so being absent from it is not the
        // refusal — having nothing built to run against is. See
        // docs/any-module.md.
        self::assertStringContainsString('no base artifacts', $tester->getDisplay());
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
    /**
     * BP-CMD-11: `upkeep issue widget abc` used to become a silent request
     * for MR !0, because this command had no IID validation at all. There is
     * one rule now, on the shared base, and it rejects 0 as well as garbage.
     */
    #[\PHPUnit\Framework\Attributes\DataProvider('invalidIids')]
    public function testANonPositiveIntegerMrArgumentIsRejectedBeforeAnyApiCall(string $iid): void
    {
        $tester = new CommandTester(new IssueCommand($this->gitlabClient(), $this->drupalClient()));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => $iid,
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('positive integer', $tester->getDisplay());
    }

    /** @return iterable<string, array{string}> */
    public static function invalidIids(): iterable
    {
        yield 'letters' => ['abc'];
        yield 'zero' => ['0'];
        yield 'negative' => ['-3'];
        yield 'decimal' => ['1.5'];
        yield 'empty' => [''];
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Command\NeedsWorkCommand;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Workflow\ExitCode;

final class NeedsWorkCommandTest extends TestCase
{
    private string $cockpit;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-needs-work-test-' . bin2hex(random_bytes(4));
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

    private static function projectPayload(): array
    {
        return [
            'id' => 42,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'name' => 'Widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ];
    }

    private static function mrPayload(array $overrides = []): array
    {
        return $overrides + [
            'iid' => 7,
            'title' => 'Issue #3467675: Make URL field required',
            'state' => 'opened',
            'draft' => false,
            'author' => ['username' => 'alice', 'id' => 100],
            'source_branch' => '3467675-make-url-required',
            'target_branch' => '2.0.x',
            'sha' => 'abc12345deadbeef',
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/7',
        ];
    }

    private function gitlabClient(array $mrOverrides = [], ?callable $noteHandler = null): GitlabClient
    {
        $project = self::projectPayload();
        $mr = self::mrPayload($mrOverrides);

        $factory = static function (string $method, string $url) use ($project, $mr, $noteHandler): MockResponse {
            if ($method === 'POST' && str_contains($url, '/notes')) {
                if ($noteHandler !== null) {
                    return $noteHandler($method, $url);
                }

                return self::json(['id' => 1, 'body' => 'ok'], 201);
            }
            if (str_contains($url, '/merge_requests/7')) {
                return self::json($mr);
            }
            if (str_contains($url, '/projects/')) {
                return self::json($project);
            }
            throw new \LogicException('Unrouted: ' . $method . ' ' . $url);
        };

        return new GitlabClient(new MockHttpClient($factory), 'test-token');
    }

    private function populateResults(
        string $sha,
        bool $withFailure = true,
        ?\DateTimeImmutable $recordedAt = null,
    ): void {
        $dir = $this->cockpit . '/results/widget/7/11';
        mkdir($dir, 0o755, true);

        $results = [
            [
                'type' => 'phpunit',
                'status' => 'passed',
                'exit_code' => 0,
                'output' => 'OK (42 tests)',
                'duration_seconds' => 12.3,
            ],
            [
                'type' => 'phpstan',
                'status' => 'passed',
                'exit_code' => 0,
                'output' => 'No errors',
                'duration_seconds' => 3.1,
            ],
        ];

        if ($withFailure) {
            $results[] = [
                'type' => 'phpcs',
                'status' => 'failed',
                'exit_code' => 2,
                'output' => "FILE: src/Plugin/Widget.php\nFOUND 3 ERRORS\n\nLine 14: Missing doc comment",
                'duration_seconds' => 1.2,
            ];
        }

        $payload = [
            'sha' => $sha,
            'recorded_at' => ($recordedAt ?? new \DateTimeImmutable('2024-06-15 09:23:00'))
                ->format(\DateTimeInterface::ATOM),
            'results' => $results,
        ];

        file_put_contents($dir . '/' . $sha . '.json', json_encode($payload, \JSON_PRETTY_PRINT));
    }

    public function testPostsCommentAndReportsSuccess(): void
    {
        $this->populateResults('abc12345deadbeef');

        $posted = false;
        $gitlab = $this->gitlabClient([], function () use (&$posted): MockResponse {
            $posted = true;

            return self::json(['id' => 1], 201);
        });

        $tester = new CommandTester(new NeedsWorkCommand($gitlab));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertTrue($posted, 'Note should have been posted');
        $display = $tester->getDisplay();
        self::assertStringContainsString('Comment posted on !7', $display);
        self::assertStringContainsString('merge_requests/7', $display);
    }

    public function testDryRunPrintsCommentWithoutPosting(): void
    {
        $this->populateResults('abc12345deadbeef');

        $posted = false;
        $gitlab = $this->gitlabClient([], function () use (&$posted): MockResponse {
            $posted = true;

            return self::json(['id' => 1], 201);
        });

        $tester = new CommandTester(new NeedsWorkCommand($gitlab));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--dry-run' => true,
        ]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertFalse($posted, 'Note should not have been posted in dry-run mode');
        $display = $tester->getDisplay();
        self::assertStringContainsString('upkeep local check results', $display);
        self::assertStringContainsString('phpcs', $display);
        self::assertStringContainsString('**FAIL**', $display);
        self::assertStringContainsString('phpunit', $display);
        self::assertStringContainsString('pass', $display);
    }

    public function testFailsWhenNoCachedResults(): void
    {
        $tester = new CommandTester(new NeedsWorkCommand($this->gitlabClient()));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        $display = $tester->getDisplay();
        self::assertStringContainsString('No cached check results', $display);
        self::assertStringContainsString('upkeep check', $display);
    }

    public function testWarnsOnStaleResultsButStillPosts(): void
    {
        $this->populateResults('old_sha_value_1234');

        $posted = false;
        $gitlab = $this->gitlabClient([], function () use (&$posted): MockResponse {
            $posted = true;

            return self::json(['id' => 1], 201);
        });

        $tester = new CommandTester(new NeedsWorkCommand($gitlab));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertTrue($posted, 'Note should still be posted for stale results');
        $display = $tester->getDisplay();
        self::assertStringContainsString('old_sha_', $display);
        self::assertStringContainsString('abc12345', $display);
    }

    public function testFailsWhenGitlabRejectsNote(): void
    {
        $this->populateResults('abc12345deadbeef');

        $gitlab = $this->gitlabClient([], fn (): MockResponse => self::json(['error' => 'forbidden'], 403));

        $tester = new CommandTester(new NeedsWorkCommand($gitlab));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('Could not post comment', $tester->getDisplay());
    }

    public function testCommentIncludesFailureOutput(): void
    {
        $this->populateResults('abc12345deadbeef');

        $tester = new CommandTester(new NeedsWorkCommand($this->gitlabClient()));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--dry-run' => true,
        ]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('<details><summary>phpcs output</summary>', $display);
        self::assertStringContainsString('FOUND 3 ERRORS', $display);
    }

    public function testCommentOmitsDetailsWhenAllPass(): void
    {
        $this->populateResults('abc12345deadbeef', withFailure: false);

        $tester = new CommandTester(new NeedsWorkCommand($this->gitlabClient()));
        $exit = $tester->execute([
            'module' => 'widget',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--dry-run' => true,
        ]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringNotContainsString('<details>', $display);
        self::assertStringContainsString('pass', $display);
    }

    public function testFailsForUnregisteredModule(): void
    {
        $tester = new CommandTester(new NeedsWorkCommand($this->gitlabClient()));
        $exit = $tester->execute([
            'module' => 'nope',
            'mr' => '7',
            '--cockpit' => $this->cockpit,
            '--no-open' => true,
        ]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('not registered', $tester->getDisplay());
    }
}

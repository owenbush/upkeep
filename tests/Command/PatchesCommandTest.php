<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Command\PatchesCommand;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Workflow\ExitCode;

final class PatchesCommandTest extends TestCase
{
    private string $cockpit;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-patches-cmd-test-' . bin2hex(random_bytes(4));
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
    private function gitlabClientWithMrs(array $mrOverrides = []): GitlabClient
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
            if (str_contains($url, '/merge_requests?')) {
                return self::json([$mr]);
            }
            if (str_contains($url, '/projects/')) {
                return self::json($project);
            }
            throw new \LogicException('Unrouted: ' . $url);
        };

        return new GitlabClient(new MockHttpClient($factory), 'test-token');
    }

    /** @param list<array<string, mixed>> $issues */
    private function drupalClientWithIssues(array $issues): DrupalOrgClient
    {
        $factory = static function (string $method, string $url) use ($issues): MockResponse {
            if (str_contains($url, '/node.json')) {
                return self::json(['list' => $issues]);
            }
            throw new \LogicException('Unrouted: ' . $url);
        };

        return new DrupalOrgClient(new MockHttpClient($factory));
    }

    /**
     * @param array<string, mixed> $overrides
     *
     * @return array<string, mixed>
     */
    private static function issuePayload(array $overrides = []): array
    {
        return $overrides + [
            'nid' => 3489012,
            'title' => 'Add config schema for settings form',
            'url' => 'https://www.drupal.org/project/widget/issues/3489012',
            'field_issue_status' => '8',
            'field_issue_priority' => '200',
            'field_issue_version' => '2.0.x-dev',
            'field_issue_component' => 'Code',
            'field_issue_category' => '2',
            'field_project' => ['machine_name' => 'widget'],
            'field_issue_files' => [
                [
                    'file' => [
                        'filename' => '3489012-5-config-schema.patch',
                        'url' => 'https://www.drupal.org/files/issues/3489012-5-config-schema.patch',
                        'filesize' => '2048',
                        'timestamp' => '1705313456',
                    ],
                ],
                [
                    'file' => [
                        'filename' => '3489012-12-config-schema.patch',
                        'url' => 'https://www.drupal.org/files/issues/3489012-12-config-schema.patch',
                        'filesize' => '3072',
                        'timestamp' => '1705400000',
                    ],
                ],
            ],
        ];
    }

    public function testShowsOrphanIssuesWithPatches(): void
    {
        $drupal = $this->drupalClientWithIssues([self::issuePayload()]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('#3489012', $display);
        self::assertStringContainsString('Add config schema', $display);
        self::assertStringContainsString('2', $display);
        self::assertStringContainsString('3489012-12-config-schema.patch', $display);
    }

    public function testFiltersOutIssuesWithCorrespondingMr(): void
    {
        $issueWithMr = self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']);
        $issueWithout = self::issuePayload(['nid' => 3489012]);

        $drupal = $this->drupalClientWithIssues([$issueWithMr, $issueWithout]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringNotContainsString('#3467675', $display);
        self::assertStringContainsString('#3489012', $display);
    }

    public function testShowsSuccessWhenAllIssuesHaveMrs(): void
    {
        $issueWithMr = self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']);
        $drupal = $this->drupalClientWithIssues([$issueWithMr]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('No orphan issues found', $tester->getDisplay());
    }

    public function testShowsSuccessWhenNoIssuesExist(): void
    {
        $drupal = $this->drupalClientWithIssues([]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('No orphan issues found', $tester->getDisplay());
    }

    public function testShowsDashWhenNoPatchFilesAttached(): void
    {
        $noPatch = self::issuePayload(['field_issue_files' => []]);
        $drupal = $this->drupalClientWithIssues([$noPatch]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('#3489012', $display);
    }

    public function testModuleFilterShowsOnlyRequestedModule(): void
    {
        file_put_contents($this->cockpit . '/registry.yml', <<<YAML
        modules:
          widget:
            project: project/widget
            core_versions: ["11"]
          gadget:
            project: project/gadget
            core_versions: ["11"]
        YAML);

        $drupal = $this->drupalClientWithIssues([self::issuePayload()]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit, '--module' => 'widget']);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('#3489012', $tester->getDisplay());
    }

    public function testFailsForUnregisteredModuleFilter(): void
    {
        $drupal = $this->drupalClientWithIssues([]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit, '--module' => 'nope']);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('not registered', $tester->getDisplay());
    }

    public function testUsesSnapshotCacheForMrCrossReference(): void
    {
        $cacheDir = $this->cockpit . '/cache/dashboard';
        mkdir($cacheDir, 0o755, true);

        $snapshot = [
            'fetched_at' => '2026-07-30T10:00:00+00:00',
            'project' => [
                'id' => 42,
                'path' => 'widget',
                'path_with_namespace' => 'project/widget',
                'name' => 'Widget',
                'web_url' => 'https://git.drupalcode.org/project/widget',
            ],
            'merge_requests' => [
                [
                    'iid' => 7,
                    'title' => 'Issue #3467675: Make URL field required',
                    'state' => 'opened',
                    'draft' => false,
                    'author' => ['username' => 'alice', 'id' => 100],
                    'source_branch' => '3467675-make-url-required',
                    'target_branch' => '2.0.x',
                    'sha' => 'abc123',
                    'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/7',
                ],
            ],
            'issues' => [],
        ];
        file_put_contents($cacheDir . '/widget.json', json_encode($snapshot));

        $issueWithMr = self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']);
        $issueWithout = self::issuePayload(['nid' => 3489012]);
        $drupal = $this->drupalClientWithIssues([$issueWithMr, $issueWithout]);

        $tester = new CommandTester(new PatchesCommand($drupal));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringNotContainsString('#3467675', $display);
        self::assertStringContainsString('#3489012', $display);
    }

    /**
     * The scan asks drupal.org for Needs Review and RTBC, and the STATUS
     * column has to tell them apart — an RTBC orphan is the one a maintainer
     * acts on first. A node whose own status is neither (drupal.org's index
     * lags its nodes, so a just-retitled issue comes back under a status it no
     * longer holds) is still listed, uncoloured, rather than dropped.
     */
    public function testTheStatusColumnDistinguishesRtbcFromReviewAndToleratesNeither(): void
    {
        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3489012, 'field_issue_status' => '14']),
            self::issuePayload(['nid' => 3489013, 'field_issue_status' => '8']),
            self::issuePayload(['nid' => 3489014, 'field_issue_status' => '13']),
        ]);

        $tester = new CommandTester(new PatchesCommand($drupal, $this->gitlabClientWithMrs()));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('RTBC', $display);
        self::assertStringContainsString('review', $display);
        self::assertStringContainsString('needs work', $display);
        self::assertStringContainsString('3 issues without MRs', $display);
    }

    /**
     * @return iterable<string, array{array<string, int>}>
     */
    public static function gitlabLookupFailures(): iterable
    {
        yield 'the project cannot be resolved' => [['/projects/' => 404]];
        yield 'the merge-request list is closed' => [['/merge_requests?' => 403]];
    }

    /**
     * GitLab is only ever consulted here to *subtract* issues that already
     * have an MR, so when it cannot answer the scan degrades to showing
     * everything rather than failing. Over-reporting is the safe direction:
     * the maintainer sees one issue too many, never one too few, and the
     * command still exits 0 exactly as the no-token degraded mode does.
     *
     * @param array<string, int> $failing URL substring => HTTP status to answer with
     */
    #[\PHPUnit\Framework\Attributes\DataProvider('gitlabLookupFailures')]
    public function testAGitlabFailureDegradesToShowingEveryIssueRatherThanFailing(array $failing): void
    {
        $project = ['id' => 42, 'path_with_namespace' => 'project/widget', 'path' => 'widget'];
        $factory = static function (string $method, string $url) use ($failing, $project): MockResponse {
            foreach ($failing as $needle => $status) {
                if (str_contains($url, $needle)) {
                    return self::json(['message' => 'refused by the test'], $status);
                }
            }

            return self::json($project);
        };

        // 3467675 is the issue the MR list would have linked, so a working
        // cross-reference would have filtered it out.
        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']),
        ]);

        $tester = new CommandTester(new PatchesCommand(
            $drupal,
            new GitlabClient(new MockHttpClient($factory), 'test-token'),
        ));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('#3467675', $tester->getDisplay());
    }

    public function testSummaryLineShowsCountAndModules(): void
    {
        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(),
            self::issuePayload(['nid' => 3501234, 'title' => 'Another issue']),
        ]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('2 issues without MRs', $tester->getDisplay());
        self::assertStringContainsString('1 module', $tester->getDisplay());
    }
}

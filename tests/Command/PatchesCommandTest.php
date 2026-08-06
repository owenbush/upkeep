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

    /**
     * Diff refs for an MR that carries changes. The command asks the single-MR
     * endpoint for these because the list endpoint omits them.
     *
     * @return array<string, string>
     */
    private static function nonEmptyDiffRefs(): array
    {
        return ['base_sha' => 'base111', 'head_sha' => 'head222'];
    }

    /**
     * Diff refs for an MR whose branch holds nothing the target does not
     * already have — the shape git.drupalcode.org returns for an empty
     * Project Update Bot draft.
     *
     * @return array<string, string>
     */
    private static function emptyDiffRefs(): array
    {
        return ['base_sha' => 'same333', 'head_sha' => 'same333'];
    }

    /**
     * A GitLab client serving one project and the given merge requests, with
     * the single-MR endpoint answering from the same set.
     *
     * The single-MR route is matched *before* the project route: both URLs
     * contain "/projects/", and a factory that checked the project first would
     * answer a merge-request detail fetch with a project payload.
     *
     * @param list<array<string, mixed>> $mrs
     */
    private function gitlabClientServing(array $mrs): GitlabClient
    {
        $project = [
            'id' => 42,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'name' => 'Widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ];

        // The list endpoint does not return diff_refs; only the detail one does.
        $listed = array_map(static function (array $mr): array {
            unset($mr['diff_refs']);

            return $mr;
        }, $mrs);

        $factory = static function (string $method, string $url) use ($project, $mrs, $listed): MockResponse {
            if (str_contains($url, '/merge_requests?')) {
                return self::json($listed);
            }
            if (preg_match('#/merge_requests/(\d+)$#', $url, $m) === 1) {
                foreach ($mrs as $mr) {
                    $iid = $mr['iid'] ?? null;
                    if (\is_int($iid) && $iid === (int) $m[1]) {
                        return self::json($mr);
                    }
                }

                return self::json(['message' => '404 Not found'], 404);
            }
            if (str_contains($url, '/projects/')) {
                return self::json($project);
            }
            throw new \LogicException('Unrouted: ' . $url);
        };

        return new GitlabClient(new MockHttpClient($factory), 'test-token');
    }

    /**
     * @param array<string, mixed> $overrides
     *
     * @return array<string, mixed>
     */
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
            'sha' => 'abc123',
            'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/7',
            'diff_refs' => self::nonEmptyDiffRefs(),
        ];
    }

    /** @param array<string, mixed> $mrOverrides */
    private function gitlabClientWithMrs(array $mrOverrides = []): GitlabClient
    {
        return $this->gitlabClientServing([self::mrPayload($mrOverrides)]);
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
        self::assertStringContainsString('1 patch-only', $display);
    }

    /**
     * An issue whose work lives on a branch and nowhere else is the dashboard's
     * business, not this command's — it is the only case that is withheld.
     */
    public function testWithholdsIssuesWhoseOnlyContributionIsAMergeRequest(): void
    {
        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'field_issue_files' => []]),
            self::issuePayload(['nid' => 3489012]),
        ]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringNotContainsString('#3467675', $display);
        self::assertStringContainsString('#3489012', $display);
    }

    /**
     * A patch and a merge request on the same issue is an ordinary Drupal
     * situation — a patch posted first, an MR opened later, a re-roll posted
     * after that — and the patch half is invisible on the MR-centric
     * dashboard. So the issue is listed, with the MR column naming the branch
     * it sits beside rather than the row being dropped for having one.
     */
    public function testListsIssuesCarryingBothAPatchAndAMergeRequest(): void
    {
        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']),
        ]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('#3467675', $display);
        self::assertStringContainsString('!7', $display);
        self::assertStringNotContainsString('!7 empty', $display);
        self::assertStringContainsString('1 patch + MR', $display);
    }

    /**
     * --without-mr narrows the report to the issues no branch carries: the
     * patch-only ones, and the ones whose only MR is empty. It must not
     * re-hide the empty-MR rows, which are precisely the ones that look
     * covered and are not.
     */
    public function testWithoutMrOmitsIssuesARealBranchAlreadyCarries(): void
    {
        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']),
            self::issuePayload(['nid' => 3489012]),
            self::issuePayload(['nid' => 3597808, 'title' => 'Automated compatibility fixes']),
        ]);
        $gitlab = $this->gitlabClientServing([
            self::mrPayload(),
            self::mrPayload([
                'iid' => 1,
                'title' => 'Draft: Automated Project Update Bot fixes',
                'draft' => true,
                'source_branch' => '3597808-compat',
                'diff_refs' => self::emptyDiffRefs(),
                'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/1',
            ]),
        ]);

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit, '--without-mr' => true]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringNotContainsString('#3467675', $display);
        self::assertStringContainsString('#3489012', $display);
        self::assertStringContainsString('#3597808', $display);
        self::assertStringContainsString('!1 empty', $display);
    }

    /**
     * The regression this command was rebuilt around. The Project Update Bot
     * opens a draft MR whose description says "Relates to #NNN" and whose
     * branch holds no commits, on a great many contrib projects. That MR
     * neither claims authorship of the issue nor carries anything, so the
     * patches sitting on the issue must still be reported.
     *
     * Both defences are exercised at once here, because in the wild they
     * arrive together: the loose reference and the empty diff.
     */
    public function testAnEmptyBotMergeRequestDoesNotHideTheIssuesPatches(): void
    {
        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3597808, 'title' => 'Automated Drupal 11 compatibility fixes']),
        ]);
        $gitlab = $this->gitlabClientServing([
            self::mrPayload([
                'iid' => 1,
                'title' => 'Draft: Automated Project Update Bot fixes',
                'draft' => true,
                'source_branch' => 'project-update-bot-only',
                'description' => 'Relates to #3597808. This merge request was automatically created '
                    . 'by the Project Update Bot. It contains the changes from run 12-843071.',
                'diff_refs' => self::emptyDiffRefs(),
                'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/1',
            ]),
        ]);

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('#3597808', $display);
        self::assertStringContainsString('3489012-12-config-schema.patch', $display);
        // The loose "Relates to" reference is not authorship, so the MR is not
        // even attributed to the issue — the column reads as no MR at all.
        self::assertStringContainsString('1 patch-only', $display);
    }

    /**
     * An MR that *does* claim the issue but carries no diff is attributed to
     * it and flagged, because the useful thing to tell a maintainer is not
     * "there is no MR" but "the MR on this issue is empty — the patch is the
     * only work there is".
     */
    public function testAnEmptyMergeRequestOnItsOwnIssueIsShownAndFlagged(): void
    {
        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']),
        ]);
        $gitlab = $this->gitlabClientWithMrs(['diff_refs' => self::emptyDiffRefs()]);

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('#3467675', $display);
        self::assertStringContainsString('!7 empty', $display);
        self::assertStringContainsString('1 patch, empty MR', $display);
    }

    /**
     * A detail fetch that cannot be made leaves the MR's emptiness unknown,
     * and unknown is read as real work — the same reading a pre-diff_refs
     * cached snapshot gets. Over-reporting is the safe direction everywhere
     * else in this command; here, under-flagging is, because inventing an
     * "empty" label out of a failed request would tell a maintainer to close
     * a branch that may hold the fix.
     */
    public function testAnUnreachableDetailEndpointLeavesTheMergeRequestTreatedAsRealWork(): void
    {
        $project = [
            'id' => 42,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ];
        $listed = self::mrPayload();
        unset($listed['diff_refs']);

        $factory = static function (string $method, string $url) use ($project, $listed): MockResponse {
            if (str_contains($url, '/merge_requests?')) {
                return self::json([$listed]);
            }
            if (preg_match('#/merge_requests/\d+$#', $url) === 1) {
                return self::json(['message' => 'refused by the test'], 403);
            }

            return self::json($project);
        };

        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'field_issue_files' => []]),
        ]);

        $tester = new CommandTester(new PatchesCommand(
            $drupal,
            new GitlabClient(new MockHttpClient($factory), 'test-token'),
        ));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        // Treated as a real branch, so a patchless issue stays withheld.
        self::assertStringContainsString('Nothing to report', $tester->getDisplay());
    }

    /**
     * The detail endpoint is asked "is this merge request empty?" and nothing
     * else. A body that comes back identifying some other resource does not
     * get written into the row under the listed MR's identity.
     */
    public function testADetailPayloadForADifferentMergeRequestIsNotAdopted(): void
    {
        $project = [
            'id' => 42,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ];
        $listed = self::mrPayload();
        unset($listed['diff_refs']);

        $factory = static function (string $method, string $url) use ($project, $listed): MockResponse {
            if (str_contains($url, '/merge_requests?')) {
                return self::json([$listed]);
            }
            if (preg_match('#/merge_requests/\d+$#', $url) === 1) {
                // Same endpoint, wrong resource: iid 99, and empty.
                return self::json(['iid' => 99, 'diff_refs' => self::emptyDiffRefs()] + $listed);
            }

            return self::json($project);
        };

        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']),
        ]);

        $tester = new CommandTester(new PatchesCommand(
            $drupal,
            new GitlabClient(new MockHttpClient($factory), 'test-token'),
        ));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('!7', $display);
        self::assertStringNotContainsString('!99', $display);
        self::assertStringNotContainsString('empty', $display);
    }

    /**
     * The detail fetch exists only to answer a question the list payload could
     * not, so a listing that already carries diff refs is taken at its word and
     * costs nothing extra. Pinned by making the detail endpoint disagree: if it
     * were consulted anyway, its answer would be the one on screen.
     */
    public function testAnMrWhoseEmptinessTheListingAlreadySettledIsNotRefetched(): void
    {
        $project = [
            'id' => 42,
            'path' => 'widget',
            'path_with_namespace' => 'project/widget',
            'web_url' => 'https://git.drupalcode.org/project/widget',
        ];

        $factory = static function (string $method, string $url) use ($project): MockResponse {
            if (str_contains($url, '/merge_requests?')) {
                return self::json([self::mrPayload(['diff_refs' => self::emptyDiffRefs()])]);
            }
            if (preg_match('#/merge_requests/\d+$#', $url) === 1) {
                return self::json(self::mrPayload(['diff_refs' => self::nonEmptyDiffRefs()]));
            }

            return self::json($project);
        };

        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']),
        ]);

        $tester = new CommandTester(new PatchesCommand(
            $drupal,
            new GitlabClient(new MockHttpClient($factory), 'test-token'),
        ));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('!7 empty', $tester->getDisplay());
    }

    /**
     * Several merge requests claiming one issue: the substantive one
     * represents the row, so a real branch is never reported as "empty"
     * because a bot draft happened to be listed first.
     */
    public function testASubstantiveMergeRequestRepresentsAnIssueThatAlsoHasAnEmptyOne(): void
    {
        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']),
        ]);
        $gitlab = $this->gitlabClientServing([
            self::mrPayload([
                'iid' => 2,
                'title' => 'Issue #3467675: an abandoned start',
                'diff_refs' => self::emptyDiffRefs(),
                'web_url' => 'https://git.drupalcode.org/project/widget/-/merge_requests/2',
            ]),
            self::mrPayload(),
        ]);

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('!7 +1', $display);
        self::assertStringNotContainsString('empty', $display);
    }

    public function testShowsSuccessWhenNoIssuesExist(): void
    {
        $drupal = $this->drupalClientWithIssues([]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('Nothing to report', $tester->getDisplay());
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
        self::assertStringContainsString('1 nothing attached', $display);
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
        self::assertStringContainsString('1 module', $tester->getDisplay());
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

    /**
     * @param list<array<string, mixed>> $mergeRequests
     */
    private function writeSnapshot(array $mergeRequests): void
    {
        $cacheDir = $this->cockpit . '/cache/dashboard';
        mkdir($cacheDir, 0o755, true);

        file_put_contents($cacheDir . '/widget.json', json_encode([
            'fetched_at' => '2026-07-30T10:00:00+00:00',
            'project' => [
                'id' => 42,
                'path' => 'widget',
                'path_with_namespace' => 'project/widget',
                'name' => 'Widget',
                'web_url' => 'https://git.drupalcode.org/project/widget',
            ],
            'merge_requests' => $mergeRequests,
            'issues' => [],
        ], \JSON_THROW_ON_ERROR));
    }

    /**
     * A dashboard snapshot holds single-MR detail payloads, so its diff refs
     * are already there — the cached path settles emptiness without a single
     * GitLab request, which is why this test passes no client at all.
     */
    public function testUsesSnapshotCacheForMrCrossReference(): void
    {
        $this->writeSnapshot([self::mrPayload()]);

        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'field_issue_files' => []]),
            self::issuePayload(['nid' => 3489012]),
        ]);

        $tester = new CommandTester(new PatchesCommand($drupal));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringNotContainsString('#3467675', $display);
        self::assertStringContainsString('#3489012', $display);
    }

    public function testAnEmptyMergeRequestInASnapshotIsFlaggedWithoutRefetching(): void
    {
        $this->writeSnapshot([self::mrPayload(['diff_refs' => self::emptyDiffRefs()])]);

        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']),
        ]);

        $tester = new CommandTester(new PatchesCommand($drupal));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('!7 empty', $tester->getDisplay());
    }

    /**
     * A snapshot written by an upkeep that predates diff-ref caching carries
     * no emptiness at all. Rather than refetch (the cached path is chosen
     * precisely to avoid the network) or invent a verdict, its MRs read as
     * real work — the behaviour that release had.
     */
    public function testASnapshotWithoutDiffRefsFallsBackToTreatingMrsAsRealWork(): void
    {
        $legacy = self::mrPayload();
        unset($legacy['diff_refs']);
        $this->writeSnapshot([$legacy]);

        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'field_issue_files' => []]),
        ]);

        $tester = new CommandTester(new PatchesCommand($drupal));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('Nothing to report', $tester->getDisplay());
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
        self::assertStringContainsString('3 issues', $display);
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
     * GitLab is only ever consulted here to *subtract* issues a branch already
     * carries, so when it cannot answer the scan degrades to showing
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

        // 3467675 is the issue the MR list would have carried, so a working
        // cross-reference would have filtered it out.
        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(['nid' => 3467675, 'field_issue_files' => []]),
        ]);

        $tester = new CommandTester(new PatchesCommand(
            $drupal,
            new GitlabClient(new MockHttpClient($factory), 'test-token'),
        ));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('#3467675', $tester->getDisplay());
    }

    /**
     * The summary counts what the table shows, split by how the work arrived,
     * so a maintainer reading only the last line still learns which pile to
     * work through.
     */
    public function testSummaryLineBreaksTheCountDownByContributionKind(): void
    {
        $drupal = $this->drupalClientWithIssues([
            self::issuePayload(),
            self::issuePayload(['nid' => 3501234, 'title' => 'Another issue']),
            self::issuePayload(['nid' => 3467675, 'title' => 'Make URL field required']),
        ]);
        $gitlab = $this->gitlabClientWithMrs();

        $tester = new CommandTester(new PatchesCommand($drupal, $gitlab));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringContainsString('3 issues', $display);
        self::assertStringContainsString('2 patch-only', $display);
        self::assertStringContainsString('1 patch + MR', $display);
        self::assertStringContainsString('1 module', $display);
        self::assertStringContainsString('statuses: review, RTBC', $display);
    }
}

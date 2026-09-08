<?php

declare(strict_types=1);

namespace Upkeep\Tests\Drupal;

use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\IssueStatus;

/**
 * What this client does when drupal.org does not answer.
 *
 * The failure mode is the dangerous one: it degrades by returning *less data*,
 * which at the call site is indistinguishable from there being less data. A
 * dropped attachment looks like an issue with fewer patches; a truncated page
 * looks like a project with fewer issues. Both are the under-reporting the
 * patch surface exists to prevent, so both must be said out loud.
 */
final class DrupalOrgClientResilienceTest extends TestCase
{
    private const PROJECT_NID = 12345;

    /** @param array<array-key, mixed> $payload */
    private static function json(array $payload, int $status = 200): MockResponse
    {
        return new MockResponse(json_encode($payload, \JSON_THROW_ON_ERROR), ['http_code' => $status]);
    }

    /** @return array<string, mixed> */
    private static function issueWithAttachments(int $nid, int ...$fids): array
    {
        return [
            'nid' => $nid,
            'title' => 'An issue',
            'field_issue_status' => '8',
            'field_project' => ['machine_name' => 'widget'],
            'field_issue_files' => array_map(
                static fn (int $fid): array => ['file' => [
                    'uri' => 'https://www.drupal.org/api-d7/file/' . $fid,
                    'id' => (string) $fid,
                    'resource' => 'file',
                ]],
                $fids,
            ),
        ];
    }

    /** @return array<string, mixed> */
    private static function fileResource(int $fid): array
    {
        return [
            'fid' => (string) $fid,
            'name' => $fid . '-fix.patch',
            'url' => 'https://www.drupal.org/files/issues/' . $fid . '-fix.patch',
            'timestamp' => '1705400000',
        ];
    }

    /**
     * @param callable(string): MockResponse $router
     * @param ?\Closure(int): void           $sleeper
     */
    private function client(callable $router, ?\Closure $sleeper = null): DrupalOrgClient
    {
        return new DrupalOrgClient(
            new MockHttpClient(static fn (string $method, string $url): MockResponse => $router($url)),
            'https://www.drupal.org/api-d7',
            $sleeper,
        );
    }

    /**
     * The regression this guards: a dropped attachment used to shrink the
     * issue's patch count with nothing said, so a maintainer read "1 patch" on
     * an issue carrying two and had no way to know.
     */
    public function testADroppedAttachmentIsReportedRatherThanSilentlyShrinkingTheIssue(): void
    {
        $client = $this->client(static function (string $url): MockResponse {
            if (str_contains($url, 'field_project_machine_name=')) {
                return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
            }
            if (str_contains($url, '/file/7002')) {
                return new MockResponse('', ['http_code' => 500]);
            }
            if (preg_match('#/file/(\d+)#', $url, $m) === 1) {
                return self::json(self::fileResource((int) $m[1]));
            }

            return self::json(['list' => [self::issueWithAttachments(100, 7001, 7002)]]);
        });

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertCount(1, $issues);
        self::assertSame(1, $issues[0]->patchCount(), 'the readable attachment still counts');
        self::assertNotSame([], $client->warnings());
        self::assertStringContainsString('7002', implode("\n", $client->warnings()));
        self::assertStringContainsString('fewer patches', implode("\n", $client->warnings()));
    }

    /**
     * A page that cannot be read ends the listing — there is no way to skip it
     * and stay in order — but silence would report a truncated project as a
     * complete one.
     */
    public function testATruncatedListingSaysSo(): void
    {
        $page = 0;
        $client = $this->client(static function (string $url) use (&$page): MockResponse {
            if (str_contains($url, 'field_project_machine_name=')) {
                return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
            }
            if (preg_match('#/file/(\d+)#', $url, $m) === 1) {
                return self::json(self::fileResource((int) $m[1]));
            }
            ++$page;

            return $page === 1
                ? self::json([
                    'list' => [self::issueWithAttachments(100, 7001)],
                    'next' => 'https://www.drupal.org/api-d7/node.json?page=1',
                ])
                : new MockResponse('', ['http_code' => 503]);
        });

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertCount(1, $issues, 'what was read is kept');
        self::assertStringContainsString('incomplete', implode("\n", $client->warnings()));
    }

    public function testACleanRunWarnsAboutNothing(): void
    {
        $client = $this->client(static function (string $url): MockResponse {
            if (str_contains($url, 'field_project_machine_name=')) {
                return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
            }
            if (preg_match('#/file/(\d+)#', $url, $m) === 1) {
                return self::json(self::fileResource((int) $m[1]));
            }

            return self::json(['list' => [self::issueWithAttachments(100, 7001)]]);
        });

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertSame(1, $issues[0]->patchCount());
        self::assertSame([], $client->warnings());
    }

    /**
     * A throttled batch is slept off once and retried, because the alternative
     * — dropping the attachments — is the silent under-reporting this client
     * is careful to avoid. The wait is injected so no suite ever really sleeps.
     */
    public function testAThrottledBatchIsRetriedOnceAfterTheRequestedWait(): void
    {
        $attempts = [];
        $slept = [];
        $client = $this->client(
            static function (string $url) use (&$attempts): MockResponse {
                if (str_contains($url, 'field_project_machine_name=')) {
                    return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
                }
                if (preg_match('#/file/(\d+)#', $url, $m) === 1) {
                    $fid = (int) $m[1];
                    $attempts[$fid] = ($attempts[$fid] ?? 0) + 1;

                    return $attempts[$fid] === 1
                        ? new MockResponse('', ['http_code' => 429, 'response_headers' => ['retry-after' => '2']])
                        : self::json(self::fileResource($fid));
                }

                return self::json(['list' => [self::issueWithAttachments(100, 7001)]]);
            },
            static function (int $seconds) use (&$slept): void {
                $slept[] = $seconds;
            },
        );

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertSame([2], $slept, 'the wait honours Retry-After');
        self::assertSame(2, $attempts[7001], 'retried exactly once');
        self::assertSame(1, $issues[0]->patchCount(), 'the retry recovered the attachment');
        self::assertStringContainsString('429', implode("\n", $client->warnings()));
    }

    /** A second 429 is reported, not slept off again — one retry, then the truth. */
    public function testAPersistentlyThrottledAttachmentIsReportedRatherThanRetriedForever(): void
    {
        $calls = 0;
        $slept = [];
        $client = $this->client(
            static function (string $url) use (&$calls): MockResponse {
                if (str_contains($url, 'field_project_machine_name=')) {
                    return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
                }
                if (str_contains($url, '/file/')) {
                    ++$calls;

                    return new MockResponse('', ['http_code' => 429]);
                }

                return self::json(['list' => [self::issueWithAttachments(100, 7001)]]);
            },
            static function (int $seconds) use (&$slept): void {
                $slept[] = $seconds;
            },
        );

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertSame(2, $calls, 'one retry, no more');
        self::assertCount(1, $slept);
        self::assertSame(0, $issues[0]->patchCount());
        self::assertStringContainsString('rate-limiting', implode("\n", $client->warnings()));
    }

    /**
     * A Retry-After longer than the cap is refused rather than slept through:
     * a CLI that appears to hang is worse than one that says it was throttled.
     */
    public function testAnUnreasonableRetryAfterIsRefusedRatherThanWaitedOut(): void
    {
        $slept = [];
        $client = $this->client(
            static function (string $url): MockResponse {
                if (str_contains($url, 'field_project_machine_name=')) {
                    return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
                }
                if (str_contains($url, '/file/')) {
                    return new MockResponse('', [
                        'http_code' => 429,
                        'response_headers' => ['retry-after' => '600'],
                    ]);
                }

                return self::json(['list' => [self::issueWithAttachments(100, 7001)]]);
            },
            static function (int $seconds) use (&$slept): void {
                $slept[] = $seconds;
            },
        );

        $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertSame([], $slept, 'nothing may sleep for ten minutes');
        self::assertStringContainsString('rate-limiting', implode("\n", $client->warnings()));
    }

    /**
     * A batch is at most MAX_CONCURRENT requests in flight — politeness towards
     * an endpoint that publishes no quota to stay inside.
     */
    public function testAttachmentLookupsAreBatchedToTheConcurrencyCap(): void
    {
        $fids = range(7001, 7020);

        // Dispatch and consumption are both recorded, in order: MockHttpClient
        // invokes the factory when request() is called (the wire moment), and
        // the generator body runs when the response is read. How many
        // dispatches precede the first read is the batch size.
        $events = [];

        $client = $this->client(static function (string $url) use ($fids, &$events): MockResponse {
            if (str_contains($url, 'field_project_machine_name=')) {
                return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
            }
            if (preg_match('#/file/(\d+)#', $url, $m) === 1) {
                $fid = (int) $m[1];
                $events[] = 'dispatch';
                $body = static function () use ($fid, &$events): \Generator {
                    $events[] = 'read';
                    yield json_encode(self::fileResource($fid), \JSON_THROW_ON_ERROR);
                };

                return new MockResponse($body());
            }

            return self::json(['list' => [self::issueWithAttachments(100, ...$fids)]]);
        });

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertSame(20, $issues[0]->patchCount(), 'every attachment resolved');

        $firstRead = array_search('read', $events, true);
        self::assertIsInt($firstRead);
        self::assertLessThanOrEqual(
            DrupalOrgClient::MAX_CONCURRENT,
            $firstRead,
            'no more than the cap may be put on the wire before any is consumed',
        );
        self::assertGreaterThan(1, $firstRead, 'and more than one, or nothing is running concurrently');
    }

    /**
     * Every attachment across a whole listing page is resolved in one sweep,
     * so an issue's attachments do not each wait for the previous issue's.
     */
    public function testAWholePageOfAttachmentsIsResolvedTogether(): void
    {
        $client = $this->client(static function (string $url): MockResponse {
            if (str_contains($url, 'field_project_machine_name=')) {
                return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
            }
            if (preg_match('#/file/(\d+)#', $url, $m) === 1) {
                return self::json(self::fileResource((int) $m[1]));
            }

            return self::json(['list' => [
                self::issueWithAttachments(100, 7001, 7002),
                self::issueWithAttachments(200, 7003),
            ]]);
        });

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertSame(2, $issues[0]->patchCount());
        self::assertSame(1, $issues[1]->patchCount());
        self::assertSame([], $client->warnings());
    }

    /** A file cited by two issues is fetched once, not once per citation. */
    public function testAFileSharedBetweenIssuesIsFetchedOnce(): void
    {
        $fileCalls = 0;
        $client = $this->client(static function (string $url) use (&$fileCalls): MockResponse {
            if (str_contains($url, 'field_project_machine_name=')) {
                return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
            }
            if (preg_match('#/file/(\d+)#', $url, $m) === 1) {
                ++$fileCalls;

                return self::json(self::fileResource((int) $m[1]));
            }

            return self::json(['list' => [
                self::issueWithAttachments(100, 7001),
                self::issueWithAttachments(200, 7001),
            ]]);
        });

        $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertSame(1, $fileCalls);
    }

    /** The single-issue endpoint resolves its attachments the same way. */
    public function testTheSingleIssueEndpointAlsoBatchesAndReports(): void
    {
        $client = $this->client(static function (string $url): MockResponse {
            if (str_contains($url, '/file/7002')) {
                return new MockResponse('', ['http_code' => 404]);
            }
            if (preg_match('#/file/(\d+)#', $url, $m) === 1) {
                return self::json(self::fileResource((int) $m[1]));
            }

            return self::json(self::issueWithAttachments(100, 7001, 7002));
        });

        $issue = $client->issue(100);

        self::assertSame(1, $issue?->patchCount());
        self::assertStringContainsString('7002', implode("\n", $client->warnings()));
    }

    /** A body that is valid JSON but not a file object is a miss, and said. */
    public function testAFileResponseThatIsNotAFileObjectIsAMiss(): void
    {
        $client = $this->client(static function (string $url): MockResponse {
            if (str_contains($url, '/file/')) {
                return self::json([]);
            }

            return self::json(self::issueWithAttachments(100, 7001));
        });

        self::assertSame(0, $client->issue(100)?->patchCount());
        self::assertStringContainsString('not a file object', implode("\n", $client->warnings()));
    }

    public function testATransportFailureOnAnAttachmentIsReported(): void
    {
        $client = $this->client(static function (string $url): MockResponse {
            if (str_contains($url, '/file/')) {
                return new MockResponse('', ['error' => 'Connection refused by the test']);
            }

            return self::json(self::issueWithAttachments(100, 7001));
        });

        self::assertSame(0, $client->issue(100)?->patchCount());
        self::assertStringContainsString('Connection refused', implode("\n", $client->warnings()));
    }

    /**
     * A request that cannot even be dispatched — a client refusing the URL, a
     * DNS failure raised eagerly — is the same kind of shortfall as one that
     * comes back wrong, and is reported the same way.
     */
    public function testARequestThatCannotBeDispatchedIsReported(): void
    {
        $client = $this->client(static function (string $url): MockResponse {
            if (str_contains($url, '/file/')) {
                throw new \RuntimeException('the client refused this URL');
            }

            return self::json(self::issueWithAttachments(100, 7001));
        });

        self::assertSame(0, $client->issue(100)?->patchCount());
        self::assertStringContainsString('the client refused this URL', implode("\n", $client->warnings()));
    }

    /**
     * The injected wait exists so the suite never sleeps — but the real one has
     * to actually wait, or the retry hammers a server that just asked for room.
     * The only honest way to show that is to let it happen once — this is the
     * one test in the suite that deliberately costs a second.
     */
    public function testTheDefaultWaitReallySleeps(): void
    {
        $calls = 0;
        // No sleeper injected: this is the production default.
        $client = new DrupalOrgClient(
            new MockHttpClient(static function (string $method, string $url) use (&$calls): MockResponse {
                if (str_contains($url, '/file/')) {
                    ++$calls;

                    return $calls === 1
                        ? new MockResponse('', ['http_code' => 429])
                        : self::json(self::fileResource(7001));
                }

                return self::json(self::issueWithAttachments(100, 7001));
            }),
        );

        $started = microtime(true);
        $issue = $client->issue(100);
        $elapsed = microtime(true) - $started;

        self::assertSame(2, $calls);
        self::assertSame(1, $issue?->patchCount(), 'the wait was followed by a successful retry');
        // Half the second it asks for, deliberately.
        //
        // `sleep(1)` does not guarantee a second of wall clock: it returns
        // early when a signal arrives, and under a coverage-instrumented run
        // this was measured returning in 0.91s. Any threshold near the
        // boundary is therefore a flake generator, and this one was — it
        // failed twice in a day and passed on rerun both times.
        //
        // What the test is actually for is that the *production default* is a
        // real sleep rather than a no-op, and that has three orders of
        // magnitude of headroom: a fake sleeper or a dropped call returns in
        // microseconds. Half a second proves the claim and cannot be reached
        // by jitter.
        self::assertGreaterThanOrEqual(0.5, $elapsed, 'the default wait must really wait');
    }

    /**
     * A badly degraded run produces one warning per dropped attachment, which
     * could be dozens. The list is capped for display, but the *count* is
     * always exact — an operator has to be able to tell "one file blipped"
     * from "half the scan failed".
     */
    public function testEveryShortfallIsRecordedEvenWhenTheDisplayWouldCapThem(): void
    {
        $fids = range(7001, 7012);
        $client = $this->client(static function (string $url) use ($fids): MockResponse {
            if (str_contains($url, 'field_project_machine_name=')) {
                return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
            }
            if (str_contains($url, '/file/')) {
                return new MockResponse('', ['http_code' => 500]);
            }

            return self::json(['list' => [self::issueWithAttachments(100, ...$fids)]]);
        });

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertSame(0, $issues[0]->patchCount());
        self::assertCount(12, $client->warnings(), 'one per dropped attachment, none collapsed');
    }

    public function testAnUnresolvableProjectIsReported(): void
    {
        $client = $this->client(static fn (string $url): MockResponse => self::json(['list' => []]));

        self::assertSame([], $client->projectIssues('widget', [IssueStatus::NeedsReview]));
        self::assertSame([], $client->warnings(), 'an empty answer is a real answer, not a failure');
    }

    public function testAProjectLookupFailureIsReported(): void
    {
        $client = $this->client(static fn (string $url): MockResponse => new MockResponse('', ['http_code' => 500]));

        self::assertSame([], $client->projectIssues('widget', [IssueStatus::NeedsReview]));
        self::assertStringContainsString('HTTP 500', implode("\n", $client->warnings()));
    }

    public function testAThrottledProjectLookupIsNamedAsThrottling(): void
    {
        $client = $this->client(static fn (string $url): MockResponse => new MockResponse('', ['http_code' => 429]));

        self::assertSame([], $client->projectIssues('widget', [IssueStatus::NeedsReview]));
        self::assertStringContainsString('rate-limiting', implode("\n", $client->warnings()));
    }

    public function testAnUnreadableBodyOnALookupIsReported(): void
    {
        $client = $this->client(static fn (string $url): MockResponse => new MockResponse('42'));

        self::assertSame([], $client->projectIssues('widget', [IssueStatus::NeedsReview]));
        self::assertStringContainsString('shape this client cannot read', implode("\n", $client->warnings()));
    }
}

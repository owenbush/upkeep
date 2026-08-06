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
    private const PROJECT_NID = 12345;

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

    /**
     * An attachment exactly as api-d7 returns one: a reference, with no name,
     * no URL and no timestamp on it.
     *
     * @return array<string, mixed>
     */
    private static function attachmentReference(int $fid): array
    {
        return [
            'file' => [
                'uri' => 'https://www.drupal.org/api-d7/file/' . $fid,
                'id' => (string) $fid,
                'resource' => 'file',
            ],
            'display' => '1',
        ];
    }

    /**
     * The file resource the reference points at. Note `name` rather than
     * `filename`, and no `filesize` — the shape the live endpoint returns.
     *
     * @return array<string, mixed>
     */
    private static function fileResource(int $fid, string $name): array
    {
        return [
            'fid' => (string) $fid,
            'name' => $name,
            'url' => 'https://www.drupal.org/files/issues/' . $name,
            'filename' => null,
            'filesize' => null,
            'timestamp' => '1781211358',
        ];
    }

    /** @param array<array-key, mixed> $payload */
    private static function json(array $payload): MockResponse
    {
        return new MockResponse(json_encode($payload, \JSON_THROW_ON_ERROR));
    }

    /**
     * A client whose endpoints are routed by URL, because a project scan now
     * touches three of them: the project lookup that turns a machine name into
     * a node id, the issue listing, and the file resource behind each
     * attachment reference.
     *
     * @param list<array<array-key, mixed>>       $issues listing entries, in order of request
     * @param array<int, array<string, mixed>>    $files  file id => file resource
     * @param callable(string): ?MockResponse|null $override consulted first
     */
    private function routedClient(array $issues, array $files = [], ?callable $override = null): DrupalOrgClient
    {
        $page = 0;
        $factory = static function (string $method, string $url) use ($issues, $files, $override, &$page) {
            if ($override !== null) {
                $answer = $override($url);
                if ($answer !== null) {
                    return $answer;
                }
            }
            if (str_contains($url, 'field_project_machine_name=')) {
                return self::json(['list' => [['nid' => self::PROJECT_NID, 'type' => 'project_module']]]);
            }
            if (preg_match('#/file/(\d+)\.json#', $url, $m) === 1) {
                $fid = (int) $m[1];

                return isset($files[$fid])
                    ? self::json($files[$fid])
                    : new MockResponse('', ['http_code' => 404]);
            }
            if (str_contains($url, 'type=project_issue')) {
                $list = $issues[$page] ?? [];
                ++$page;

                return self::json(['list' => $list]);
            }
            throw new \LogicException('Unrouted: ' . $url);
        };

        return new DrupalOrgClient(new MockHttpClient($factory));
    }

    public function testFetchesAndParsesAnIssue(): void
    {
        $client = new DrupalOrgClient(new MockHttpClient(self::json(self::issuePayload())));

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

            return self::json(self::issuePayload());
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
            self::json(self::issuePayload(['field_issue_status' => '999'])),
        ));

        self::assertNull($client->issue(3467675));
    }

    public function testRtbcStatus(): void
    {
        $client = new DrupalOrgClient(new MockHttpClient(
            self::json(self::issuePayload(['field_issue_status' => '14'])),
        ));

        $issue = $client->issue(3467675);
        self::assertNotNull($issue);
        self::assertSame(IssueStatus::Rtbc, $issue->status);
        self::assertSame('Reviewed & tested by the community', $issue->status->label());
    }

    public function testProjectIssuesReturnsIssuesFromListing(): void
    {
        $client = $this->routedClient([[
            self::issuePayload(['nid' => 100, 'field_issue_status' => '8']),
            self::issuePayload(['nid' => 200, 'field_issue_status' => '8']),
        ]]);

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertCount(2, $issues);
        self::assertSame(100, $issues[0]->nid);
        self::assertSame(200, $issues[1]->nid);
    }

    /**
     * api-d7 exposes `field_project_machine_name` on project nodes only, so
     * filtering the *issue* listing by it matches nothing — an empty result
     * indistinguishable from "no issues in that status", which is how a patch
     * report came to report nothing at all. The machine name is resolved to a
     * node id first, and the listing filtered on `field_project`.
     */
    public function testTheListingIsFilteredByProjectNodeIdNotMachineName(): void
    {
        $urls = [];
        $client = new DrupalOrgClient(new MockHttpClient(
            static function (string $method, string $url) use (&$urls): MockResponse {
                $urls[] = $url;
                if (str_contains($url, 'field_project_machine_name=')) {
                    return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
                }

                return self::json(['list' => [self::issuePayload(['nid' => 100])]]);
            },
        ));

        $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertStringContainsString('field_project_machine_name=widget', $urls[0]);
        self::assertStringContainsString('field_project=' . self::PROJECT_NID, $urls[1]);
        self::assertStringNotContainsString('field_project_machine_name', $urls[1]);
    }

    /**
     * The lookup is per project, not per status or per page: two statuses are
     * two listing requests behind one resolution.
     */
    public function testTheProjectLookupHappensOncePerRun(): void
    {
        $lookups = 0;
        $client = new DrupalOrgClient(new MockHttpClient(
            static function (string $method, string $url) use (&$lookups): MockResponse {
                if (str_contains($url, 'field_project_machine_name=')) {
                    ++$lookups;

                    return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
                }

                return self::json(['list' => []]);
            },
        ));

        $client->projectIssues('widget', [IssueStatus::NeedsReview, IssueStatus::Rtbc]);
        $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertSame(1, $lookups);
    }

    /**
     * A project whose machine name resolves to nothing has no issues to
     * report, and asks for no listing at all.
     */
    public function testAnUnresolvableProjectYieldsNoIssuesAndNoListingRequest(): void
    {
        foreach ([['list' => []], ['list' => 'not-a-list'], ['list' => ['no-nid-here']]] as $answer) {
            $client = new DrupalOrgClient(new MockHttpClient(
                static function (string $method, string $url) use ($answer): MockResponse {
                    if (str_contains($url, 'field_project_machine_name=')) {
                        return self::json($answer);
                    }
                    throw new \LogicException('The listing must not be requested: ' . $url);
                },
            ));

            self::assertSame([], $client->projectIssues('widget', [IssueStatus::NeedsReview]));
        }
    }

    /**
     * api-d7 never inlines an attachment: it returns a reference carrying an
     * id and nothing else. The name that decides whether an attachment is a
     * patch lives one request away, on the file resource — so an issue read
     * without that request has zero attachments, and a patch report built on
     * it has nothing to report.
     */
    public function testAttachmentReferencesAreDereferencedIntoRealFiles(): void
    {
        $client = $this->routedClient(
            [[self::issuePayload([
                'nid' => 100,
                'field_issue_files' => [self::attachmentReference(7128077), self::attachmentReference(7128078)],
            ])]],
            [
                7128077 => self::fileResource(7128077, 'entity_type_access_conditions.1.0.1.rector.patch'),
                7128078 => self::fileResource(7128078, 'before-and-after.png'),
            ],
        );

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertCount(1, $issues);
        self::assertCount(2, $issues[0]->files);
        self::assertSame(1, $issues[0]->patchCount(), 'the screenshot is not a patch');
        self::assertSame(
            'entity_type_access_conditions.1.0.1.rector.patch',
            $issues[0]->latestPatch()?->name,
        );
    }

    /**
     * The single-issue endpoint returns the same references, and the dashboard
     * reads its patch flag from them.
     */
    public function testAttachmentsAreDereferencedOnTheSingleIssueEndpointToo(): void
    {
        $client = new DrupalOrgClient(new MockHttpClient(
            static function (string $method, string $url): MockResponse {
                if (str_contains($url, '/file/')) {
                    return self::json(self::fileResource(7128077, '3467675-4-fix.patch'));
                }

                return self::json(self::issuePayload([
                    'field_issue_files' => ['und' => [self::attachmentReference(7128077)]],
                ]));
            },
        ));

        $issue = $client->issue(3467675);

        self::assertNotNull($issue);
        self::assertSame(1, $issue->patchCount());
        self::assertSame('3467675-4-fix.patch', $issue->latestPatch()?->name);
    }

    /**
     * One request per distinct file, however many issues cite it.
     */
    public function testAFileIsFetchedOncePerRun(): void
    {
        $fileCalls = 0;
        $client = new DrupalOrgClient(new MockHttpClient(
            static function (string $method, string $url) use (&$fileCalls): MockResponse {
                if (str_contains($url, 'field_project_machine_name=')) {
                    return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
                }
                if (str_contains($url, '/file/')) {
                    ++$fileCalls;

                    return self::json(self::fileResource(7128077, 'shared.patch'));
                }

                return self::json(['list' => [
                    self::issuePayload(['nid' => 100, 'field_issue_files' => [self::attachmentReference(7128077)]]),
                    self::issuePayload(['nid' => 200, 'field_issue_files' => [self::attachmentReference(7128077)]]),
                ]]);
            },
        ));

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertCount(2, $issues);
        self::assertSame(1, $fileCalls);
    }

    /**
     * An attachment whose file resource cannot be read stays a reference and
     * is dropped, rather than becoming a nameless attachment that the patch
     * test cannot classify. The rest of the issue survives.
     */
    public function testAnUnreadableFileResourceCostsThatAttachmentAndNothingElse(): void
    {
        $client = $this->routedClient(
            [[self::issuePayload([
                'nid' => 100,
                'field_issue_files' => [self::attachmentReference(404404), self::attachmentReference(7128077)],
            ])]],
            [7128077 => self::fileResource(7128077, 'good.patch')],
        );

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertCount(1, $issues);
        self::assertCount(1, $issues[0]->files);
        self::assertSame('good.patch', $issues[0]->files[0]->name);
    }

    /**
     * A lookup that never reaches drupal.org at all — DNS, TLS, a dropped
     * connection — is absorbed exactly as an HTTP failure is. This client's
     * contract is that the caller gets less data, never an exception.
     */
    public function testATransportFailureOnALookupIsAbsorbedLikeAnyOtherFailure(): void
    {
        $client = $this->routedClient(
            [[self::issuePayload(['nid' => 100, 'field_issue_files' => [self::attachmentReference(7128077)]])]],
            [],
            static fn (string $url): ?MockResponse => str_contains($url, '/file/')
                ? new MockResponse('', ['error' => 'Connection refused by the test'])
                : null,
        );

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertCount(1, $issues);
        self::assertSame([], $issues[0]->files);
    }

    /**
     * Attachment shapes that need no lookup are left alone — an already
     * resolved payload (a cached snapshot round-trips one), a malformed entry,
     * and a reference with no id to follow.
     */
    public function testAttachmentsThatCannotOrNeedNotBeLookedUpAreLeftAsTheyAre(): void
    {
        $inline = [
            'file' => [
                'filename' => 'already-here.patch',
                'url' => 'https://www.drupal.org/files/issues/already-here.patch',
                'filesize' => '2048',
                'timestamp' => '1705400000',
            ],
        ];

        $client = $this->routedClient(
            [[self::issuePayload([
                'nid' => 100,
                'field_issue_files' => [
                    $inline,
                    'not-an-entry',
                    ['file' => 'not-an-object'],
                    ['file' => ['resource' => 'file']],
                ],
            ])]],
            [],
            static fn (string $url): ?MockResponse => str_contains($url, '/file/')
                ? throw new \LogicException('No file lookup should happen: ' . $url)
                : null,
        );

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertCount(1, $issues[0]->files);
        self::assertSame('already-here.patch', $issues[0]->files[0]->name);
    }

    /** An attachments field that is not a structure at all is passed through. */
    public function testANonArrayAttachmentsFieldIsLeftAlone(): void
    {
        $client = $this->routedClient(
            [[self::issuePayload(['nid' => 100, 'field_issue_files' => 'nonsense'])]],
        );

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertSame([], $issues[0]->files);
    }

    public function testProjectIssuesMergesMultipleStatuses(): void
    {
        $client = $this->routedClient([
            [self::issuePayload(['nid' => 100, 'field_issue_status' => '8'])],
            [self::issuePayload(['nid' => 200, 'field_issue_status' => '14'])],
        ]);

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview, IssueStatus::Rtbc]);

        self::assertCount(2, $issues);
        self::assertSame([100, 200], array_map(static fn ($i): int => $i->nid, $issues));
    }

    public function testProjectIssuesReturnsEmptyOnHttpFailure(): void
    {
        $client = new DrupalOrgClient(new MockHttpClient(
            static function (string $method, string $url): MockResponse {
                if (str_contains($url, 'field_project_machine_name=')) {
                    return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
                }

                return new MockResponse('', ['http_code' => 500]);
            },
        ));

        self::assertSame([], $client->projectIssues('widget', [IssueStatus::NeedsReview]));
    }

    public function testProjectIssuesDeduplicatesByNid(): void
    {
        $issue = self::issuePayload(['nid' => 100, 'field_issue_status' => '8']);
        $client = $this->routedClient([[$issue, $issue]]);

        self::assertCount(1, $client->projectIssues('widget', [IssueStatus::NeedsReview]));
    }

    /**
     * The api-d7 endpoint is public, unversioned and outside this project's
     * control. Anything that is valid JSON but not the documented object shape
     * — an error string, a bare null, an interstitial — must read as "no
     * issue" on both endpoints rather than reaching the models as mixed.
     */
    public function testAJsonBodyThatIsNotAnObjectReadsAsNoIssueOnEitherEndpoint(): void
    {
        $single = new DrupalOrgClient(new MockHttpClient(new MockResponse('"service unavailable"')));
        self::assertNull($single->issue(3467675));

        $listing = new DrupalOrgClient(new MockHttpClient(
            static function (string $method, string $url): MockResponse {
                return str_contains($url, 'field_project_machine_name=')
                    ? self::json(['list' => [['nid' => self::PROJECT_NID]]])
                    : new MockResponse('42');
            },
        ));
        self::assertSame([], $listing->projectIssues('widget', [IssueStatus::NeedsReview]));
    }

    public function testUnusableEntriesInAListingAreSkippedWhileTheRestOfThePageIsKept(): void
    {
        $client = $this->routedClient([[
            'not-an-object',
            self::issuePayload(['nid' => 100, 'field_issue_status' => '8']),
            self::issuePayload(['nid' => 200, 'field_issue_status' => '999']),
        ]]);

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertCount(1, $issues);
        self::assertSame(100, $issues[0]->nid);
    }

    public function testAnUnreadablePageEndsTheListingInsteadOfEscapingAsAnException(): void
    {
        // Failures are absorbed here by design: the caller gets whatever was
        // gathered and decides how to degrade. A half-read listing must not
        // take down the command that asked for it.
        $listingCalls = 0;
        $client = new DrupalOrgClient(new MockHttpClient(
            static function (string $method, string $url) use (&$listingCalls): MockResponse {
                if (str_contains($url, 'field_project_machine_name=')) {
                    return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
                }
                ++$listingCalls;

                return $listingCalls === 1
                    ? self::json([
                        'list' => [self::issuePayload(['nid' => 100, 'field_issue_status' => '8'])],
                        'next' => 'https://www.drupal.org/api-d7/node.json?page=1',
                    ])
                    : new MockResponse('<html>gateway interstitial</html>');
            },
        ));

        $issues = $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        self::assertSame(2, $listingCalls, 'A "next" link must be followed.');
        self::assertCount(1, $issues, 'What was already read is kept.');
    }

    public function testProjectIssuesCachesIndividualIssues(): void
    {
        $listingCalls = 0;
        $client = new DrupalOrgClient(new MockHttpClient(
            static function (string $method, string $url) use (&$listingCalls): MockResponse {
                if (str_contains($url, 'field_project_machine_name=')) {
                    return self::json(['list' => [['nid' => self::PROJECT_NID]]]);
                }
                if (str_contains($url, '/node/')) {
                    throw new \LogicException('issue() should have been served from the cache');
                }
                ++$listingCalls;

                return self::json(['list' => [self::issuePayload(['nid' => 100, 'field_issue_status' => '8'])]]);
            },
        ));

        $client->projectIssues('widget', [IssueStatus::NeedsReview]);

        $cached = $client->issue(100);
        self::assertNotNull($cached);
        self::assertSame(100, $cached->nid);
        self::assertSame(1, $listingCalls);
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Workflow;

use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Cockpit\Module;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Cockpit\RegistryException;
use Upkeep\Workflow\MrContextResolver;
use Upkeep\Workflow\WorkflowException;

final class MrContextResolverTest extends TestCase
{
    /**
     * @param list<MockResponse> $responses
     */
    /**
     * @param list<MockResponse> $responses
     * @param list<string>       $coresOnDisk base artifacts, for a module the registry does not carry
     */
    private static function resolver(array $responses = [], array $coresOnDisk = []): MrContextResolver
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
        ], $client, $coresOnDisk);
    }

    /**
     * @param array<string, mixed> $payload
     */
    private static function json(array $payload, int $status = 200): MockResponse
    {
        return new MockResponse(json_encode($payload, JSON_THROW_ON_ERROR), [
            'http_code' => $status,
            'response_headers' => ['content-type' => 'application/json'],
        ]);
    }

    /**
     * @return array<string, mixed>
     */
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

    /**
     * @param array<string, mixed> $overrides
     *
     * @return array<string, mixed>
     */
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

    // ------------------------------------- the core the branch actually declares

    /** A plain-text response, for the raw-file endpoint info.yml comes from. */
    private static function raw(string $body): MockResponse
    {
        return new MockResponse($body, ['http_code' => 200]);
    }

    /**
     * Checking a branch on a core it never claimed produces a failure that
     * says nothing about the module — composer refuses to resolve, and the
     * report reads as though the contribution is broken. It matters more now
     * the core can be inferred: a module the registry does not carry takes the
     * newest core built on this machine, which knows nothing about the branch.
     */
    public function testACoreTheTargetBranchDoesNotDeclareIsRefused(): void
    {
        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessageMatches('/does not include core 12.*--version=10 or --version=11/s');

        self::resolver([
            self::json(self::projectPayload()),
            self::json(self::mrPayload()),
            self::raw("name: Widget\ncore_version_requirement: ^10 || ^11\n"),
        ], ['10', '11', '12'])->resolve('paragraphs', 2, '12');
    }

    /** A core it does declare is simply used. */
    public function testACoreTheBranchDeclaresIsAccepted(): void
    {
        $context = self::resolver([
            self::json(self::projectPayload()),
            self::json(self::mrPayload()),
            self::raw("core_version_requirement: ^10 || ^11\n"),
        ], ['10', '11'])->resolve('paragraphs', 2, '11');

        self::assertSame('11', $context->coreMajor);
    }

    /**
     * Silence whenever the branch cannot be read. A missing info.yml, an
     * unparseable constraint or a closed endpoint all mean upkeep does not
     * know — and refusing on not-knowing would block work over a file it
     * merely failed to fetch.
     */
    public function testAnUnreadableOrUnparseableBranchIsNotARefusal(): void
    {
        foreach (["see the README\n", ''] as $body) {
            $context = self::resolver([
                self::json(self::projectPayload()),
                self::json(self::mrPayload()),
                self::raw($body === '' ? '' : "core_version_requirement: " . $body),
            ], ['11'])->resolve('paragraphs', 2, '11');

            self::assertSame('11', $context->coreMajor);
        }
    }

    /**
     * The branch declares cores, and none of them is built here. Naming one
     * anyway would answer a refusal with another refusal, so the suggestion
     * becomes the build.
     */
    public function testWhenNothingDeclaredIsBuiltTheSuggestionIsToBuildOne(): void
    {
        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessage('upkeep base-artifacts:build');

        self::resolver([
            self::json(self::projectPayload()),
            self::json(self::mrPayload()),
            self::raw("core_version_requirement: ^12 || ^13\n"),
        ], ['10', '11'])->resolve('paragraphs', 2, '10');
    }

    /**
     * With no cockpit to ask — no base artifacts handed in — only registered
     * modules resolve. That is the behaviour that predates the watchlist
     * split, not a new refusal: there is nothing on disk to pick a core from.
     */
    public function testWithNothingBuiltOnlyRegisteredModulesResolve(): void
    {
        $this->expectException(RegistryException::class);
        $this->expectExceptionMessage('no base artifacts');

        self::resolver()->resolve('nope_module', 1, null);
    }

    /**
     * And the merge-request path takes any module, like every other subject
     * command. This is the case that was missed: `check <module> <mr>` went
     * through resolve(), which still gated on the registry, so the headline
     * claim of the watchlist change was false for the command it was most
     * about. See docs/any-module.md.
     */
    public function testTheMergeRequestPathResolvesAnUnregisteredModule(): void
    {
        $context = self::resolver([
            self::json(self::projectPayload()),
            self::json(self::mrPayload()),
        ], ['10', '11'])->resolve('paragraphs', 2, null);

        self::assertSame('paragraphs', $context->module->name);
        self::assertSame('project/paragraphs', $context->module->project);
        self::assertSame('11', $context->coreMajor, 'the newest core built here');
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

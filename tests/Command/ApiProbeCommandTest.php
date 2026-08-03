<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\MockGitlab;
use Upkeep\Workflow\ExitCode;

/**
 * `api:probe` is the operator's "is my token working and what does GitLab
 * think of this project" surface, so what it owns is the report: which fields
 * are shown, and which API failures stop it dead versus merely degrade it.
 *
 * That distinction is the whole of its custom logic. The project lookup and
 * the MR list are prerequisites — without either there is nothing to report,
 * so both are infrastructure failures. The single-MR re-fetch is not: its only
 * extra content is the head pipeline, so when it fails the command says the
 * pipeline is unavailable, prints the listed MR's fields anyway, and still
 * exits 0.
 */
final class ApiProbeCommandTest extends TestCase
{
    private const HEAD_SHA = 'abc123def456abc123def456abc123def456abcd';

    private ?CliHarness $cli = null;

    protected function tearDown(): void
    {
        $this->cli?->destroy();
    }

    private function cli(): CliHarness
    {
        return $this->cli ??= CliHarness::create('api-probe');
    }

    /**
     * @param array<string, mixed> $overrides
     *
     * @return array<string, mixed>
     */
    private static function pipelinePayload(array $overrides = []): array
    {
        return $overrides + [
            'id' => 77,
            'status' => 'success',
            'sha' => self::HEAD_SHA,
            'web_url' => 'https://git.drupalcode.org/project/widget/-/pipelines/77',
        ];
    }

    /**
     * The full report against a healthy project: the module's identity, how
     * many MRs are open, and — from the single-MR endpoint, the only one
     * carrying it — the head pipeline.
     */
    public function testItReportsTheProjectItsOpenMrsAndTheHeadPipelineOfTheFirst(): void
    {
        $cli = $this->cli();
        $mr = MockGitlab::mergeRequestPayload('widget', 5, ['sha' => self::HEAD_SHA]);

        $cli->withGitlab(
            MockGitlab::create()
                ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget', 4242))
                ->route('/merge_requests?', [$mr])
                ->route('/merge_requests/5', $mr + ['head_pipeline' => self::pipelinePayload()])
                ->client(),
        );

        self::assertSame(ExitCode::OK, $cli->run('api:probe', 'widget'), $cli->display());

        $display = $cli->display();
        self::assertStringContainsString('project/widget (project id 4242)', $display);
        self::assertStringContainsString('Open merge requests: 1', $display);
        self::assertStringContainsString('MR !5', $display);
        self::assertStringContainsString('3489012-add-config-schema', $display);
        self::assertStringContainsString('alice (id 100)', $display);
        self::assertStringContainsString('Head pipeline: success (#77)', $display);
    }

    /**
     * A project with nothing open is a normal, successful answer — the command
     * says so and stops rather than inventing an MR to inspect.
     */
    public function testAProjectWithNoOpenMergeRequestsIsReportedAndExitsOk(): void
    {
        $cli = $this->cli();
        $cli->withGitlab(
            MockGitlab::create()
                ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget'))
                ->route('/merge_requests?', [])
                ->client(),
        );

        self::assertSame(ExitCode::OK, $cli->run('api:probe', 'widget'), $cli->display());
        self::assertStringContainsString('Open merge requests: 0', $cli->display());
        self::assertStringContainsString('No open MR to inspect further.', $cli->display());
    }

    /** An MR that has never had a pipeline is named as such, not left blank. */
    public function testAnMrWithoutAHeadPipelineSaysSoRatherThanShowingNothing(): void
    {
        $cli = $this->cli();
        $mr = MockGitlab::mergeRequestPayload('widget', 5, [
            'draft' => true,
            'detailed_merge_status' => null,
            'sha' => null,
            'author' => ['username' => 'alice'],
        ]);

        $cli->withGitlab(
            MockGitlab::create()
                ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget'))
                ->route('/merge_requests?', [$mr])
                ->route('/merge_requests/5', $mr)
                ->client(),
        );

        self::assertSame(ExitCode::OK, $cli->run('api:probe', 'widget'), $cli->display());

        $display = $cli->display();
        self::assertStringContainsString('Head pipeline: none', $display);
        // A draft with no merge status and no head commit still renders every
        // row: the report degrades to "n/a", it does not omit fields.
        self::assertStringContainsString('yes', $display);
        self::assertStringContainsString('n/a', $display);
    }

    /**
     * The degraded path: only the single-MR endpoint carries head_pipeline, so
     * when it fails the pipeline is unavailable — but everything the list
     * already told us is still worth printing, and nothing failed that the
     * operator asked for. Warning, full report, exit 0.
     */
    public function testAFailedSingleMrRefetchDegradesToTheListedMrAndStillExitsOk(): void
    {
        $cli = $this->cli();
        $cli->withGitlab(
            MockGitlab::create()
                ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget'))
                ->route('/merge_requests?', [MockGitlab::mergeRequestPayload('widget', 5)])
                ->route('/merge_requests/5', ['message' => '403 Forbidden'], 403)
                ->client(),
        );

        self::assertSame(ExitCode::OK, $cli->run('api:probe', 'widget'), $cli->display());

        $display = $cli->display();
        self::assertStringContainsString('pipeline status unavailable', $display);
        self::assertStringContainsString('MR !5', $display);
        self::assertStringContainsString('Add config schema', $display);
    }

    /**
     * @return iterable<string, array{list<array{string, array<string, mixed>, int}>, string}>
     */
    public static function fatalApiFailures(): iterable
    {
        yield 'the project cannot be looked up' => [
            [['/projects/project%2Fwidget', ['message' => '404 Project Not Found'], 404]],
            'Project lookup failed',
        ];

        yield 'the merge-request list cannot be fetched' => [
            [
                ['/projects/project%2Fwidget', MockGitlab::projectPayload('widget'), 200],
                ['/merge_requests?', ['message' => 'upstream is unwell'], 502],
            ],
            'MR list failed',
        ];
    }

    /**
     * Both prerequisites are infrastructure failures: with no project or no MR
     * list there is no report to degrade to, so the run produced no answer and
     * exits 2 naming which fetch failed.
     *
     * @param list<array{string, array<string, mixed>, int}> $routes
     */
    #[\PHPUnit\Framework\Attributes\DataProvider('fatalApiFailures')]
    public function testAFailedPrerequisiteFetchIsAnInfrastructureFailure(array $routes, string $expected): void
    {
        $cli = $this->cli();
        $gitlab = MockGitlab::create();
        foreach ($routes as [$needle, $payload, $status]) {
            $gitlab->route($needle, $payload, $status);
        }
        $cli->withGitlab($gitlab->client());

        self::assertSame(ExitCode::INFRASTRUCTURE, $cli->run('api:probe', 'widget'), $cli->display());
        self::assertStringContainsString($expected, $cli->display());
    }

    /**
     * The command takes the project path directly, without consulting the
     * registry: it is the debug surface used when a module is *not* yet
     * registered, which is exactly when the registry cannot help.
     */
    public function testAFullProjectPathIsProbedWithoutConsultingTheRegistry(): void
    {
        $cli = $this->cli();
        $gitlab = MockGitlab::create()
            ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget'))
            ->route('/merge_requests?', []);
        $cli->withGitlab($gitlab->client());

        // No registry.yml is written at all, and the module is not registered.
        self::assertSame(ExitCode::OK, $cli->run('api:probe', 'project/widget'), $cli->display());
        self::assertStringContainsString('project/widget (project id 4242)', $cli->display());
    }
}

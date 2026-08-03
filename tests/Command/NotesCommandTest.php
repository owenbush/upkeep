<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\MockGitlab;
use Upkeep\Workflow\ExitCode;

/**
 * `notes` drafts release notes onto stdout for the maintainer to paste.
 *
 * The Markdown itself is NotesGenerator's, unit-tested there. What this
 * command owns is: which project path the module name resolves to (the
 * registry when there is one, the argument itself when there is not), the
 * "since when" boundary, which fetch failures are fatal, and the promise that
 * stdout carries nothing but the paste-ready draft.
 *
 * The wiring is asserted end-to-end rather than by unit-testing the pieces
 * because the failure this file exists to prevent was a wiring failure: the
 * class once shipped with an unimported GitlabClientFactory and fataled on
 * every invocation, with every unit test still green.
 */
final class NotesCommandTest extends TestCase
{
    private ?CliHarness $cli = null;

    protected function tearDown(): void
    {
        $this->cli?->destroy();
    }

    private function cli(): CliHarness
    {
        return $this->cli ??= CliHarness::create('notes');
    }

    /**
     * @param array<string, mixed> $overrides
     *
     * @return array<string, mixed>
     */
    private static function mergedMr(int $iid, string $title, string $author, array $overrides = []): array
    {
        return $overrides + MockGitlab::mergeRequestPayload('widget', $iid, [
            'title' => $title,
            'state' => 'merged',
            'author' => ['username' => $author, 'id' => $author === 'Project-Update-Bot' ? 66574 : 100],
        ]);
    }

    /**
     * @param list<array<string, mixed>> $tags
     * @param list<array<string, mixed>> $merged
     */
    private function withHistory(array $tags, array $merged): MockGitlab
    {
        return MockGitlab::create()
            ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget'))
            ->route('/repository/tags', $tags)
            ->route('/merge_requests?', $merged);
    }

    /**
     * The draft itself: dated from the latest tag, with the bot's homogeneous
     * compatibility noise compressed away from the human changes.
     */
    public function testItDraftsNotesSinceTheLatestTagOntoStdout(): void
    {
        $cli = $this->cli();
        $cli->registerModule('widget');
        $cli->withGitlab(
            $this->withHistory(
                [
                    ['name' => '2.0.0', 'commit' => ['id' => 'aaa', 'created_at' => '2026-01-05T10:00:00Z']],
                    ['name' => '1.9.0', 'commit' => ['id' => 'bbb', 'created_at' => '2025-06-01T10:00:00Z']],
                    // Undated: cannot serve as a boundary, so it is skipped
                    // even though it sorts last alphabetically.
                    ['name' => '2.1.0-rc1', 'commit' => ['id' => 'ccc']],
                ],
                [
                    self::mergedMr(11, 'Automated Project Update Bot fixes', 'Project-Update-Bot'),
                    self::mergedMr(12, 'Issue #3489012: Add config schema', 'alice'),
                ],
            )->client(),
        );

        self::assertSame(ExitCode::OK, $cli->run('notes', 'widget'), $cli->display());

        $display = $cli->display();
        self::assertStringContainsString('## widget — since 2.0.0 (2026-01-05)', $display);
        self::assertStringContainsString('### Compatibility updates', $display);
        self::assertStringContainsString('### Changes', $display);
        self::assertStringContainsString('- Issue #3489012: Add config schema ([!12]', $display);
    }

    /**
     * A project that has never been tagged is not an error: "since the last
     * tag" becomes "everything", and the draft says so.
     */
    public function testATaglessProjectDraftsTheFullMergedHistory(): void
    {
        $cli = $this->cli();
        $cli->registerModule('widget');
        $cli->withGitlab($this->withHistory([], [self::mergedMr(3, 'Initial release work', 'alice')])->client());

        self::assertSame(ExitCode::OK, $cli->run('notes', 'widget'), $cli->display());
        self::assertStringContainsString('full merged history (no previous tag)', $cli->display());
        self::assertStringContainsString('- Initial release work ([!3]', $cli->display());
    }

    /**
     * The registry's project path wins over the bare argument, so a module
     * whose machine name and drupalcode path differ still resolves. The mock
     * only routes the namespaced path, so a command that skipped the registry
     * would fail loudly rather than silently probe the wrong project.
     */
    public function testARegisteredModuleIsFetchedUnderItsRegistryProjectPath(): void
    {
        $cli = $this->cli();
        $cli->registerModule('widget', 'project/widget_ui');

        $gitlab = MockGitlab::create()
            ->route('/projects/project%2Fwidget_ui', MockGitlab::projectPayload('widget_ui'))
            ->route('/repository/tags', [])
            ->route('/merge_requests?', []);
        $cli->withGitlab($gitlab->client());

        self::assertSame(ExitCode::OK, $cli->run('notes', 'widget'), $cli->display());
        self::assertStringContainsString('No merge requests have been merged', $cli->display());
    }

    /**
     * The one place a missing registry is deliberately not an error: `notes`
     * accepts a bare project path, so with no usable cockpit the argument
     * simply IS the project path. Every other command would exit 2 here.
     */
    public function testWithNoUsableCockpitTheArgumentIsTakenAsTheProjectPath(): void
    {
        $cli = $this->cli();
        // No registry.yml is ever written: the cockpit directory exists but
        // holds nothing.
        $cli->withGitlab($this->withHistory([], [])->client());

        self::assertSame(ExitCode::OK, $cli->run('notes', 'project/widget'), $cli->display());
        self::assertStringContainsString('## project/widget', $cli->display());
    }

    /**
     * @return iterable<string, array{list<array{string, array<string, mixed>, int}>, string}>
     */
    public static function fatalFetchFailures(): iterable
    {
        yield 'project lookup' => [
            [['/projects/project%2Fwidget', ['message' => '404 Not Found'], 404]],
            'Project lookup failed',
        ];

        yield 'tag list' => [
            [
                ['/projects/project%2Fwidget', MockGitlab::projectPayload('widget'), 200],
                ['/repository/tags', ['message' => 'nope'], 500],
            ],
            'Tag list failed',
        ];

        yield 'merged-MR list' => [
            [
                ['/projects/project%2Fwidget', MockGitlab::projectPayload('widget'), 200],
                ['/repository/tags', [], 200],
                ['/merge_requests?', ['message' => 'nope'], 500],
            ],
            'Merged-MR list failed',
        ];
    }

    /**
     * Every fetch this command makes is a prerequisite for the draft, so any
     * of them failing means no notes were produced: exit 2, naming which.
     * A partial draft would be worse than none — it would read as complete.
     *
     * @param list<array{string, array<string, mixed>, int}> $routes
     */
    #[\PHPUnit\Framework\Attributes\DataProvider('fatalFetchFailures')]
    public function testAFailedFetchProducesNoDraftAndExitsInfrastructure(array $routes, string $expected): void
    {
        $cli = $this->cli();
        $cli->registerModule('widget');

        $gitlab = MockGitlab::create();
        foreach ($routes as [$needle, $payload, $status]) {
            $gitlab->route($needle, $payload, $status);
        }
        $cli->withGitlab($gitlab->client());

        self::assertSame(ExitCode::INFRASTRUCTURE, $cli->run('notes', 'widget'), $cli->display());
        self::assertStringContainsString($expected, $cli->display());
        self::assertStringNotContainsString('## widget', $cli->display());
    }

    /**
     * No token configured is an infrastructure failure with the CLI-wide
     * wording — and the guidance goes to stderr, because this command's
     * stdout is a file the maintainer redirects. `upkeep notes widget >
     * notes.md` must never write an error message into notes.md.
     */
    public function testWithoutATokenTheGuidanceGoesToStderrAndStdoutStaysEmpty(): void
    {
        $cli = $this->cli();
        $cli->registerModule('widget');

        $exit = $cli->runSplittingStreams('notes', 'widget');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit, $cli->errorDisplay());
        self::assertStringContainsString('No GitLab token found', $cli->errorDisplay());
        self::assertSame('', $cli->display(), 'stdout carries only the paste-ready draft');
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\MockGitlab;
use Upkeep\Workflow\ExitCode;

/**
 * The last step of `issue` and `needs-work`: handing the drupal.org issue to
 * the operator's browser.
 *
 * This exists because the drupal.org API is read-only — upkeep can never set
 * an issue to RTBC or Needs work itself, so getting the human onto the right
 * page IS the feature. Both outcomes matter: when the browser takes the URL
 * the command says where to act, and when it does not the URL has to be
 * printed instead, or the operator is left with nothing to copy.
 *
 * The opener under test is the real one — Command\BrowserOpener, spawning a
 * real child process. Only the binary it finds is substituted, by a stub first
 * on $PATH, so no browser is ever launched and the platform choice, the
 * credential scrubbing and the exit-status reading all stay in the path.
 */
final class BrowserHandoffTest extends TestCase
{
    private const HEAD_SHA = 'abc123def456abc123def456abc123def456abcd';
    private const ISSUE_URL = 'https://www.drupal.org/node/3489012';

    private ?CliHarness $cli = null;

    protected function tearDown(): void
    {
        $this->cli?->destroy();
    }

    private function cli(): CliHarness
    {
        if ($this->cli === null) {
            $this->cli = CliHarness::create('browser');
            $this->cli->registerModule('widget');
        }

        return $this->cli;
    }

    /** A drupal.org client that cannot reach the API, so only the URL is known. */
    private static function unreachableDrupalOrg(): DrupalOrgClient
    {
        return new DrupalOrgClient(new MockHttpClient(new MockResponse('', ['http_code' => 500])));
    }

    private function gitlab(): MockGitlab
    {
        return MockGitlab::create()
            ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget'))
            ->route('/merge_requests/5/notes', ['id' => 1], 201)
            ->route('/merge_requests/5', MockGitlab::mergeRequestPayload('widget', 5, ['sha' => self::HEAD_SHA]));
    }

    /**
     * `issue` hands the URL over and, when the browser takes it, tells the
     * operator what to do there — the status change upkeep cannot make.
     */
    public function testIssueOpensTheLinkedIssueAndSaysWhereToChangeTheStatus(): void
    {
        $cli = $this->cli();
        $cli->withGitlab($this->gitlab()->client())
            ->withDrupalOrg(self::unreachableDrupalOrg())
            ->withBrowserOpener(true);

        self::assertSame(ExitCode::OK, $cli->run('issue', 'widget', '5'), $cli->display());

        self::assertSame([self::ISSUE_URL], $cli->openedUrls());
        self::assertStringContainsString('Opened in browser.', $cli->display());
    }

    /**
     * A headless box, an operator over SSH, no xdg-open: the handoff fails and
     * the URL is printed instead. Still exit 0 — the issue was found and
     * reported, which is what was asked for.
     */
    public function testIssuePrintsTheUrlWhenTheBrowserCannotBeLaunched(): void
    {
        $cli = $this->cli();
        $cli->withGitlab($this->gitlab()->client())
            ->withDrupalOrg(self::unreachableDrupalOrg())
            ->withBrowserOpener(false);

        self::assertSame(ExitCode::OK, $cli->run('issue', 'widget', '5'), $cli->display());

        self::assertSame([self::ISSUE_URL], $cli->openedUrls(), 'the opener is still attempted');
        self::assertStringContainsString(
            'Could not open browser automatically. Visit: ' . self::ISSUE_URL,
            $cli->display(),
        );
        self::assertStringNotContainsString('Opened in browser', $cli->display());
    }

    /** `--no-open` suppresses the handoff entirely; nothing is spawned. */
    public function testNoOpenSuppressesTheHandoffAltogether(): void
    {
        $cli = $this->cli();
        $cli->withGitlab($this->gitlab()->client())
            ->withDrupalOrg(self::unreachableDrupalOrg())
            ->withBrowserOpener(true);

        self::assertSame(ExitCode::OK, $cli->run('issue', 'widget', '5', '--no-open'), $cli->display());

        self::assertSame([], $cli->openedUrls());
        self::assertStringNotContainsString('browser', $cli->display());
    }

    /**
     * `needs-work` posts the results comment first and only then opens the
     * issue, because the comment is the evidence the status change refers to.
     * Both handoff outcomes name the status the operator has to set.
     */
    public function testNeedsWorkPostsTheCommentThenHandsTheIssueToTheBrowser(): void
    {
        $cli = $this->cli();
        $this->cacheAFailingLocalResult($cli);
        $cli->withGitlab($this->gitlab()->client())->withBrowserOpener(true);

        self::assertSame(ExitCode::OK, $cli->run('needs-work', 'widget', '5'), $cli->display());

        self::assertStringContainsString('Comment posted on !5', $cli->display());
        self::assertSame([self::ISSUE_URL], $cli->openedUrls());
        self::assertStringContainsString('set the status to Needs work', $cli->display());
    }

    public function testNeedsWorkPrintsTheIssueUrlWhenTheBrowserCannotBeLaunched(): void
    {
        $cli = $this->cli();
        $this->cacheAFailingLocalResult($cli);
        $cli->withGitlab($this->gitlab()->client())->withBrowserOpener(false);

        self::assertSame(ExitCode::OK, $cli->run('needs-work', 'widget', '5'), $cli->display());

        self::assertStringContainsString('Set the issue status at: ' . self::ISSUE_URL, $cli->display());
    }

    private function cacheAFailingLocalResult(CliHarness $cli): void
    {
        (new ResultsCache($cli->cockpit . '/results'))->store(
            'widget',
            ResultKey::mergeRequest(5),
            '11',
            self::HEAD_SHA,
            new CheckRunResult([
            new CheckResult(CheckType::PhpCs, CheckStatus::Failed, 2, 'FOUND 3 ERRORS', 1.2),
            ])
        );
    }
}

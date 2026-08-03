<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\ServeResult;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Tests\Support\MockGitlab;
use Upkeep\Workflow\ExitCode;

/**
 * `review` puts a merge request in front of a human: resolve, provision,
 * apply, serve, and print where to look.
 *
 * Its exit codes are deliberately only 0 and 2 — it runs no checks, so there
 * is no supervised work that can report failure and 1 is never returned. The
 * URLs are the whole product of a successful run, and the login URL is
 * optional because not every engine can mint one.
 */
final class ReviewCommandTest extends TestCase
{
    private ?CliHarness $cli = null;

    protected function tearDown(): void
    {
        $this->cli?->destroy();
    }

    private function cli(): CliHarness
    {
        if ($this->cli === null) {
            $this->cli = CliHarness::create('review');
            $this->cli->registerModule('widget', 'project/widget', ['11', '10']);
            $this->cli->withGitlab(
                MockGitlab::create()
                    ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget'))
                    ->route('/merge_requests/5', MockGitlab::mergeRequestPayload('widget', 5))
                    ->client(),
            );
        }

        return $this->cli;
    }

    private function environment(string $coreMajor = '11'): Environment
    {
        return new Environment(
            'widget',
            $coreMajor,
            'upkeep-widget-' . $coreMajor,
            $this->cli()->path('env-' . $coreMajor),
            'https://upkeep-widget-' . $coreMajor . '.example',
            false,
        );
    }

    /**
     * The happy path, and the two URLs that are its entire point: the site to
     * browse, and the one-time login the engine minted for it.
     */
    public function testItServesTheMrAndPrintsTheSiteAndOneTimeLoginUrls(): void
    {
        $cli = $this->cli();
        $cli->withEngine(FakeEngineAdapter::withServeResult(
            $this->environment(),
            new ServeResult('https://widget-11.example', 'https://widget-11.example/user/reset/one-time'),
        ));

        self::assertSame(ExitCode::OK, $cli->run('review', 'widget', '5'), $cli->display());

        $display = $cli->display();
        self::assertStringContainsString('for review on Drupal core 11.', $display);
        self::assertStringContainsString('Site URL:   https://widget-11.example', $display);
        self::assertStringContainsString(
            'Login URL:  https://widget-11.example/user/reset/one-time  (one-time)',
            $display,
        );
        // The default target core is the first version in the registry entry,
        // and the run says so rather than leaving the operator to guess.
        self::assertStringContainsString('default: first tracked core version', $display);
    }

    /**
     * An engine that cannot mint a login URL is not a failure: the site is
     * still up and browsable, so the run succeeds and simply omits the line
     * rather than printing an empty one.
     */
    public function testAnEngineThatCannotMintALoginUrlStillSucceedsAndOmitsTheLine(): void
    {
        $cli = $this->cli();
        $cli->withEngine(FakeEngineAdapter::withServeResult(
            $this->environment('10'),
            new ServeResult('https://widget-10.example', null),
        ));

        self::assertSame(ExitCode::OK, $cli->run('review', 'widget', '5', '--version=10'), $cli->display());

        $display = $cli->display();
        self::assertStringContainsString('Site URL:   https://widget-10.example', $display);
        self::assertStringNotContainsString('Login URL', $display);
        // An explicitly requested core is not annotated as the default.
        self::assertStringNotContainsString('default: first tracked core version', $display);
    }

    /**
     * An engine that cannot provision is an infrastructure failure: no site
     * was served, so nothing was reviewed. 1 is never returned by this
     * command — there is no supervised work to fail.
     */
    public function testAnEngineFailureIsAnInfrastructureFailureNotACheckFailure(): void
    {
        $cli = $this->cli();
        $cli->withEngine(FakeEngineAdapter::failing(new AdapterException('base artifacts for core 11 are missing')));

        $exit = $cli->run('review', 'widget', '5');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit, $cli->display());
        self::assertStringContainsString('base artifacts for core 11 are missing', $cli->display());
    }

    /** A core version the registry does not track is refused before provisioning. */
    public function testAnUntrackedCoreVersionIsRefusedBeforeTheEngineIsAskedForAnything(): void
    {
        $cli = $this->cli();
        $cli->withEngine(FakeEngineAdapter::withServeResult(
            $this->environment(),
            new ServeResult('https://widget-11.example', null),
        ));

        $exit = $cli->run('review', 'widget', '5', '--version=9');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit, $cli->display());
        self::assertStringContainsString('does not track core version', $cli->display());
        self::assertStringNotContainsString('Site URL', $cli->display());
    }
}

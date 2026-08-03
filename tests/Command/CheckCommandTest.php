<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Adapter\Environment;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Tests\Support\MockGitlab;
use Upkeep\Workflow\ExitCode;

/**
 * `check` beyond its exit codes (those are ExitCodeContractTest's).
 *
 * What is asserted here is what the command does around the check run: the
 * optional fixture load and its failure mode, how a run is reported when a
 * check could not execute at all, and the caching rule — results are keyed by
 * the MR's head SHA, so an MR with no head SHA must not be cached at all.
 * That last one is the interesting case: a cache entry no staleness check
 * could ever match would make the dashboard show a green LOCAL cell forever.
 */
final class CheckCommandTest extends TestCase
{
    private const HEAD_SHA = 'abc123def456abc123def456abc123def456abcd';

    private ?CliHarness $cli = null;

    protected function tearDown(): void
    {
        $this->cli?->destroy();
    }

    private function cli(): CliHarness
    {
        if ($this->cli === null) {
            $this->cli = CliHarness::create('check');
            $this->cli->registerModule('widget');
        }

        return $this->cli;
    }

    /** @param array<string, mixed> $mrOverrides */
    private function withMr(array $mrOverrides = []): CliHarness
    {
        $cli = $this->cli();
        $cli->withGitlab(
            MockGitlab::create()
                ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget'))
                ->route('/merge_requests/5', MockGitlab::mergeRequestPayload(
                    'widget',
                    5,
                    $mrOverrides + ['sha' => self::HEAD_SHA],
                ))
                ->client(),
        );

        return $cli;
    }

    private function environment(): Environment
    {
        return new Environment('widget', '11', 'upkeep-widget-11', $this->cli()->path('env'), 'https://x', false);
    }

    private static function greenRun(): CheckRunResult
    {
        return new CheckRunResult([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.5)]);
    }

    /**
     * A named fixture is loaded into the database before the checks run, and
     * the results are cached under the (module, MR, core, head SHA) key the
     * dashboard and the fast-lane gate look them up by.
     */
    public function testANamedFixtureIsLoadedBeforeTheChecksAndTheRunIsCachedByHeadSha(): void
    {
        $cli = $this->withMr();
        $engine = FakeEngineAdapter::withCheckRun($this->environment(), self::greenRun());
        $cli->withEngine($engine);

        self::assertSame(ExitCode::OK, $cli->run('check', 'widget', '5', '--fixture=demo-content'), $cli->display());

        self::assertSame(['demo-content'], $engine->loadedFixtures);
        self::assertStringContainsString('Fixture', $cli->display());
        self::assertFileExists($cli->cockpit . '/results/widget/5/11/' . self::HEAD_SHA . '.json');
        self::assertStringContainsString('Results cached:', $cli->display());
    }

    /** With no --fixture the engine is never asked to load one. */
    public function testWithoutTheFixtureOptionNoFixtureIsLoaded(): void
    {
        $cli = $this->withMr();
        $engine = FakeEngineAdapter::withCheckRun($this->environment(), self::greenRun());
        $cli->withEngine($engine);

        self::assertSame(ExitCode::OK, $cli->run('check', 'widget', '5'), $cli->display());
        self::assertSame([], $engine->loadedFixtures);
    }

    /**
     * The option's help text promises the run aborts before any check when the
     * fixture is unknown — a check run against the wrong database state would
     * produce a verdict about nothing.
     */
    public function testAnUnknownFixtureAbortsBeforeAnyCheckRuns(): void
    {
        $cli = $this->withMr();
        // runChecks() is unconfigured on this double, so reaching it would
        // raise a BadMethodCallException rather than quietly pass.
        $cli->withEngine(FakeEngineAdapter::withFailingFixtureLoad(
            $this->environment(),
            new AdapterException('Unknown fixture "nope": no such dump in the fixture library.'),
        ));

        $exit = $cli->run('check', 'widget', '5', '--fixture=nope');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit, $cli->display());
        self::assertStringContainsString('Unknown fixture "nope"', $cli->display());
        self::assertStringNotContainsString('All checks green', $cli->display());
        self::assertDirectoryDoesNotExist($cli->cockpit . '/results/widget');
    }

    /**
     * A check the engine could not run at all is reported by name as
     * unavailable. It is not a failure — the exit code stays 0 — but it must
     * not read as a pass either, because nothing was verified.
     */
    public function testACheckThatCouldNotRunIsReportedAsUnavailableWithoutFailingTheRun(): void
    {
        $cli = $this->withMr();
        $cli->withEngine(FakeEngineAdapter::withCheckRun($this->environment(), new CheckRunResult([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.5),
            new CheckResult(CheckType::PhpStan, CheckStatus::Unavailable, null, '', 0.0),
        ])));

        self::assertSame(ExitCode::OK, $cli->run('check', 'widget', '5'), $cli->display());

        $display = $cli->display();
        self::assertStringContainsString('unavailable', $display);
        // No exit code to show for a check that never ran.
        self::assertMatchesRegularExpression('/phpstan\s+\|?\s*unavailable\s+\|?\s*-/', $display);
        self::assertStringContainsString('All checks green', $display);
    }

    /**
     * The cache key is (module, MR, core, head SHA). An MR GitLab reports
     * without a head SHA therefore has no key: caching it would store an entry
     * that no freshness check could ever match, so the dashboard would show
     * its LOCAL cell as fresh forever. The checks still ran and their verdict
     * is still the exit code — only the caching is skipped, loudly.
     */
    public function testAnMrWithNoHeadShaIsNotCachedAndSaysWhy(): void
    {
        $cli = $this->withMr(['sha' => null]);
        $cli->withEngine(FakeEngineAdapter::withCheckRun($this->environment(), self::greenRun()));

        self::assertSame(ExitCode::OK, $cli->run('check', 'widget', '5'), $cli->display());

        $display = $cli->display();
        self::assertStringContainsString('results were NOT cached', $display);
        self::assertStringNotContainsString('Results cached:', $display);
        self::assertDirectoryDoesNotExist($cli->cockpit . '/results/widget');
    }
}

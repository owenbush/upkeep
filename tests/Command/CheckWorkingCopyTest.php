<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\WorkingCopyStatus;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Workflow\ExitCode;

/**
 * `upkeep check <module> --working-copy` — the suite against whatever you are
 * on right now.
 *
 * It exists because the two ways of getting work into an environment — `start`
 * and `patch:promote` — both leave you on a branch no remote has heard of, and
 * every checking verb wanted a merge request IID. `start` had been closing with
 * "upkeep check <module> --branch" since it was written, naming a flag that was
 * never built.
 *
 * The load-bearing property is what it does *not* do: it caches nothing. A
 * cached verdict is keyed by a subject and a revision so staleness is
 * detectable; a working copy has neither, and an entry keyed on a guess would
 * put permanently-fresh-looking evidence in front of the fast-lane gate.
 */
final class CheckWorkingCopyTest extends TestCase
{
    private ?CliHarness $cli = null;

    protected function tearDown(): void
    {
        $this->cli?->destroy();
    }

    private function cli(): CliHarness
    {
        if ($this->cli === null) {
            $this->cli = CliHarness::create('wc');
            $this->cli->registerModule('widget');
        }

        return $this->cli;
    }

    private static function environment(): Environment
    {
        return new Environment(
            moduleName: 'widget',
            coreMajor: '11',
            projectName: 'widget-11',
            projectPath: '/tmp/projects/widget-11',
            primaryUrl: 'https://widget-11.ddev.site',
            reused: false,
        );
    }

    private static function greenRun(): CheckRunResult
    {
        return new CheckRunResult([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'ok', 1.0),
        ]);
    }

    private static function redRun(): CheckRunResult
    {
        return new CheckRunResult([
            new CheckResult(CheckType::PhpCs, CheckStatus::Failed, 1, 'widget.module line 12: bad spacing', 0.5),
        ]);
    }

    private static function onBranch(string $branch, bool $dirty = false): WorkingCopyStatus
    {
        return new WorkingCopyStatus(
            hasStagedChanges: false,
            hasUnstagedChanges: $dirty,
            hasUntrackedFiles: false,
            commitsAhead: 0,
            currentBranch: $branch,
        );
    }

    private function engine(CheckRunResult $run, ?WorkingCopyStatus $status): FakeEngineAdapter
    {
        $engine = FakeEngineAdapter::withCheckRun(self::environment(), $run);
        $engine->workingCopy = $status;

        return $engine;
    }

    // ----------------------------------------------------------- happy path

    public function testItRunsTheSuiteAgainstTheCurrentBranchAndNamesIt(): void
    {
        $engine = $this->engine(self::greenRun(), self::onBranch('3597808-fix-the-widget'));
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('check', 'widget', '--working-copy', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertStringContainsString('3597808-fix-the-widget', $cli->display());
        self::assertStringContainsString('All checks green', $cli->display());
    }

    /**
     * The whole point of the mode: no merge request is fetched, so it works on
     * a branch no remote has heard of — and, incidentally, with no token.
     */
    public function testNoMergeRequestIsApplied(): void
    {
        $engine = $this->engine(self::greenRun(), self::onBranch('3597808-fix-the-widget'));

        $this->cli()->withEngine($engine)->run('check', 'widget', '--working-copy', '--version=11');

        self::assertSame([], $engine->appliedMrs);
        self::assertSame([], $engine->appliedPatches);
    }

    /**
     * Nothing is cached, and the run says so rather than leaving the operator
     * to wonder why the dashboard did not move.
     */
    public function testNothingIsCachedAndTheRunSaysSo(): void
    {
        $engine = $this->engine(self::greenRun(), self::onBranch('3597808-fix-the-widget'));
        $cli = $this->cli()->withEngine($engine);

        $cli->run('check', 'widget', '--working-copy', '--version=11');

        self::assertStringContainsString('Nothing was cached', $cli->display());
        // The MR mode prints the path it wrote; this mode has nothing to print
        // because it wrote nothing.
        self::assertStringNotContainsString('Results cached', $cli->display());
    }

    /** A failed check is still a verdict, so the exit code is the normal one. */
    public function testAFailedCheckExitsOne(): void
    {
        $engine = $this->engine(self::redRun(), self::onBranch('3597808-fix-the-widget'));
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('check', 'widget', '--working-copy', '--version=11');

        self::assertSame(ExitCode::FAILED, $exit);
        self::assertStringContainsString('bad spacing', $cli->display());
    }

    // ------------------------------------------------------------- reporting

    /**
     * A dirty tree is checkable but not reportable — the run describes a state
     * that exists only in that moment, and the first thing anybody does with a
     * green result is act on it.
     */
    public function testUncommittedChangesAreCalledOut(): void
    {
        $engine = $this->engine(self::greenRun(), self::onBranch('3597808-fix-the-widget', dirty: true));
        $cli = $this->cli()->withEngine($engine);

        $cli->run('check', 'widget', '--working-copy', '--version=11');

        self::assertStringContainsString('Uncommitted changes are included', $cli->display());
    }

    public function testADetachedHeadIsStillCheckedAndSaidSo(): void
    {
        $engine = $this->engine(self::greenRun(), new WorkingCopyStatus(false, false, false, 0, null));
        $cli = $this->cli()->withEngine($engine);

        self::assertSame(ExitCode::OK, $cli->run('check', 'widget', '--working-copy', '--version=11'));
        self::assertStringContainsString('detached HEAD', $cli->display());
    }

    /** No environment yet is a 2, and names the command that makes one. */
    public function testAnUnreadableWorkingCopyIsRefusedWithSomethingToDo(): void
    {
        $engine = $this->engine(self::greenRun(), null);
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('check', 'widget', '--working-copy', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('upkeep start widget', $cli->display());
    }

    // ------------------------------------------------------------- refusals

    /**
     * Console can say "required" and "optional" but not "exactly one of
     * these", so both empty and both-given are refused here — guessing either
     * way would run a different check than the one asked for.
     */
    public function testGivingBothAnIidAndWorkingCopyIsRefused(): void
    {
        $engine = $this->engine(self::greenRun(), self::onBranch('x'));
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('check', 'widget', '12', '--working-copy', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('not both', $cli->display());
        self::assertSame([], $engine->appliedMrs);
    }

    public function testGivingNeitherIsRefusedAndNamesBothWays(): void
    {
        $cli = $this->cli()->withEngine($this->engine(self::greenRun(), self::onBranch('x')));

        $exit = $cli->run('check', 'widget', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('--working-copy', $cli->display());
        self::assertStringContainsString('<mr>', $cli->display());
    }

    /** The fixture option belongs to both modes. */
    public function testAFixtureIsLoadedBeforeTheChecks(): void
    {
        $engine = $this->engine(self::greenRun(), self::onBranch('3597808-fix-the-widget'));
        $cli = $this->cli()->withEngine($engine);

        $cli->run('check', 'widget', '--working-copy', '--version=11', '--fixture=sample');

        self::assertSame(['sample'], $engine->loadedFixtures);
    }
}

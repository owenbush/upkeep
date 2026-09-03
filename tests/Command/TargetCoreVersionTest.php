<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Adapter\Environment;
use Upkeep\Dashboard\DashboardCache;
use Upkeep\Dashboard\ModuleSnapshot;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Tests\Support\MockGitlab;
use Upkeep\Workflow\ExitCode;

/**
 * What `--version` means on check, review and dashboard.
 *
 * It is the *target Drupal core* selector — which tracked core major the
 * command acts against — and never the application's own version. Symfony
 * Console reserves that flag for printing the app version, and does so from
 * Application::doRun() before any command runs; `bin/upkeep` disables that
 * hijack (Command\VersionOptionInput) precisely so these commands can own
 * the name. That is why every case here goes through argv: an ArrayInput
 * would take a different path and prove nothing about the real invocation.
 */
final class TargetCoreVersionTest extends TestCase
{
    private const HEAD_SHA = 'abcdefabcdefabcdefabcdefabcdefabcdefabcd';

    private CliHarness $cli;

    protected function setUp(): void
    {
        $this->cli = CliHarness::create('target-core');
    }

    protected function tearDown(): void
    {
        $this->cli->destroy();
    }

    /**
     * The flag names a tracked core version and the run targets it — asserted
     * where it is observable: in the reported target and in the core segment
     * of the results-cache key the dashboard later reads.
     */
    public function testTheFlagSelectsWhichTrackedCoreVersionTheRunTargets(): void
    {
        $this->cli->registerModule('widget', coreVersions: ['10', '11']);
        $this->cli->withGitlab($this->gitlabWithOpenMr());
        $this->cli->withEngine(FakeEngineAdapter::withCheckRun($this->environment('11'), self::greenRun()));

        $exit = $this->cli->run('check', 'widget', '5', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $this->cli->display());
        $display = $this->cli->display();
        self::assertStringContainsString('Target: Drupal core 11, module widget', $display);
        self::assertStringContainsString('/results/widget/5/11/', $display);
        self::assertStringNotContainsString('Upkeep dev', $display);
    }

    /**
     * Omitting it targets the first core version in the module's registry
     * entry — the registry order is the maintainer's priority order, so the
     * default is data, not the highest number.
     */
    public function testOmittingTheFlagTargetsTheFirstCoreVersionTheRegistryLists(): void
    {
        $this->cli->registerModule('widget', coreVersions: ['10', '11']);
        $this->cli->withGitlab($this->gitlabWithOpenMr());
        $this->cli->withEngine(FakeEngineAdapter::withCheckRun($this->environment('10'), self::greenRun()));

        $exit = $this->cli->run('check', 'widget', '5');

        self::assertSame(ExitCode::OK, $exit, $this->cli->display());
        $display = $this->cli->display();
        self::assertStringContainsString('Target: Drupal core 10 (default:', $display);
        self::assertStringContainsString('/results/widget/5/10/', $display);
    }

    /**
     * The one assertion that would have caught a regression to the Symfony
     * default: on all three commands `--version` reaches the command and
     * carries the core meaning, and none of them prints the application
     * version and exits 0.
     *
     * "9" is untracked by the module on purpose, so each command has to have
     * read the value to answer at all — and check/review must reject it
     * before any request leaves the process, which the mock asserts.
     */
    public function testTheFlagIsTheTargetCoreSelectorAndNeverTheApplicationVersion(): void
    {
        $this->cli->registerModule('widget', coreVersions: ['10', '11']);
        $gitlab = MockGitlab::create();
        $this->cli->withGitlab($gitlab->client());
        $this->cacheDashboardSnapshot();

        foreach (['check', 'review'] as $command) {
            $exit = $this->cli->run($command, 'widget', '5', '--version=9');

            self::assertSame(ExitCode::INFRASTRUCTURE, $exit, $command);
            self::assertStringContainsString('does not track core version', $this->cli->display(), $command);
            self::assertStringNotContainsString('Upkeep dev', $this->cli->display(), $command);
        }

        self::assertSame([], $gitlab->requests, 'an untracked core is rejected before any GitLab call');

        // On the dashboard the same flag filters the assembled rows, so an
        // untracked core is an empty table rather than a failure.
        $exit = $this->cli->run('dashboard', '--version=9');

        self::assertSame(ExitCode::OK, $exit, $this->cli->display());
        self::assertStringContainsString('targeting core 9', $this->cli->display());
        self::assertStringNotContainsString('Upkeep dev', $this->cli->display());
    }

    /**
     * On the dashboard the flag narrows the *evidence*, not the rows.
     *
     * Core stopped being part of a row's identity — a module branch supports
     * several cores at once — so filtering by one cannot remove rows. It says
     * which core the row reports on, which is visible in the LOCAL cell and in
     * the command the row hands you.
     */
    public function testTheDashboardEvidenceNarrowsToTheRequestedCore(): void
    {
        $this->cli->registerModule('widget', coreVersions: ['10', '11']);
        $this->cacheDashboardSnapshot();

        self::assertSame(ExitCode::OK, $this->cli->run('dashboard', 'widget'));
        $unfiltered = $this->cli->display();
        self::assertStringContainsString('widget', $unfiltered);
        // Nothing checked on either core, so the row points at the first that
        // needs attention.
        self::assertStringContainsString('--version=10', $unfiltered);

        self::assertSame(ExitCode::OK, $this->cli->run('dashboard', 'widget', '--version=11'));
        $filtered = $this->cli->display();
        self::assertStringContainsString('widget', $filtered);
        self::assertStringContainsString('--version=11', $filtered);
        self::assertStringNotContainsString('--version=10', $filtered);
    }

    /**
     * base-artifacts:build has no module and therefore no registry entry to
     * take a default from, so its --version is required. It was spelled
     * --core until the CLI settled on one name for "which core", which makes
     * this the end-to-end guard on that rename.
     */
    public function testBaseArtifactsBuildRequiresTheSameVersionFlagAndNotTheOldCoreFlag(): void
    {
        $this->cli->registerModule('widget');

        $exit = $this->cli->run('base-artifacts:build');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('--version', $this->cli->display());
        self::assertStringNotContainsString('--core', $this->cli->display());
    }

    // ------------------------------------------------------------- fixtures

    private function gitlabWithOpenMr(): \Upkeep\Gitlab\GitlabClient
    {
        return MockGitlab::create()
            ->route('/projects/project%2Fwidget', MockGitlab::projectPayload('widget'))
            ->route('/merge_requests/5', MockGitlab::mergeRequestPayload('widget', 5, ['sha' => self::HEAD_SHA]))
            ->client();
    }

    private function environment(string $coreMajor): Environment
    {
        return new Environment(
            'widget',
            $coreMajor,
            'upkeep-widget-' . $coreMajor,
            $this->cli->path('projects/upkeep-widget-' . $coreMajor),
            'https://upkeep-widget-' . $coreMajor . '.ddev.site',
            true,
        );
    }

    private static function greenRun(): CheckRunResult
    {
        return new CheckRunResult([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK (3 tests)', 1.5),
        ]);
    }

    /**
     * A dashboard snapshot for widget, so the dashboard renders rows without
     * a token and without touching the network.
     */
    private function cacheDashboardSnapshot(): void
    {
        (new DashboardCache($this->cli->cockpit . '/cache/dashboard'))->save('widget', new ModuleSnapshot(
            new \DateTimeImmutable(),
            MockGitlab::projectPayload('widget'),
            [MockGitlab::mergeRequestPayload('widget', 5, [
                'title' => 'Automated bot fixes',
                'sha' => self::HEAD_SHA,
            ])],
            [],
        ));
    }
}

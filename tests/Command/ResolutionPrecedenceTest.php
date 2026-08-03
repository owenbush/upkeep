<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\RecordingEngineAdapterFactory;
use Upkeep\Workflow\ExitCode;

/**
 * Where upkeep looks for its two roots, driven through the console.
 *
 * Both rules are documented conventions a user's shell profile depends on,
 * and both are precedence rules rather than lookups: it is not enough that
 * each source works in isolation, the *order* is the contract. Every case
 * below therefore populates more than one source and asserts which one won.
 */
final class ResolutionPrecedenceTest extends TestCase
{
    private CliHarness $cli;

    protected function setUp(): void
    {
        // The harness redirects $HOME, $XDG_CONFIG_HOME and every UPKEEP_*
        // variable at temporary paths, and restores them in tearDown(), so
        // nothing here can read or write the real ~/.upkeep or ~/.config.
        $this->cli = CliHarness::create('resolution');
    }

    protected function tearDown(): void
    {
        $this->cli->destroy();
    }

    // ------------------------------------------------------------- cockpit

    /**
     * `--cockpit` > $UPKEEP_COCKPIT > cwd, asserted with all three populated
     * and pointing at registries whose module names differ, so the winner is
     * identified rather than merely "a cockpit was found".
     */
    public function testCockpitResolutionPrefersTheFlagOverTheEnvironmentOverTheWorkingDirectory(): void
    {
        $flagged = $this->cockpitContaining('flagged', 'flag_module');
        $fromEnv = $this->cockpitContaining('env', 'env_module');
        $cwd = $this->cockpitContaining('cwd', 'cwd_module');

        $this->cli->setEnv('UPKEEP_COCKPIT', $fromEnv);
        $this->cli->inDirectory($cwd);

        self::assertSame(ExitCode::OK, $this->cli->run('modules', '--cockpit=' . $flagged));
        self::assertStringContainsString('flag_module', $this->cli->display());

        self::assertSame(ExitCode::OK, $this->cli->run('modules'));
        self::assertStringContainsString('env_module', $this->cli->display());

        $this->cli->setEnv('UPKEEP_COCKPIT', null);
        self::assertSame(ExitCode::OK, $this->cli->run('modules'));
        self::assertStringContainsString('cwd_module', $this->cli->display());
    }

    /**
     * `--cockpit=` is a mistake worth naming rather than a silent fallback to
     * the next source: an empty value in a wrapper script would otherwise
     * operate on whatever directory the user happened to be in.
     */
    public function testAnEmptyCockpitFlagIsRefusedRatherThanFallingBackToTheNextSource(): void
    {
        $this->cli->setEnv('UPKEEP_COCKPIT', $this->cockpitContaining('env', 'env_module'));

        self::assertSame(ExitCode::INFRASTRUCTURE, $this->cli->run('modules', '--cockpit='));
        self::assertStringContainsString('empty --cockpit', $this->cli->display());
        self::assertStringNotContainsString('env_module', $this->cli->display());
    }

    // ------------------------------------------------------- projects root

    /**
     * `--projects-root` > $UPKEEP_PROJECTS_ROOT > <cockpit>/projects (when it
     * exists) > ~/.upkeep/projects — as resolved for a real invocation and
     * handed to the engine factory.
     *
     * The middle branch is not in the CLAUDE.md one-liner but is in the
     * code and in the option's own help text, so it is asserted here: a test
     * claiming "$UPKEEP_PROJECTS_ROOT, else the home default" would be
     * describing behaviour the tool does not have.
     */
    public function testProjectsRootResolutionPrefersTheFlagOverTheEnvironmentOverTheCockpitOverTheHomeDefault(): void
    {
        $factory = new RecordingEngineAdapterFactory();
        $this->cli->withEngineFactory($factory);
        $this->cli->registerModule('widget');

        $flagged = $this->cli->makeDirectory('flagged-projects');
        $fromEnv = $this->cli->makeDirectory('env-projects');

        $this->cli->setEnv('UPKEEP_PROJECTS_ROOT', $fromEnv);

        $this->cli->run('env:path', 'widget', '--projects-root=' . $flagged);
        self::assertSame($flagged, $factory->projectsRoot);

        $this->cli->run('env:path', 'widget');
        self::assertSame($fromEnv, $factory->projectsRoot);

        $this->cli->setEnv('UPKEEP_PROJECTS_ROOT', null);
        $inCockpit = $this->cli->makeDirectory('cockpit/projects');
        $this->cli->run('env:path', 'widget');
        self::assertSame($inCockpit, $factory->projectsRoot);

        rmdir($inCockpit);
        $this->cli->run('env:path', 'widget');
        self::assertSame($this->cli->home . '/.upkeep/projects', $factory->projectsRoot);
    }

    /**
     * The containment rule applies to the explicit sources exactly as it
     * applies to the default: a projects root outside $HOME cannot be
     * bind-mounted by the macOS Docker providers, so it is refused up front
     * instead of failing minutes into a provision.
     */
    public function testAProjectsRootOutsideHomeIsRefusedAsAnInfrastructureFailure(): void
    {
        $this->cli->withEngineFactory(new RecordingEngineAdapterFactory());
        $this->cli->registerModule('widget');

        $outside = sys_get_temp_dir();
        $exit = $this->cli->run('env:path', 'widget', '--projects-root=' . $outside);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('Refusing the projects root', $this->cli->display());
    }

    /**
     * A cockpit of its own, holding one distinctly-named module so the
     * `modules` table identifies which cockpit was resolved.
     */
    private function cockpitContaining(string $directory, string $moduleName): string
    {
        $root = $this->cli->makeDirectory('cockpits/' . $directory);
        file_put_contents(
            $root . '/registry.yml',
            sprintf("modules:\n  %s:\n    project: project/%s\n    core_versions: ['11']\n", $moduleName, $moduleName),
        );

        return $root;
    }
}

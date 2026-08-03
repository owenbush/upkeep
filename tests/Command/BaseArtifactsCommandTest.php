<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Upkeep\Tests\BaseArtifact\FakeCommandRunner;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Workflow\ExitCode;

/**
 * The base-artifact pair, driven through the console.
 *
 * The build orchestration itself is BaseArtifactBuilder's, unit-tested against
 * the same shell-out seam these runs inject. What the commands own is the
 * part in between: the required `--version`, the $HOME containment rule the
 * `--scratch-dir` help text promises, the translation of the layout's
 * "that is not a core major" into the CLI's exit-code contract, and what
 * `status` reports about an artifact set that is on disk but not finished.
 */
final class BaseArtifactsCommandTest extends TestCase
{
    private ?CliHarness $cli = null;

    protected function tearDown(): void
    {
        $this->cli?->destroy();
    }

    /** A cockpit with a parseable registry, which both commands require. */
    private function cli(): CliHarness
    {
        if ($this->cli === null) {
            $this->cli = CliHarness::create('base-artifacts');
            $this->cli->registerModule('widget');
        }

        return $this->cli;
    }

    // ------------------------------------------------------------- build

    /**
     * The whole build, against a scripted shell-out seam: it lands the
     * canonical tree, the dump, the meta sidecar and the never-auto-prune
     * marker under the cockpit, and reports what it resolved.
     */
    public function testABuildLandsTheArtifactSetInTheCockpitAndReportsWhatItResolved(): void
    {
        $cli = $this->cli();
        $cli->withCommandRunner(new FakeCommandRunner());

        self::assertSame(ExitCode::OK, $cli->run('base-artifacts:build', '--version=11'), $cli->display());

        $versionDir = $cli->cockpit . '/base-artifacts/11';
        self::assertDirectoryExists($versionDir . '/tree');
        self::assertFileExists($versionDir . '/clean-install.sql.gz');
        self::assertFileExists($versionDir . '/meta.yml');
        self::assertFileExists($versionDir . '/canonical');
        self::assertStringContainsString('core 11.4.4', $cli->display());
        self::assertStringContainsString('PHP 8.3.30', $cli->display());
    }

    /**
     * `--version` is what the build is *for*; without it there is no artifact
     * set to build, so it is refused as bad usage rather than defaulted.
     */
    public function testABuildWithoutAVersionIsRefusedAsBadUsage(): void
    {
        $cli = $this->cli();
        $cli->withCommandRunner(new FakeCommandRunner());

        self::assertSame(ExitCode::INFRASTRUCTURE, $cli->run('base-artifacts:build'), $cli->display());
        self::assertStringContainsString('--version option is required', $cli->display());
    }

    /**
     * The selector is a core *major*. A dotted version reaches the layout,
     * which refuses it with an \InvalidArgumentException — a type the console
     * base does not map, so the command translates it into the contract
     * rather than letting it escape as an unhandled error.
     */
    public function testADottedCoreVersionIsRefusedThroughTheExitCodeContract(): void
    {
        $cli = $this->cli();
        $cli->withCommandRunner(new FakeCommandRunner());

        $exit = $cli->run('base-artifacts:build', '--version=11.1');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit, $cli->display());
        self::assertStringContainsString('whole major version number', $cli->display());
    }

    /**
     * The `--scratch-dir` help text promises a path the Docker provider
     * mounts, and macOS providers mount only $HOME. The promise is enforced,
     * not merely documented: a scratch directory outside $HOME is refused
     * before a single command is issued.
     */
    public function testAScratchDirectoryOutsideHomeIsRefusedBeforeAnythingRuns(): void
    {
        $cli = $this->cli();
        $runner = new FakeCommandRunner();
        $cli->withCommandRunner($runner);

        $exit = $cli->run('base-artifacts:build', '--version=11', '--scratch-dir=' . sys_get_temp_dir());

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit, $cli->display());
        self::assertStringContainsString('outside your home directory', $cli->display());
        self::assertSame([], $runner->commands(), 'nothing may be executed once the scratch dir is refused');
        self::assertDirectoryDoesNotExist($cli->cockpit . '/base-artifacts/11');
    }

    /**
     * An existing version directory is the tool's "this build completed"
     * marker, so rebuilding over it has to be asked for deliberately.
     */
    public function testAnExistingArtifactSetIsRefusedUntilForceIsGiven(): void
    {
        $cli = $this->cli();
        $cli->withCommandRunner(new FakeCommandRunner());

        self::assertSame(ExitCode::OK, $cli->run('base-artifacts:build', '--version=11'), $cli->display());
        self::assertSame(
            ExitCode::INFRASTRUCTURE,
            $cli->run('base-artifacts:build', '--version=11'),
            $cli->display(),
        );
        self::assertStringContainsString('--force', $cli->display());

        self::assertSame(
            ExitCode::OK,
            $cli->run('base-artifacts:build', '--version=11', '--force'),
            $cli->display(),
        );
    }

    // ------------------------------------------------------------ status

    /**
     * With nothing built, the status is not an error — it is the hint that
     * names the command that would fix it, in the `--version=N` spelling the
     * build actually accepts.
     */
    public function testStatusOnAnEmptyCockpitPointsAtTheBuildCommand(): void
    {
        $cli = $this->cli();

        self::assertSame(ExitCode::OK, $cli->run('base-artifacts:status'), $cli->display());
        self::assertStringContainsString('No base artifacts built yet', $cli->display());
        self::assertStringContainsString('base-artifacts:build --version=N', $cli->display());
    }

    /**
     * A finished set reads as canonical; a half-built one names what is
     * missing instead of being reported as usable. Downstream environments
     * seed from these trees, so "incomplete" has to be visible.
     */
    public function testStatusDistinguishesACompleteArtifactSetFromAHalfBuiltOne(): void
    {
        $cli = $this->cli();
        $cli->withCommandRunner(new FakeCommandRunner());
        self::assertSame(ExitCode::OK, $cli->run('base-artifacts:build', '--version=11'), $cli->display());

        // A version directory holding nothing at all: on disk, but finished
        // by no build.
        $cli->makeDirectory('cockpit/base-artifacts/10');

        self::assertSame(ExitCode::OK, $cli->run('base-artifacts:status'), $cli->display());

        $display = $cli->display();
        self::assertStringContainsString('complete (canonical)', $display);
        self::assertStringContainsString('11.4.4', $display);
        self::assertStringContainsString('mariadb:10.11', $display);
        self::assertStringContainsString('incomplete: missing', $display);
        self::assertStringContainsString('meta.yml', $display);
    }

    /**
     * Both commands parse the registry rather than merely stat-ing the
     * cockpit, so a registry that does not parse fails them the same way it
     * fails every other command (behaviour change 5).
     */
    public function testAMalformedRegistryFailsBothCommands(): void
    {
        $cli = $this->cli();
        $cli->writeRegistry("modules:\n  widget: [\n");

        self::assertSame(ExitCode::INFRASTRUCTURE, $cli->run('base-artifacts:status'), $cli->display());
        self::assertSame(
            ExitCode::INFRASTRUCTURE,
            $cli->run('base-artifacts:build', '--version=11'),
            $cli->display(),
        );
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Cockpit;

use PHPUnit\Framework\TestCase;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Filesystem\FilesystemException;

final class CockpitTest extends TestCase
{
    private string $world;

    private string|false $originalEnv;

    protected function setUp(): void
    {
        $this->world = (string) realpath(sys_get_temp_dir()) . '/upkeep-cockpit-' . bin2hex(random_bytes(4));
        mkdir($this->world . '/cockpit/nested', 0o700, true);
        mkdir($this->world . '/elsewhere', 0o700, true);
        $this->originalEnv = getenv(Cockpit::ENV_VAR);
    }

    protected function tearDown(): void
    {
        putenv($this->originalEnv === false ? Cockpit::ENV_VAR : Cockpit::ENV_VAR . '=' . $this->originalEnv);
        exec('rm -rf ' . escapeshellarg($this->world));
    }

    public function testTraversalSequencesAreNormalisedAwayInsteadOfSurvivingIntoEveryDerivedPath(): void
    {
        $cockpit = Cockpit::resolve($this->world . '/cockpit/nested/../../elsewhere');

        self::assertSame($this->world . '/elsewhere', $cockpit->root);
        self::assertSame($this->world . '/elsewhere/registry.yml', $cockpit->registryPath());
    }

    public function testASymlinkedCockpitResolvesToItsTargetSoTwoSpellingsAreOnePath(): void
    {
        symlink($this->world . '/cockpit', $this->world . '/link');

        self::assertSame(
            Cockpit::resolve($this->world . '/cockpit')->root,
            Cockpit::resolve($this->world . '/link')->root,
        );
    }

    public function testATrailingSlashDoesNotProduceADoubleSlashInDerivedPaths(): void
    {
        self::assertSame(
            $this->world . '/cockpit/registry.yml',
            Cockpit::resolve($this->world . '/cockpit/')->registryPath(),
        );
    }

    public function testAnEmptyCockpitOptionIsRefusedRatherThanRootingEveryPathAtTheFilesystemRoot(): void
    {
        putenv(Cockpit::ENV_VAR . '=' . $this->world . '/cockpit');

        $this->expectException(FilesystemException::class);
        $this->expectExceptionMessageMatches('/--cockpit/');

        Cockpit::resolve('');
    }

    public function testAnEmptyEnvironmentVariableFallsThroughToTheWorkingDirectory(): void
    {
        putenv(Cockpit::ENV_VAR . '=');
        $original = getcwd();
        self::assertNotFalse($original);
        chdir($this->world . '/cockpit');

        try {
            self::assertSame($this->world . '/cockpit', Cockpit::resolve(null)->root);
        } finally {
            chdir($original);
        }
    }

    public function testResolutionOrderIsExplicitOptionThenEnvironmentVariableThenWorkingDirectory(): void
    {
        putenv(Cockpit::ENV_VAR . '=' . $this->world . '/elsewhere');
        $original = getcwd();
        self::assertNotFalse($original);
        chdir($this->world . '/cockpit/nested');

        try {
            self::assertSame($this->world . '/cockpit', Cockpit::resolve($this->world . '/cockpit')->root);
            self::assertSame($this->world . '/elsewhere', Cockpit::resolve(null)->root);

            putenv(Cockpit::ENV_VAR);
            self::assertSame($this->world . '/cockpit/nested', Cockpit::resolve(null)->root);
        } finally {
            chdir($original);
        }
    }

    public function testADeletedWorkingDirectoryIsRefusedRatherThanResolvingToNothing(): void
    {
        // Falling back to the cwd is only meaningful while the cwd exists. A
        // shell left sitting in a directory that has since been removed must
        // get an explanation, not a cockpit rooted at an empty path.
        putenv(Cockpit::ENV_VAR);
        $original = getcwd();
        self::assertNotFalse($original);
        $doomed = $this->world . '/doomed';
        mkdir($doomed, 0o700);
        chdir($doomed);
        rmdir($doomed);

        try {
            $this->expectException(FilesystemException::class);
            $this->expectExceptionMessageMatches('/current directory/');

            Cockpit::resolve(null);
        } finally {
            chdir($original);
        }
    }

    public function testAnUnresolvableTraversalSegmentIsRefused(): void
    {
        $this->expectException(FilesystemException::class);

        Cockpit::resolve($this->world . '/no-such-dir/../escape');
    }
    /**
     * BP-CMD-13: the results/ and cache/dashboard/ layout used to live as
     * literal strings at six call sites while the other three sub-paths had
     * accessors. All five are derived from the same constants now.
     */
    public function testEverySubPathIsDerivedFromTheCockpitRootAndItsConstants(): void
    {
        $cockpit = new Cockpit($this->world . '/cockpit');

        self::assertSame($cockpit->root . '/' . Cockpit::REGISTRY_FILENAME, $cockpit->registryPath());
        self::assertSame($cockpit->root . '/' . Cockpit::BASE_ARTIFACTS_DIR, $cockpit->baseArtifactsPath());
        self::assertSame($cockpit->root . '/' . Cockpit::FIXTURES_DIR, $cockpit->fixturesPath());
        self::assertSame($cockpit->root . '/' . Cockpit::PROJECTS_DIR, $cockpit->projectsPath());
        self::assertSame($cockpit->root . '/' . Cockpit::RESULTS_DIR, $cockpit->resultsPath());
        self::assertSame($cockpit->root . '/' . Cockpit::DASHBOARD_CACHE_DIR, $cockpit->dashboardCachePath());
    }

    /** The documented on-disk layout, which existing cockpits already have. */
    public function testTheResultsAndDashboardCacheLocationsAreTheDocumentedOnes(): void
    {
        $cockpit = new Cockpit($this->world . '/cockpit');

        self::assertSame($cockpit->root . '/results', $cockpit->resultsPath());
        self::assertSame($cockpit->root . '/cache/dashboard', $cockpit->dashboardCachePath());
    }
}

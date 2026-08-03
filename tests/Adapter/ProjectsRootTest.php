<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\ProjectsRoot;
use Upkeep\Filesystem\FilesystemException;

final class ProjectsRootTest extends TestCase
{
    private string|false $originalEnv;

    private string|false $originalHome;

    /** Stands in for $HOME so no test ever touches the real home directory. */
    private string $home;

    protected function setUp(): void
    {
        $this->originalEnv = getenv(ProjectsRoot::ENV_VAR);
        $this->originalHome = getenv('HOME');

        $this->home = (string) realpath(sys_get_temp_dir()) . '/upkeep-projects-root-' . bin2hex(random_bytes(4));
        mkdir($this->home, 0o700, true);
        putenv('HOME=' . $this->home);
    }

    protected function tearDown(): void
    {
        putenv($this->originalEnv === false ? ProjectsRoot::ENV_VAR : ProjectsRoot::ENV_VAR . '=' . $this->originalEnv);
        putenv($this->originalHome === false ? 'HOME' : 'HOME=' . $this->originalHome);
        exec('rm -rf ' . escapeshellarg($this->home));
    }

    public function testExplicitConfigurationWinsOverEverything(): void
    {
        putenv(ProjectsRoot::ENV_VAR . '=' . $this->home . '/elsewhere');

        self::assertSame($this->home . '/explicit', ProjectsRoot::resolve($this->home . '/explicit'));
    }

    public function testEnvironmentVariableWinsOverDefault(): void
    {
        putenv(ProjectsRoot::ENV_VAR . '=' . $this->home . '/from-env');

        self::assertSame($this->home . '/from-env', ProjectsRoot::resolve(null));
    }

    public function testCockpitProjectsDirUsedWhenItExists(): void
    {
        putenv(ProjectsRoot::ENV_VAR);

        $cockpitRoot = $this->home . '/cockpit';
        $projectsDir = $cockpitRoot . '/projects';
        mkdir($projectsDir, 0o700, true);

        self::assertSame($projectsDir, ProjectsRoot::resolve(null, $cockpitRoot));
    }

    public function testCockpitProjectsDirSkippedWhenMissing(): void
    {
        putenv(ProjectsRoot::ENV_VAR);

        self::assertSame(
            $this->home . '/.upkeep/projects',
            ProjectsRoot::resolve(null, $this->home . '/cockpit-without-projects'),
        );
    }

    public function testEnvVarStillWinsOverCockpitProjects(): void
    {
        putenv(ProjectsRoot::ENV_VAR . '=' . $this->home . '/from-env');

        $projectsDir = $this->home . '/cockpit/projects';
        mkdir($projectsDir, 0o700, true);

        self::assertSame($this->home . '/from-env', ProjectsRoot::resolve(null, $this->home . '/cockpit'));
    }

    public function testDefaultsUnderHomeBecauseDockerProvidersOnlyMountHome(): void
    {
        putenv(ProjectsRoot::ENV_VAR);

        self::assertSame($this->home . '/.upkeep/projects', ProjectsRoot::resolve(null));
    }

    public function testRefusesToFallBackToATempDirWhenHomeIsUnavailable(): void
    {
        putenv(ProjectsRoot::ENV_VAR);
        putenv('HOME');

        $this->expectException(AdapterException::class);
        ProjectsRoot::resolve(null);
    }

    public function testAnExplicitProjectsRootOutsideHomeIsRefusedWithTheDockerMountReason(): void
    {
        $outside = (string) realpath(sys_get_temp_dir()) . '/upkeep-outside-' . bin2hex(random_bytes(4));
        mkdir($outside, 0o700, true);

        try {
            $this->expectException(AdapterException::class);
            $this->expectExceptionMessageMatches('/only (share|mount)/i');
            $this->expectExceptionMessageMatches('/home directory/i');
            ProjectsRoot::resolve($outside);
        } finally {
            @rmdir($outside);
        }
    }

    public function testTheEnvironmentVariableIsHeldToTheSameHomeContainmentRule(): void
    {
        putenv(ProjectsRoot::ENV_VAR . '=' . sys_get_temp_dir());

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessageMatches('/home directory/i');
        ProjectsRoot::resolve(null);
    }

    public function testATraversalEscapeOutOfHomeIsRefusedEvenThoughItStartsInsideHome(): void
    {
        $this->expectException(AdapterException::class);
        $this->expectExceptionMessageMatches('/home directory/i');
        ProjectsRoot::resolve($this->home . '/../escaped');
    }

    public function testASymlinkOutOfHomeIsRefusedThoughNoDotDotAppearsInTheSpelling(): void
    {
        $outside = (string) realpath(sys_get_temp_dir()) . '/upkeep-outside-' . bin2hex(random_bytes(4));
        mkdir($outside, 0o700, true);
        symlink($outside, $this->home . '/looks-inside');

        try {
            $this->expectException(AdapterException::class);
            $this->expectExceptionMessageMatches('/home directory/i');
            ProjectsRoot::resolve($this->home . '/looks-inside');
        } finally {
            @rmdir($outside);
        }
    }

    public function testAResolvedRootIsCanonicalSoTwoSpellingsProduceOnePath(): void
    {
        mkdir($this->home . '/projects', 0o700, true);

        self::assertSame(
            ProjectsRoot::resolve($this->home . '/projects'),
            ProjectsRoot::resolve($this->home . '/./projects/'),
        );
    }

    public function testExplicitConfigurationIsAlsoRefusedWhenHomeIsUnknown(): void
    {
        putenv('HOME');

        $this->expectException(AdapterException::class);
        ProjectsRoot::resolve('/anywhere');
    }

    /**
     * A path that cannot be canonicalised at all — a traversal segment below a
     * directory that does not exist, which cannot be resolved against anything
     * real — is reported as an adapter problem naming the projects root, not
     * as a bare filesystem error from two layers down.
     */
    public function testAnUnresolvablePathIsRefusedAsAProjectsRootProblem(): void
    {
        try {
            ProjectsRoot::resolve($this->home . '/never-created/../sideways');
            self::fail('Expected the unresolvable path to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('as the projects root', $e->getMessage());
            self::assertStringContainsString('traversal segment', $e->getMessage());
            self::assertInstanceOf(FilesystemException::class, $e->getPrevious());
        }
    }
}

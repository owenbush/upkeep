<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\ProjectsRoot;

final class ProjectsRootTest extends TestCase
{
    private string|false $originalEnv;

    private string|false $originalHome;

    protected function setUp(): void
    {
        $this->originalEnv = getenv(ProjectsRoot::ENV_VAR);
        $this->originalHome = getenv('HOME');
    }

    protected function tearDown(): void
    {
        putenv($this->originalEnv === false ? ProjectsRoot::ENV_VAR : ProjectsRoot::ENV_VAR . '=' . $this->originalEnv);
        putenv($this->originalHome === false ? 'HOME' : 'HOME=' . $this->originalHome);
    }

    public function testExplicitConfigurationWinsOverEverything(): void
    {
        putenv(ProjectsRoot::ENV_VAR . '=/home/user/elsewhere');

        self::assertSame('/home/user/explicit', ProjectsRoot::resolve('/home/user/explicit'));
    }

    public function testEnvironmentVariableWinsOverDefault(): void
    {
        putenv(ProjectsRoot::ENV_VAR . '=/home/user/from-env');

        self::assertSame('/home/user/from-env', ProjectsRoot::resolve(null));
    }

    public function testCockpitProjectsDirUsedWhenItExists(): void
    {
        putenv(ProjectsRoot::ENV_VAR);

        $cockpitRoot = sys_get_temp_dir() . '/upkeep-test-cockpit-' . getmypid();
        $projectsDir = $cockpitRoot . '/projects';
        @mkdir($projectsDir, 0755, true);

        try {
            self::assertSame($projectsDir, ProjectsRoot::resolve(null, $cockpitRoot));
        } finally {
            @rmdir($projectsDir);
            @rmdir($cockpitRoot);
        }
    }

    public function testCockpitProjectsDirSkippedWhenMissing(): void
    {
        putenv(ProjectsRoot::ENV_VAR);
        putenv('HOME=/home/user');

        $cockpitRoot = sys_get_temp_dir() . '/upkeep-test-no-projects-' . getmypid();

        self::assertSame('/home/user/.upkeep/projects', ProjectsRoot::resolve(null, $cockpitRoot));
    }

    public function testEnvVarStillWinsOverCockpitProjects(): void
    {
        putenv(ProjectsRoot::ENV_VAR . '=/home/user/from-env');

        $cockpitRoot = sys_get_temp_dir() . '/upkeep-test-cockpit-env-' . getmypid();
        $projectsDir = $cockpitRoot . '/projects';
        @mkdir($projectsDir, 0755, true);

        try {
            self::assertSame('/home/user/from-env', ProjectsRoot::resolve(null, $cockpitRoot));
        } finally {
            @rmdir($projectsDir);
            @rmdir($cockpitRoot);
        }
    }

    public function testDefaultsUnderHomeBecauseDockerProvidersOnlyMountHome(): void
    {
        putenv(ProjectsRoot::ENV_VAR);
        putenv('HOME=/home/user');

        self::assertSame('/home/user/.upkeep/projects', ProjectsRoot::resolve(null));
    }

    public function testRefusesToFallBackToATempDirWhenHomeIsUnavailable(): void
    {
        putenv(ProjectsRoot::ENV_VAR);
        putenv('HOME');

        $this->expectException(AdapterException::class);
        ProjectsRoot::resolve(null);
    }
}

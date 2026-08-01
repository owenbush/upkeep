<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Yaml\Yaml;
use Upkeep\Adapter\EngineAddOn;

final class EngineAddOnTest extends TestCase
{
    /**
     * The verbatim config.contrib.yaml shipped by ddev-drupal-contrib 1.1.5 —
     * the pinned engine add-on version. If the pin moves, refresh this fixture
     * and re-verify the adaptation against the new file.
     */
    private const SHIPPED_CONFIG = <<<'YAML'
        #ddev-generated
        ## Command provided by https://github.com/ddev/ddev-drupal-contrib
        web_environment:
          - IGNORE_PROJECT_DRUPAL_CORE_VERSION=1
          - DRUPAL_PROJECTS_PATH=modules/custom
          - SIMPLETEST_DB=mysql://db:db@db/db
          - SIMPLETEST_BASE_URL=http://web
          - BROWSERTEST_OUTPUT_DIRECTORY=/tmp
          - BROWSERTEST_OUTPUT_BASE_URL=${DDEV_PRIMARY_URL}
        hooks:
          post-start:
            - exec-host: |
                if [[ -f vendor/autoload.php ]]; then
                  ddev symlink-project
                else
                  exit 0
                fi
        YAML;

    public function testRemovesTheModuleAsProjectPostStartHook(): void
    {
        $adapted = Yaml::parse(EngineAddOn::adaptContribConfig(self::SHIPPED_CONFIG));

        self::assertArrayNotHasKey('hooks', $adapted);
    }

    public function testRepointsProjectsPathAtTheComposerInstalledModuleLocation(): void
    {
        $adapted = Yaml::parse(EngineAddOn::adaptContribConfig(self::SHIPPED_CONFIG));

        self::assertContains('DRUPAL_PROJECTS_PATH=modules/contrib', $adapted['web_environment']);
        self::assertNotContains('DRUPAL_PROJECTS_PATH=modules/custom', $adapted['web_environment']);
    }

    public function testPreservesTheEngineTestRunnerEnvironment(): void
    {
        $adapted = Yaml::parse(EngineAddOn::adaptContribConfig(self::SHIPPED_CONFIG));

        self::assertContains('SIMPLETEST_DB=mysql://db:db@db/db', $adapted['web_environment']);
        self::assertContains('SIMPLETEST_BASE_URL=http://web', $adapted['web_environment']);
        self::assertContains('BROWSERTEST_OUTPUT_BASE_URL=${DDEV_PRIMARY_URL}', $adapted['web_environment']);
    }

    public function testKeepsTheDdevGeneratedMarkerSoReGetsStayDetectable(): void
    {
        self::assertStringStartsWith("#ddev-generated\n", EngineAddOn::adaptContribConfig(self::SHIPPED_CONFIG));
    }

    public function testAdaptationIsIdempotent(): void
    {
        $once = EngineAddOn::adaptContribConfig(self::SHIPPED_CONFIG);

        self::assertSame($once, EngineAddOn::adaptContribConfig($once));
    }
}

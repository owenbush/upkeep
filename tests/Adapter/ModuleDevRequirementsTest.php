<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\ModuleDevRequirements;

/**
 * The dev dependencies a module declares for itself.
 *
 * The other half of honouring a module's own phpcs and phpstan configuration:
 * those configurations reference packages the *module* requires, not the site.
 * Read live from field_visibility_conditions 2.0.x, its ruleset says
 *
 *     <rule ref="./vendor/phpcompatibility/php-compatibility/PHPCompatibility"/>
 *
 * and its composer.json puts `phpcompatibility/php-compatibility: ^9.3` in
 * require-dev. CI resolves that because `composer install` runs in the module
 * repository. Under ddev-drupal-contrib the module is a path repository of the
 * site, and composer does not install a path dependency's require-dev — so the
 * sniff was absent and the check failed against a ruleset that is correct.
 */
final class ModuleDevRequirementsTest extends TestCase
{
    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/upkeep-devreqs-' . bin2hex(random_bytes(4));
        mkdir($this->dir, 0o755, true);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    /** @param array<string, mixed> $manifest */
    private function manifest(array $manifest): string
    {
        file_put_contents($this->dir . '/composer.json', json_encode($manifest, \JSON_THROW_ON_ERROR));

        return $this->dir;
    }

    /** The real manifest that surfaced this, verbatim. */
    public function testTheModulesOwnDevDependenciesAreWhatComesBack(): void
    {
        $dir = $this->manifest([
            'require' => ['php' => '>=8.1', 'drupal/core' => '^10.1 || ^11 || ^12'],
            'require-dev' => ['drupal/coder' => '^8.3', 'phpcompatibility/php-compatibility' => '^9.3'],
        ]);

        self::assertSame(
            ['drupal/coder', 'phpcompatibility/php-compatibility'],
            ModuleDevRequirements::of($dir),
        );
    }

    /** Runtime requirements are the site's business, not the toolchain's. */
    public function testTheRuntimeRequireSectionIsNotInstalledAsDev(): void
    {
        $dir = $this->manifest([
            'require' => ['drupal/token' => '^1.0'],
            'require-dev' => ['drupal/coder' => '^8.3'],
        ]);

        self::assertSame(['drupal/coder'], ModuleDevRequirements::of($dir));
    }

    /**
     * Platform requirements are not packages. Asking composer to require `php`
     * or an extension into the site fails a provision over something no
     * install can satisfy.
     */
    public function testPlatformRequirementsAreDropped(): void
    {
        $dir = $this->manifest([
            'require-dev' => [
                'php' => '>=8.1',
                'ext-gd' => '*',
                'lib-curl' => '*',
                'phpcompatibility/php-compatibility' => '^9.3',
            ],
        ]);

        self::assertSame(['phpcompatibility/php-compatibility'], ModuleDevRequirements::of($dir));
    }

    /**
     * A module with no manifest, an unreadable one, or no require-dev is not a
     * reason to refuse to check it. The checks still run, and a configuration
     * referencing something missing reports its own clear error.
     */
    public function testAnythingUnreadableYieldsNoneRatherThanFailing(): void
    {
        self::assertSame([], ModuleDevRequirements::of($this->dir), 'no composer.json at all');
        self::assertSame([], ModuleDevRequirements::of($this->dir . '/nope'));

        file_put_contents($this->dir . '/composer.json', 'not json');
        self::assertSame([], ModuleDevRequirements::of($this->dir));

        self::assertSame([], ModuleDevRequirements::of($this->manifest(['name' => 'drupal/widget'])));
        self::assertSame([], ModuleDevRequirements::of($this->manifest(['require-dev' => 'nonsense'])));
        self::assertSame([], ModuleDevRequirements::of($this->manifest([])));
    }

    /**
     * A trailing slash on the directory is the caller's habit, not an error.
     */
    public function testTheModuleDirectoryMayCarryATrailingSlash(): void
    {
        $dir = $this->manifest(['require-dev' => ['drupal/coder' => '^8.3']]);

        self::assertSame(['drupal/coder'], ModuleDevRequirements::of($dir . '/'));
    }

    /**
     * The warning names the packages, and says what it means for the run
     * rather than only what failed: the checks continue, and a configuration
     * that needed one of them will say so itself.
     */
    public function testTheWarningNamesThePackagesAndTheConsequence(): void
    {
        $message = ModuleDevRequirements::unavailable(['phpcompatibility/php-compatibility']);

        self::assertStringContainsString('phpcompatibility/php-compatibility', $message);
        self::assertStringContainsString('missing', $message);
        self::assertStringContainsString('the check will name it', $message);
    }
}

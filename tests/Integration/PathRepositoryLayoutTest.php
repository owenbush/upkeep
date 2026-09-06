<?php

declare(strict_types=1);

namespace Upkeep\Tests\Integration;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\ModuleDevRequirements;
use Upkeep\Adapter\ProcessRunner;
use Upkeep\Security\SecretRedactor;

/**
 * The composer fact that broke the third check: **a path dependency's
 * `require-dev` is never installed.**
 *
 * ddev-drupal-contrib wires the module into the site as a path repository, so
 * the module's own dev requirements are not the site's. A module's phpcs
 * ruleset referencing `./vendor/phpcompatibility/php-compatibility/…` — which
 * field_visibility_conditions' does, and which its composer.json requires —
 * therefore found nothing, and the check failed against a ruleset that is
 * perfectly correct. CI does not hit it because there `composer install` runs
 * in the module repository itself.
 *
 * This needs no docker and no ddev: it is composer's behaviour, and the
 * cheapest possible place to pin it. It lives with the integration suite
 * rather than the unit one because it shells out to composer and writes a
 * package tree, neither of which belongs in a suite that has to stay fast and
 * offline.
 */
final class PathRepositoryLayoutTest extends TestCase
{
    private string $dir;

    protected function setUp(): void
    {
        if (self::composer() === null) {
            self::markTestSkipped('composer is not on PATH.');
        }

        $this->dir = sys_get_temp_dir() . '/upkeep-pathrepo-' . bin2hex(random_bytes(4));
        mkdir($this->dir . '/site', 0o755, true);
        mkdir($this->dir . '/module', 0o755, true);
        mkdir($this->dir . '/sniffer', 0o755, true);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    private static function composer(): ?string
    {
        $which = trim((string) shell_exec('command -v composer 2>/dev/null'));

        return $which === '' ? null : $which;
    }

    /** @param array<string, mixed> $manifest */
    private function write(string $where, array $manifest): void
    {
        file_put_contents(
            $this->dir . '/' . $where . '/composer.json',
            json_encode($manifest, \JSON_THROW_ON_ERROR | \JSON_PRETTY_PRINT),
        );
    }

    /**
     * Everything is a local path package, so this resolves with no network at
     * all — the behaviour under test is composer's, not packagist's.
     */
    private function buildSite(): void
    {
        // Stands in for phpcompatibility/php-compatibility: something the
        // module wants for its own checks and the site has never heard of.
        $this->write('sniffer', ['name' => 'fixture/sniffer', 'version' => '1.0.0']);

        $this->write('module', [
            'name' => 'fixture/widget',
            'version' => '1.0.0',
            'repositories' => [['type' => 'path', 'url' => '../sniffer']],
            'require-dev' => ['fixture/sniffer' => '*'],
        ]);

        $this->write('site', [
            'name' => 'fixture/site',
            'repositories' => [['type' => 'path', 'url' => '../module']],
            'require' => ['fixture/widget' => '*'],
            'config' => ['allow-plugins' => false],
        ]);
    }

    private function installSite(): string
    {
        $captured = (new ProcessRunner(static function (): void {
        }, new SecretRedactor()))->capture(
            ['composer', 'install', '--no-interaction', '--no-progress'],
            $this->dir . '/site',
            300,
        );

        self::assertSame(0, $captured->exitCode, $captured->output);

        return $captured->output;
    }

    /**
     * The fact itself. The module is installed; what the module needs to check
     * itself is not.
     */
    public function testAPathDependencysDevRequirementsAreNotInstalled(): void
    {
        $this->buildSite();
        $this->installSite();

        self::assertDirectoryExists(
            $this->dir . '/site/vendor/fixture/widget',
            'the module itself is installed, as ddev-drupal-contrib installs it',
        );
        self::assertDirectoryDoesNotExist(
            $this->dir . '/site/vendor/fixture/sniffer',
            'and its require-dev is not — which is why a module ruleset referencing '
            . './vendor/… found nothing',
        );
    }

    /**
     * And upkeep's answer: read the module's require-dev and install it into
     * the site alongside the toolchain. The names have to be the ones composer
     * accepts, so this reads the same manifest the adapter reads and requires
     * exactly that.
     */
    public function testInstallingWhatTheModuleDeclaresPutsItWhereARulesetLooks(): void
    {
        $this->buildSite();
        $this->installSite();

        $packages = ModuleDevRequirements::of($this->dir . '/module');
        self::assertSame(['fixture/sniffer'], $packages);

        // The site cannot resolve a path package it has no repository for, so
        // the fixture site is pointed at it the way ddev-drupal-contrib's
        // project composer.json already knows about the module.
        $captured = (new ProcessRunner(static function (): void {
        }, new SecretRedactor()))->capture(
            ['composer', 'config', 'repositories.sniffer', 'path', '../sniffer'],
            $this->dir . '/site',
            120,
        );
        self::assertSame(0, $captured->exitCode, $captured->output);

        $required = (new ProcessRunner(static function (): void {
        }, new SecretRedactor()))->capture(
            ['composer', 'require', '--dev', '--no-interaction', '--no-progress', ...$packages],
            $this->dir . '/site',
            300,
        );

        self::assertSame(0, $required->exitCode, $required->output);
        self::assertDirectoryExists(
            $this->dir . '/site/vendor/fixture/sniffer',
            'now a ./vendor/… reference in the module\'s ruleset resolves',
        );
    }
}

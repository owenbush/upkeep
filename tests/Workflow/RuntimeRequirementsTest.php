<?php

declare(strict_types=1);

namespace Upkeep\Tests\Workflow;

use PHPUnit\Framework\TestCase;
use Upkeep\Workflow\RuntimeRequirements;

/**
 * The check that would have turned a forty-line stack trace into one sentence.
 *
 * A commit added `composer/semver` to composer.json. Every existing checkout
 * then had a vendor/ older than its code, and the first command reaching the
 * new package died with an uncaught `Class "Composer\Semver\VersionParser" not
 * found` — from a tool whose exit-code contract says a 2 and a recovery line.
 *
 * Neither gate could see it. `./bin/upkeep list` boots the application and
 * touches no path that reaches a dependency, so it exited 0 with the package
 * gone; and the suite runs against a vendor/ where it is present by
 * construction. Nothing in the repository was wrong — composer.json declared
 * it correctly. What was missing was a signal to an install that predated the
 * declaration.
 */
final class RuntimeRequirementsTest extends TestCase
{
    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/upkeep-requirements-' . bin2hex(random_bytes(4));
        mkdir($this->dir, 0o755, true);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    /** @param array<string, mixed> $manifest */
    private function manifest(array $manifest): string
    {
        $path = $this->dir . '/composer.json';
        file_put_contents($path, json_encode($manifest, \JSON_THROW_ON_ERROR));

        return $path;
    }

    /**
     * The manifest is the source of truth, so a dependency added tomorrow is
     * covered without anyone remembering to list it here. That is the only
     * version of this check worth having — a hand-maintained list would have
     * been forgotten by exactly the commit that caused this.
     */
    public function testTheRequirementsComeFromTheManifestItself(): void
    {
        $path = $this->manifest(['require' => [
            'php' => '>=8.2',
            'composer/semver' => '^3.4',
            'symfony/console' => '^7.2',
        ]]);

        self::assertSame(
            ['composer/semver', 'symfony/console'],
            RuntimeRequirements::declaredIn($path),
        );
    }

    /**
     * Platform requirements are not installed packages. PHP has already
     * refused to run if the version is wrong, and a missing extension produces
     * its own clear error — asking composer's metadata about either would
     * report every install as broken.
     */
    public function testPlatformRequirementsAreNotPackages(): void
    {
        $path = $this->manifest(['require' => [
            'php' => '>=8.2',
            'ext-json' => '*',
            'lib-curl' => '*',
            'symfony/yaml' => '^7.2',
        ]]);

        self::assertSame(['symfony/yaml'], RuntimeRequirements::declaredIn($path));
    }

    /** Only the runtime section; a dev dependency is not needed to run. */
    public function testDevRequirementsAreNotChecked(): void
    {
        $path = $this->manifest([
            'require' => ['symfony/console' => '^7.2'],
            'require-dev' => ['phpunit/phpunit' => '^11.5'],
        ]);

        self::assertSame(['symfony/console'], RuntimeRequirements::declaredIn($path));
    }

    /**
     * A manifest that cannot be read yields no requirements, so the check
     * passes and the tool starts.
     *
     * Deliberately the opposite direction from everything else here: refusing
     * to start because the *manifest* could not be read would turn a cosmetic
     * problem into a fatal one, which is the failure this exists to prevent
     * rather than to reproduce.
     */
    public function testAnUnreadableManifestIsNotAReasonToRefuseToStart(): void
    {
        self::assertSame([], RuntimeRequirements::declaredIn($this->dir . '/nope.json'));
        self::assertSame([], RuntimeRequirements::declaredIn($this->dir));

        file_put_contents($this->dir . '/junk.json', 'not json at all');
        self::assertSame([], RuntimeRequirements::declaredIn($this->dir . '/junk.json'));

        file_put_contents($this->dir . '/scalar.json', '42');
        self::assertSame([], RuntimeRequirements::declaredIn($this->dir . '/scalar.json'));

        self::assertSame([], RuntimeRequirements::declaredIn($this->manifest(['name' => 'x/y'])));
        self::assertSame([], RuntimeRequirements::declaredIn($this->manifest(['require' => 'nonsense'])));
    }

    public function testOnlyThePackagesThatAreActuallyAbsentAreReported(): void
    {
        $installed = static fn (string $p): bool => $p !== 'composer/semver';

        self::assertSame(
            ['composer/semver'],
            RuntimeRequirements::missing(['symfony/console', 'composer/semver', 'symfony/yaml'], $installed),
        );
    }

    public function testAFullyInstalledCheckoutReportsNothing(): void
    {
        self::assertSame(
            [],
            RuntimeRequirements::missing(['symfony/console'], static fn (): bool => true),
        );
    }

    /**
     * The message names what is missing and what to run, and *where* to run
     * it: somebody meeting this has usually just pulled in one checkout among
     * several, and `composer install` in the wrong one fixes nothing.
     */
    public function testTheMessageNamesThePackagesTheDirectoryAndTheCommand(): void
    {
        $message = RuntimeRequirements::message(['composer/semver'], '/Users/owen/contrib/upkeep');

        self::assertStringContainsString('1 required package is not installed', $message);
        self::assertStringContainsString('composer/semver', $message);
        self::assertStringContainsString('/Users/owen/contrib/upkeep', $message);
        self::assertStringContainsString('composer install', $message);
    }

    public function testTheMessagePluralisesForSeveral(): void
    {
        $message = RuntimeRequirements::message(['composer/semver', 'symfony/yaml'], '/tmp/upkeep');

        self::assertStringContainsString('2 required packages are not installed', $message);
        self::assertStringContainsString('symfony/yaml', $message);
    }

    /**
     * The real manifest, checked against the real installation.
     *
     * The suite runs where every dependency is present, so this can only ever
     * pass — which is exactly why it could not catch the original failure, and
     * why the guard it covers lives in `bin/upkeep` rather than here. It is
     * still worth asserting that the two halves fit together on real inputs:
     * that composer.json parses, that its package names are the names
     * composer's own metadata uses, and so that a genuinely stale vendor/ is
     * reported rather than missed.
     */
    public function testTheProjectsOwnManifestResolvesAgainstComposersMetadata(): void
    {
        $declared = RuntimeRequirements::declaredIn(\dirname(__DIR__, 2) . '/composer.json');

        self::assertContains('composer/semver', $declared, 'the dependency that caused this');
        self::assertSame(
            [],
            RuntimeRequirements::missing($declared, \Composer\InstalledVersions::isInstalled(...)),
        );
    }

    /**
     * Installed as somebody's dependency — which is what
     * `composer global require` does — the package sits under vendor/, where
     * `composer install` is meaningless. Naming the wrong command in a
     * directory that is not a project is worse than naming none, because it
     * reads as authoritative.
     */
    public function testAVendoredInstallIsToldToUpdateThePackageNotToInstallInIt(): void
    {
        $message = RuntimeRequirements::message(['symfony/yaml'], '/home/me/.composer/vendor/owenbush');

        self::assertStringContainsString('composer global update owenbush/upkeep', $message);
        self::assertStringNotContainsString('composer install', $message);
        self::assertStringNotContainsString('/home/me/.composer/vendor/owenbush', $message);
    }

    /** A clone has a project to run it in, and the path is the useful part. */
    public function testACloneIsToldWhereToRunComposerInstall(): void
    {
        $message = RuntimeRequirements::message(['symfony/yaml'], '/home/me/code/upkeep');

        self::assertStringContainsString('From /home/me/code/upkeep:', $message);
        self::assertStringContainsString('composer install', $message);
        self::assertStringNotContainsString('global update', $message);
    }
}

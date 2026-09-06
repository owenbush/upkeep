<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckScript;

/**
 * The two checks a module can configure for itself.
 *
 * phpstan and phpcs both discover their configuration from the **current
 * working directory**, and upkeep ran them from the project root — where the
 * only config is the gitlab_templates default it had just downloaded. A module
 * shipping its own `phpstan.neon` or `phpcs.xml.dist` had every bit of it
 * ignored: its level, its baseline, its ignores, its ruleset. The check then
 * reported a verdict against rules the project does not use, and agreed or
 * disagreed with CI for reasons nobody could see.
 *
 * drupal.org's CI does the opposite on purpose. `.phpstan-base` starts with
 * `cd $DRUPAL_PROJECT_FOLDER` and fetches the template only in the `else`
 * branch; `.phpcs-base` starts with `cd $CI_PROJECT_DIR` and looks for
 * `{.,}phpcs.xml{.dist,}` before falling back. These are shell scripts sent to
 * a container, so nothing else in the suite can hold them to that — which is
 * why they are pinned here.
 */
final class CheckScriptTest extends TestCase
{
    private const MODULE = '"$DDEV_DOCROOT/$DRUPAL_PROJECTS_PATH"/\'widget\'';

    /**
     * The invariant two rewrites were lost to, stated so a third cannot be.
     *
     * `ddev exec` re-joins its arguments and hands the result to a shell that
     * expands the string *before* the inner shell runs it. A variable defined
     * and used in the same command therefore cannot work: `ROOT=$(pwd) && …
     * "$ROOT/x"` expands `$ROOT` while the string is being built, where it is
     * unset, and dies with `ROOT: unbound variable`. An earlier `MODULE=…`
     * died the same way. Both were found on real runs, after a green suite,
     * because nothing here executes what it builds.
     *
     * Only variables already in the container's environment survive, since
     * expanding them early yields the same value. So those two are the only
     * ones allowed to appear.
     */
    public function testNoCommandUsesAShellVariableOfItsOwn(): void
    {
        foreach (self::everyCommand() as $label => $command) {
            preg_match_all('/\$\{?([A-Za-z_][A-Za-z0-9_]*)/', $command, $found);

            self::assertSame(
                [],
                array_values(array_diff(array_unique($found[1]), ['DDEV_DOCROOT', 'DRUPAL_PROJECTS_PATH'])),
                $label . ' uses a variable ddev exec will have expanded away before the shell runs',
            );
            self::assertStringNotContainsString('$(', $command, $label . ' substitutes a command too early');
        }
    }

    /**
     * And nothing changes directory.
     *
     * CI runs these from inside the module because in CI the module repo root
     * is where composer put `vendor/`. Under ddev-drupal-contrib the module is
     * a checkout symlinked into a site whose vendor lives at the project root,
     * so from inside the module every vendor-relative path in a ruleset breaks
     * — observed as `Referenced sniff "./vendor/drupal/coder/coder_sniffer/
     * Drupal" does not exist` against a real module's own phpcs.xml.dist.
     */
    public function testNoCommandLeavesTheProjectRoot(): void
    {
        foreach (self::everyCommand() as $label => $command) {
            self::assertDoesNotMatchRegularExpression('/(^|\s)cd\s/', $command, $label);
        }
    }

    /** One line, because newlines are one more thing the join does not keep. */
    public function testEveryCommandIsASingleLineAndRebindsNoShellState(): void
    {
        foreach (self::everyCommand() as $label => $command) {
            self::assertStringNotContainsString("\n", $command, $label);
            self::assertDoesNotMatchRegularExpression('/(^|\s)set(\s|$)/', $command, $label);
        }
    }

    /**
     * @return array<string, string>
     */
    private static function everyCommand(): array
    {
        return [
            'phpstan probe' => CheckScript::configProbe(self::MODULE, 'phpstan.neon'),
            'phpcs probe' => CheckScript::configProbe(self::MODULE, 'phpcs.xml.dist'),
            'phpstan with the module\'s own config' => CheckScript::phpStan(self::MODULE, 'phpstan.neon'),
            'phpstan falling back' => CheckScript::phpStan(self::MODULE, null),
            'phpcs with the module\'s own ruleset' => CheckScript::phpCs(self::MODULE, 'phpcs.xml.dist'),
            'phpcs falling back' => CheckScript::phpCs(self::MODULE, null),
        ];
    }

    // ------------------------------------------------------------- the probe

    public function testTheProbeAsksAboutOneNamedFile(): void
    {
        self::assertSame(
            'test -f ' . self::MODULE . '/phpstan.neon',
            CheckScript::configProbe(self::MODULE, 'phpstan.neon'),
        );
    }

    /**
     * The names, in the order the tools resolve them — which is the order the
     * adapter probes in, so upkeep picks the same file the tool would.
     */
    public function testTheConfigNamesAreTheOnesTheToolsAndCiUse(): void
    {
        self::assertSame(
            ['phpstan.neon', 'phpstan.neon.dist', 'phpstan.dist.neon'],
            CheckScript::PHPSTAN_CONFIGS,
        );
        self::assertSame(
            ['phpcs.xml', 'phpcs.xml.dist', '.phpcs.xml', '.phpcs.xml.dist'],
            CheckScript::PHPCS_CONFIGS,
        );
    }

    // ------------------------------------------------------------- the runs

    /**
     * The bug this all started from: a module's own configuration is used, not
     * the gitlab_templates default that happened to be sitting in the working
     * directory.
     */
    public function testAModulesOwnConfigurationIsNamedExplicitly(): void
    {
        self::assertStringContainsString(
            '-c ' . self::MODULE . '/phpstan.neon',
            CheckScript::phpStan(self::MODULE, 'phpstan.neon'),
        );
        self::assertStringContainsString(
            '--standard=' . self::MODULE . '/phpcs.xml.dist',
            CheckScript::phpCs(self::MODULE, 'phpcs.xml.dist'),
        );
    }

    /** With its own config there is nothing to download. */
    public function testNothingIsFetchedWhenTheModuleHasItsOwn(): void
    {
        self::assertStringNotContainsString('curl', CheckScript::phpStan(self::MODULE, 'phpstan.neon'));
        self::assertStringNotContainsString('curl', CheckScript::phpCs(self::MODULE, 'phpcs.xml.dist'));
    }

    public function testTheFallbackFetchesTheTemplateAndUsesIt(): void
    {
        $script = CheckScript::phpStan(self::MODULE, null);

        self::assertStringContainsString('curl -sSOL', $script);
        self::assertStringContainsString('BASELINE_PLACEHOLDER', $script);
        self::assertStringEndsWith('-c phpstan.neon', $script, 'the one downloaded into the project root');
    }

    /** Only the module is scanned, and reported relative to itself. */
    public function testOnlyTheModuleIsScannedAndPathsAreRelativeToIt(): void
    {
        foreach ([null, 'phpcs.xml.dist'] as $ownConfig) {
            $script = CheckScript::phpCs(self::MODULE, $ownConfig);

            self::assertStringContainsString('--basepath=' . self::MODULE, $script);
            self::assertStringContainsString("--ignore='*/.ddev/*'", $script);
            self::assertStringEndsWith(self::MODULE, $script);
        }
    }

    /**
     * `&&` and `||` bind equally and left to right, so the guarded steps are
     * braced. Ungrouped, a failed download falls through to the *next* step's
     * `||` and the tool runs against a config that was never fetched — a green
     * check on nothing.
     */
    public function testGuardedStepsAreGroupedSoAFailedFetchStopsTheChain(): void
    {
        foreach ([CheckScript::phpStan(self::MODULE, null), CheckScript::phpCs(self::MODULE, null)] as $script) {
            preg_match_all('/\{[^}]*\|\|[^}]*; \}/', $script, $braced);
            preg_match_all('/\|\|/', $script, $ors);

            self::assertNotSame([], $ors[0]);
            self::assertCount(\count($ors[0]), $braced[0], 'every || in a && chain must be braced');
        }
    }

    /**
     * The fallback writes at the project root and never into the module: that
     * directory is a git checkout, and two untracked files in it would make
     * the next applyPatch or startWork refuse on a dirty working copy.
     */
    public function testTheFallbackNeverWritesIntoTheModuleCheckout(): void
    {
        foreach ([CheckScript::phpStan(self::MODULE, null), CheckScript::phpCs(self::MODULE, null)] as $script) {
            preg_match_all('/(?:curl -sSOL \S+|sed -i \S+ (\S+)|touch (\S+))/', $script, $writes);

            foreach (array_merge($writes[1], $writes[2]) as $target) {
                if ($target === '') {
                    continue;
                }
                self::assertStringNotContainsString('DDEV_DOCROOT', $target, 'written inside the module');
            }
        }
    }
}

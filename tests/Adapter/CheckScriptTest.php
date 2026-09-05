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
     * The property everything else here depends on, and the one that was
     * missing.
     *
     * `ddev exec` re-joins its arguments into one command string for the
     * container's shell, so newlines do not survive. The first version of
     * these was a multi-line script; it arrived as
     * `set -eu ROOT=$(pwd) MODULE=…`, one `set` call that turned on `-u` and
     * swallowed both assignments as positional parameters, and the first
     * `"$MODULE"` after it died with `MODULE: unbound variable` on a real run.
     *
     * A command that fits on one line cannot be mangled that way.
     */
    public function testEveryCommandIsASingleLine(): void
    {
        foreach (self::everyCommand() as $label => $command) {
            self::assertStringNotContainsString("\n", $command, $label . ' must survive being joined onto one line');
        }
    }

    /**
     * And no `set`, which is what ate the assignments. Options belong on the
     * commands themselves.
     */
    public function testNoCommandRebindsTheShellsState(): void
    {
        foreach (self::everyCommand() as $label => $command) {
            self::assertDoesNotMatchRegularExpression('/(^|\s)set(\s|$)/', $command, $label);
        }
    }

    /**
     * @return array<string, string>
     */
    private static function everyCommand(): array
    {
        return [
            'phpstan probe' => CheckScript::configProbe(self::MODULE, CheckScript::PHPSTAN_CONFIGS),
            'phpcs probe' => CheckScript::configProbe(self::MODULE, CheckScript::PHPCS_CONFIGS),
            'phpstan with the module\'s own config' => CheckScript::phpStan(self::MODULE, true),
            'phpstan falling back' => CheckScript::phpStan(self::MODULE, false),
            'phpcs with the module\'s own ruleset' => CheckScript::phpCs(self::MODULE, true),
            'phpcs falling back' => CheckScript::phpCs(self::MODULE, false),
        ];
    }

    // ------------------------------------------------------------- the probe

    /**
     * Exit status is the whole answer, so there is nothing to parse — and the
     * decision it feeds is made in PHP, where an `if` survives.
     */
    public function testTheProbeTestsEveryConfigNameTheToolLooksFor(): void
    {
        $script = CheckScript::configProbe(self::MODULE, CheckScript::PHPSTAN_CONFIGS);

        foreach (CheckScript::PHPSTAN_CONFIGS as $name) {
            self::assertStringContainsString('test -f ' . self::MODULE . '/' . $name, $script);
        }
        self::assertSame(2, substr_count($script, '||'), 'three tests, two separators');
    }

    public function testTheConfigNamesAreTheOnesTheToolsAndCiUse(): void
    {
        self::assertSame(
            ['phpstan.neon', 'phpstan.neon.dist', 'phpstan.dist.neon'],
            CheckScript::PHPSTAN_CONFIGS,
            'PHPStan\'s own precedence',
        );
        self::assertSame(
            ['phpcs.xml', 'phpcs.xml.dist', '.phpcs.xml', '.phpcs.xml.dist'],
            CheckScript::PHPCS_CONFIGS,
        );
    }

    // ----------------------------------------------------------- phpstan

    /**
     * The original bug, stated as an assertion: the analysis runs from inside
     * the module, so PHPStan finds the module's own config the way CI lets it.
     */
    public function testPhpStanRunsFromInsideTheModule(): void
    {
        foreach ([true, false] as $ownConfig) {
            $script = CheckScript::phpStan(self::MODULE, $ownConfig);
            self::assertStringContainsString('cd ' . self::MODULE . ' &&', $script);
        }
    }

    /** With its own config there is nothing to fetch and nothing to point at. */
    public function testAModulesOwnConfigIsDiscoveredRatherThanPassed(): void
    {
        $script = CheckScript::phpStan(self::MODULE, true);

        self::assertStringNotContainsString('-c ', $script);
        self::assertStringNotContainsString('curl', $script);
        self::assertStringEndsWith('phpstan analyze . --autoload-file="$ROOT/vendor/autoload.php"', $script);
    }

    /**
     * The autoloader becomes load-bearing the moment the working directory
     * stops being the project root: PHPStan resolves Drupal's classes through
     * the site's autoloader and cannot find it from inside the module. CI
     * passes it for the same reason.
     */
    public function testTheSitesAutoloaderIsPassedAndItsRootCapturedBeforeTheCd(): void
    {
        foreach ([true, false] as $ownConfig) {
            $script = CheckScript::phpStan(self::MODULE, $ownConfig);

            self::assertStringContainsString('--autoload-file="$ROOT/vendor/autoload.php"', $script);
            self::assertLessThan(
                strpos($script, 'cd ' . self::MODULE),
                strpos($script, 'ROOT=$(pwd)'),
                'the project root has to be captured while it still is the working directory',
            );
        }
    }

    public function testTheFallbackFetchesTheTemplateAndNamesIt(): void
    {
        $script = CheckScript::phpStan(self::MODULE, false);

        self::assertStringContainsString('curl -sSOL', $script);
        self::assertStringContainsString('BASELINE_PLACEHOLDER', $script);
        self::assertStringEndsWith('-c "$ROOT/phpstan.neon"', $script);
    }

    /**
     * `&&` and `||` bind equally and left to right, so the guarded steps are
     * braced. Ungrouped, a failed download falls through to the *next* step's
     * `||` and the analysis runs against a config that was never fetched —
     * a green check on nothing.
     */
    public function testGuardedStepsAreGroupedSoAFailedFetchStopsTheChain(): void
    {
        foreach ([CheckScript::phpStan(self::MODULE, false), CheckScript::phpCs(self::MODULE, false)] as $script) {
            preg_match_all('/\{[^}]*\|\|[^}]*; \}/', $script, $braced);
            preg_match_all('/\|\|/', $script, $ors);

            self::assertNotSame([], $ors[0]);
            self::assertCount(
                \count($ors[0]),
                $braced[0],
                'every || in a && chain must be braced or its precedence is wrong',
            );
        }
    }

    // ------------------------------------------------------------- phpcs

    public function testPhpCsRunsFromInsideTheModuleAndReportsRelativePaths(): void
    {
        foreach ([true, false] as $ownConfig) {
            $script = CheckScript::phpCs(self::MODULE, $ownConfig);

            self::assertStringContainsString('cd ' . self::MODULE . ' &&', $script);
            self::assertStringContainsString('--basepath=.', $script);
            self::assertStringContainsString("--ignore='*/.ddev/*'", $script);
            self::assertStringEndsWith(' .', $script, 'the module itself is what gets scanned');
        }
    }

    public function testAModulesOwnRulesetIsDiscoveredRatherThanPassed(): void
    {
        self::assertStringNotContainsString('--standard', CheckScript::phpCs(self::MODULE, true));
        self::assertStringContainsString('--standard="$ROOT/phpcs.xml.dist"', CheckScript::phpCs(self::MODULE, false));
    }

    // ------------------------------------------------- the shared invariant

    /**
     * Nothing is ever written into the module directory.
     *
     * CI drops the fallback config and baseline beside the code because the
     * container is thrown away. Here the module directory is a real git
     * checkout, and two untracked files in it would make the next applyPatch
     * or startWork refuse on a dirty working copy — turning a config fallback
     * into a broken tool.
     */
    public function testTheFallbackNeverWritesIntoTheModuleCheckout(): void
    {
        foreach ([CheckScript::phpStan(self::MODULE, false), CheckScript::phpCs(self::MODULE, false)] as $script) {
            $cd = strpos($script, 'cd ' . self::MODULE);
            self::assertIsInt($cd);

            foreach (['curl', 'sed -i', 'touch'] as $write) {
                $at = strpos($script, $write);
                if ($at === false) {
                    continue;
                }
                self::assertLessThan($cd, $at, $write . ' must run at the project root, never inside the module');
            }
        }
    }

    /**
     * The module path arrives already shell-quoted and is spliced in whole, so
     * a module name never reaches the shell unquoted.
     */
    public function testTheModulePathIsUsedExactlyAsQuoted(): void
    {
        foreach (self::everyCommand() as $label => $command) {
            self::assertStringContainsString(self::MODULE, $command, $label);
        }
    }
}

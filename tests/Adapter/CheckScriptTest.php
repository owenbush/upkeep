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
    private const MODULE = '"$DDEV_DOCROOT/$DRUPAL_PROJECTS_PATH"/widget';

    /**
     * @return list<string>
     */
    private static function lines(string $script): array
    {
        return explode("\n", $script);
    }

    /** Index of the first line containing $needle, or -1. */
    private static function at(string $script, string $needle): int
    {
        foreach (self::lines($script) as $i => $line) {
            if (str_contains($line, $needle)) {
                return $i;
            }
        }

        return -1;
    }

    // ----------------------------------------------------------- phpstan

    /**
     * The bug, stated as an assertion: the analysis runs from inside the
     * module, so PHPStan finds the module's own config the way CI lets it.
     */
    public function testPhpStanRunsFromInsideTheModule(): void
    {
        $script = CheckScript::phpStan(self::MODULE);

        $cd = self::at($script, 'cd "$MODULE"');
        $analyze = self::at($script, 'phpstan analyze .');

        self::assertGreaterThan(-1, $cd, 'the analysis must not run from the project root');
        self::assertGreaterThan($cd, $analyze);
    }

    public function testAModulesOwnPhpStanConfigIsPreferredOverTheTemplate(): void
    {
        $script = CheckScript::phpStan(self::MODULE);

        // PHPStan's own precedence, and the order CI tests them in.
        foreach (['phpstan.neon', 'phpstan.neon.dist', 'phpstan.dist.neon'] as $name) {
            self::assertStringContainsString('[ -f "$MODULE/' . $name . '" ]', $script);
        }

        // Discovered, not passed: with the working directory inside the
        // module, PHPStan picks its own config up unaided — which is exactly
        // what CI relies on. `-c` is added only in the fallback branch, so the
        // one invocation carries it only when the else ran.
        self::assertStringContainsString(
            'phpstan analyze . --autoload-file="$ROOT/vendor/autoload.php" "$@"',
            $script,
        );
        self::assertGreaterThan(
            self::at($script, 'else'),
            self::at($script, '-c "$ROOT/phpstan.neon"'),
            'the template config is added in the fallback branch only',
        );
    }

    /**
     * The autoloader becomes load-bearing the moment the working directory
     * stops being the project root: PHPStan resolves Drupal's classes through
     * the site's autoloader and can no longer find it from inside the module.
     * CI passes it for the same reason.
     */
    public function testTheSitesAutoloaderIsPassedExplicitlyOnBothPaths(): void
    {
        $script = CheckScript::phpStan(self::MODULE);

        self::assertStringContainsString('--autoload-file="$ROOT/vendor/autoload.php"', $script);
        self::assertLessThan(
            self::at($script, 'cd "$MODULE"'),
            self::at($script, 'ROOT=$(pwd)'),
            'the project root has to be captured while it is still the working directory',
        );
    }

    public function testTheFallbackNamesTheTemplateExplicitlyBecauseTheCwdMoved(): void
    {
        $script = CheckScript::phpStan(self::MODULE);

        self::assertStringContainsString('-c "$ROOT/phpstan.neon"', $script);
        self::assertStringContainsString('BASELINE_PLACEHOLDER', $script, 'the template still needs its sed');
    }

    // ------------------------------------------------------------- phpcs

    public function testPhpCsRunsFromInsideTheModule(): void
    {
        $script = CheckScript::phpCs(self::MODULE);

        $cd = self::at($script, 'cd "$MODULE"');
        $run = self::at($script, 'phpcs ');

        self::assertGreaterThan(-1, $cd);
        self::assertGreaterThan($cd, $run);
    }

    public function testAModulesOwnRulesetIsPreferredOverTheTemplate(): void
    {
        $script = CheckScript::phpCs(self::MODULE);

        foreach (['phpcs.xml', 'phpcs.xml.dist', '.phpcs.xml', '.phpcs.xml.dist'] as $name) {
            self::assertStringContainsString('[ -f "$MODULE/' . $name . '" ]', $script);
        }

        self::assertStringContainsString('--standard="$ROOT/phpcs.xml.dist"', $script, 'the fallback');
    }

    /** Module-relative paths, which is what CI prints and what a person can read. */
    public function testReportPathsAreRelativeToTheModule(): void
    {
        self::assertStringContainsString('--basepath=.', CheckScript::phpCs(self::MODULE));
    }

    public function testTheDdevDirectoryStaysOutOfTheResults(): void
    {
        self::assertStringContainsString('--ignore=*/.ddev/*', CheckScript::phpCs(self::MODULE));
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
     *
     * So every write happens at the project root, before the cd.
     */
    public function testTheFallbackNeverWritesIntoTheModuleCheckout(): void
    {
        foreach ([CheckScript::phpStan(self::MODULE), CheckScript::phpCs(self::MODULE)] as $script) {
            $lines = self::lines($script);
            $cds = array_keys(array_filter($lines, static fn (string $l): bool => str_contains($l, 'cd "$MODULE"')));

            // Exactly one, so "before the cd" is a statement about the whole
            // script rather than about whichever branch happens to run.
            self::assertCount(1, $cds, 'one cd, or this property cannot be checked at all');
            $cd = $cds[0];

            foreach ($lines as $i => $line) {
                if (preg_match('/\b(curl|sed -i|touch)\b/', $line) !== 1) {
                    continue;
                }
                self::assertLessThan(
                    $cd,
                    $i,
                    'writes must happen at the project root, never inside the module: ' . trim($line),
                );
            }
        }
    }

    /**
     * Extra arguments ride the positional parameters, not a string.
     *
     * `"$@"` is empty when there are none and stays quoted when there are;
     * an unquoted `$CONFIG` would split on whitespace in a path.
     */
    public function testExtraArgumentsAreCarriedQuoted(): void
    {
        foreach ([CheckScript::phpStan(self::MODULE), CheckScript::phpCs(self::MODULE)] as $script) {
            self::assertStringContainsString('set --', $script);
            self::assertStringContainsString('"$@"', $script);
        }
    }

    /** A shell script that stops at the first failure rather than carrying on. */
    public function testBothScriptsAbortOnTheFirstFailure(): void
    {
        self::assertStringStartsWith('set -eu', CheckScript::phpStan(self::MODULE));
        self::assertStringStartsWith('set -eu', CheckScript::phpCs(self::MODULE));
    }

    /**
     * The module path arrives already shell-quoted and is used as a variable
     * from then on, so a module name is never spliced into a `[ -f ... ]` test
     * or a cd unquoted.
     */
    public function testTheModulePathIsBoundOnceAndUsedAsAVariable(): void
    {
        foreach ([CheckScript::phpStan(self::MODULE), CheckScript::phpCs(self::MODULE)] as $script) {
            self::assertSame(1, substr_count($script, self::MODULE), 'bound exactly once');
            self::assertStringContainsString('MODULE=' . self::MODULE, $script);
            self::assertStringContainsString('cd "$MODULE"', $script);
        }
    }
}

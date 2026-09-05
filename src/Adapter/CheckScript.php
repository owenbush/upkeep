<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The shell bodies for the two checks that are configured by a file the module
 * may ship itself.
 *
 * Separated out because getting them wrong is silent. Both phpstan and phpcs
 * discover their configuration from the **current working directory**, and
 * upkeep ran them from the project root — where the only config present is the
 * gitlab_templates default upkeep had just downloaded. A module shipping its
 * own `phpstan.neon` or `phpcs.xml.dist` — its own level, baseline, ignores,
 * bootstrap, ruleset — had all of it ignored, and the check reported a verdict
 * against rules the project does not use.
 *
 * drupal.org's CI does the opposite, and deliberately: `.phpstan-base` runs
 * `cd $DRUPAL_PROJECT_FOLDER` first and only fetches the template `else`, and
 * `.phpcs-base` runs `cd $CI_PROJECT_DIR` and looks for `{.,}phpcs.xml{.dist,}`
 * before falling back. So these mirror that: **the module's own configuration
 * wins, and the template is the fallback it is in CI.**
 *
 * The fallback config stays at the project root rather than being written into
 * the module. CI writes it beside the code because the container is thrown
 * away; here the module directory is a real git checkout that the next command
 * inspects for dirtiness, and dropping two untracked files into it would make
 * every subsequent apply refuse.
 */
final readonly class CheckScript
{
    private const TEMPLATES = 'https://git.drupalcode.org/project/gitlab_templates/-/raw/default-ref/assets';

    /**
     * PHPStan, run from inside the module exactly as `.phpstan-base` does.
     *
     * `--autoload-file` becomes load-bearing the moment the working directory
     * stops being the project root: PHPStan resolves Drupal's classes through
     * the site's autoloader, and from inside the module it can no longer find
     * it by itself. CI passes it for the same reason.
     *
     * @param string $modulePath the module's in-container path, already quoted
     */
    public static function phpStan(string $modulePath): string
    {
        return implode("\n", [
            'set -eu',
            'ROOT=$(pwd)',
            sprintf('MODULE=%s', $modulePath),
            // Extra arguments are carried in the positional parameters rather
            // than a string, so `"$@"` stays quoted and expands to nothing
            // when there are none. One cd and one invocation: with two of
            // each it is no longer checkable that nothing is written into the
            // module after the working directory moves into it.
            'set --',
            // PHPStan's own precedence, and the order CI tests them in.
            'if [ -f "$MODULE/phpstan.neon" ] || [ -f "$MODULE/phpstan.neon.dist" ] '
            . '|| [ -f "$MODULE/phpstan.dist.neon" ]; then',
            '  echo "Using the module\'s own PHPStan configuration, as CI does."',
            'else',
            '  echo "Module ships no PHPStan configuration; using the gitlab_templates default."',
            sprintf('  test -e phpstan.neon || curl -sSOL %s/phpstan.neon', self::TEMPLATES),
            "  sed -i 's/BASELINE_PLACEHOLDER/phpstan-baseline.neon/g' phpstan.neon",
            '  test -e phpstan-baseline.neon || touch phpstan-baseline.neon',
            '  set -- -c "$ROOT/phpstan.neon"',
            'fi',
            'cd "$MODULE"',
            'phpstan analyze . --autoload-file="$ROOT/vendor/autoload.php" "$@"',
        ]);
    }

    /**
     * PHPCS, run from inside the module exactly as `.phpcs-base` does.
     *
     * `--basepath=.` so reported paths are module-relative, which is both what
     * CI prints and the only form that means anything to somebody reading a
     * check result rather than a container filesystem.
     *
     * @param string $modulePath the module's in-container path, already quoted
     */
    public static function phpCs(string $modulePath): string
    {
        return implode("\n", [
            'set -eu',
            'ROOT=$(pwd)',
            sprintf('MODULE=%s', $modulePath),
            'set --',
            // The four names phpcs itself looks for, in CI's order.
            'if [ -f "$MODULE/phpcs.xml" ] || [ -f "$MODULE/phpcs.xml.dist" ] '
            . '|| [ -f "$MODULE/.phpcs.xml" ] || [ -f "$MODULE/.phpcs.xml.dist" ]; then',
            '  echo "Using the module\'s own PHPCS ruleset, as CI does."',
            'else',
            '  echo "Module ships no PHPCS ruleset; using the gitlab_templates default."',
            sprintf('  test -e phpcs.xml.dist || curl -sSOL %s/phpcs.xml.dist', self::TEMPLATES),
            '  set -- --standard="$ROOT/phpcs.xml.dist"',
            'fi',
            'cd "$MODULE"',
            'phpcs -s --report-full --report-summary --report-source --basepath=. '
            . '--ignore=*/.ddev/* "$@" .',
        ]);
    }
}

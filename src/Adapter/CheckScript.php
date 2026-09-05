<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The command lines for the two checks a module can configure for itself.
 *
 * **Single-line, and that is a hard requirement rather than a style.**
 * `ddev exec` re-joins the arguments it is given into one command string for
 * the container's shell, and newlines do not survive that. A multi-line script
 * arrives as a single line, which turns
 *
 *     set -eu
 *     ROOT=$(pwd)
 *     MODULE=…
 *
 * into `set -eu ROOT=$(pwd) MODULE=…` — one `set` call that enables `-u` and
 * swallows both assignments as positional parameters. The first `"$MODULE"`
 * after it then dies with `MODULE: unbound variable`, which is exactly how
 * this was found: phpstan and phpcs failed on a real run while the checks that
 * do not go through a script passed. The earlier one-command-per-line scripts
 * survived because no line depended on another; the moment there was state and
 * an `if`, joining broke it.
 *
 * So there is no control flow here at all. Which configuration to use is
 * decided in PHP, from a probe the adapter runs first, and what reaches the
 * container is a flat `&&` chain.
 *
 * The behaviour being preserved: both tools discover their configuration from
 * the working directory, and CI runs them from **inside the module** —
 * `.phpstan-base` opens with `cd $DRUPAL_PROJECT_FOLDER`, `.phpcs-base` with
 * `cd $CI_PROJECT_DIR`. So a module's own config wins and the gitlab_templates
 * default is the fallback, as it is in CI.
 *
 * The fallback config stays at the project root and is never written into the
 * module. CI writes it beside the code because the container is thrown away;
 * here the module directory is a git checkout whose cleanliness the next
 * applyPatch or startWork refuses on.
 */
final readonly class CheckScript
{
    private const TEMPLATES = 'https://git.drupalcode.org/project/gitlab_templates/-/raw/default-ref/assets';

    /** PHPStan's own precedence, and the order CI tests them in. */
    public const PHPSTAN_CONFIGS = ['phpstan.neon', 'phpstan.neon.dist', 'phpstan.dist.neon'];

    /** The four names phpcs looks for, in CI's order. */
    public const PHPCS_CONFIGS = ['phpcs.xml', 'phpcs.xml.dist', '.phpcs.xml', '.phpcs.xml.dist'];

    /**
     * A single-line test for "does the module ship any of these?".
     *
     * Exit status is the whole answer, so there is nothing to parse and no
     * pipeline to survive joining.
     *
     * @param list<string> $names
     */
    public static function configProbe(string $modulePath, array $names): string
    {
        return implode(' || ', array_map(
            static fn (string $name): string => sprintf('test -f %s/%s', $modulePath, $name),
            $names,
        ));
    }

    /**
     * PHPStan, run from inside the module exactly as `.phpstan-base` does.
     *
     * `--autoload-file` becomes load-bearing the moment the working directory
     * stops being the project root: PHPStan resolves Drupal's classes through
     * the site's autoloader and cannot find it from inside the module. CI
     * passes it for the same reason.
     *
     * @param string $modulePath the module's in-container path, already quoted
     * @param bool   $ownConfig  whether the module ships its own configuration
     */
    public static function phpStan(string $modulePath, bool $ownConfig): string
    {
        // ROOT is captured before the cd, as its own command in the chain —
        // never as a trailing word on another, which is what `set -eu` turned
        // it into.
        $analyse = sprintf(
            'ROOT=$(pwd) && cd %s && phpstan analyze . --autoload-file="$ROOT/vendor/autoload.php"',
            $modulePath,
        );

        if ($ownConfig) {
            return $analyse;
        }

        return implode(' && ', [
            // Braced, because `&&` and `||` bind equally and left to right:
            // ungrouped, a failed download would fall through to the `||` of
            // the *next* step and end up analysing against a config that was
            // never fetched.
            sprintf('{ test -e phpstan.neon || curl -sSOL %s/phpstan.neon; }', self::TEMPLATES),
            "sed -i 's/BASELINE_PLACEHOLDER/phpstan-baseline.neon/g' phpstan.neon",
            '{ test -e phpstan-baseline.neon || touch phpstan-baseline.neon; }',
            $analyse . ' -c "$ROOT/phpstan.neon"',
        ]);
    }

    /**
     * PHPCS, run from inside the module exactly as `.phpcs-base` does.
     *
     * `--basepath=.` so reported paths are module-relative, which is what CI
     * prints and the only form that means anything to somebody reading a check
     * result rather than a container filesystem.
     *
     * @param string $modulePath the module's in-container path, already quoted
     * @param bool   $ownConfig  whether the module ships its own ruleset
     */
    public static function phpCs(string $modulePath, bool $ownConfig): string
    {
        $report = "-s --report-full --report-summary --report-source --basepath=. --ignore='*/.ddev/*'";
        $run = sprintf('ROOT=$(pwd) && cd %s && phpcs %s', $modulePath, $report);

        if ($ownConfig) {
            return $run . ' .';
        }

        return implode(' && ', [
            sprintf('{ test -e phpcs.xml.dist || curl -sSOL %s/phpcs.xml.dist; }', self::TEMPLATES),
            $run . ' --standard="$ROOT/phpcs.xml.dist" .',
        ]);
    }
}

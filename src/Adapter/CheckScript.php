<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The command lines for the two checks a module can configure for itself.
 *
 * **No shell variable of our own, and no `cd`.** Both rules are paid for.
 *
 * `ddev exec` re-joins its arguments and hands the result to a shell that
 * expands the string *before* the inner shell runs it, so an assignment and
 * its use in the same command cannot work: `ROOT=$(pwd) && … "$ROOT/x"`
 * expands `$ROOT` while the string is being built, where it is unset, and
 * dies with `ROOT: unbound variable`. The same thing killed an earlier
 * `MODULE=…`. Only variables already in the container's environment —
 * `$DDEV_DOCROOT`, `$DRUPAL_PROJECTS_PATH` — survive, because expanding them
 * early produces the same value. That is why the long-standing scripts worked
 * and both rewrites did not.
 *
 * The `cd` had to go for a different reason. CI runs these from inside the
 * module because in CI the module repo root *is* where `composer install`
 * put `vendor/`. Under ddev-drupal-contrib the module is a checkout symlinked
 * into a site whose `vendor/` lives at the project root, so from inside the
 * module every vendor-relative path in a ruleset breaks — observed as
 * `Referenced sniff "./vendor/drupal/coder/coder_sniffer/Drupal" does not
 * exist` against a real module's own phpcs.xml.dist.
 *
 * So the working directory stays at the project root, where `vendor/` is, and
 * the module's own configuration is named explicitly instead of discovered.
 * That is the same shape as verifying it by hand with an explicit config flag,
 * and it keeps the part that matters: **a module's own configuration is used,
 * and the gitlab_templates default is only the fallback** — which is the
 * behaviour CI has and upkeep did not.
 *
 * The fallback config is written at the project root and never into the
 * module: that directory is a git checkout whose cleanliness the next
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
     * A single test for "does the module ship this one?".
     *
     * One command per candidate, run in order, because the *name* is the
     * answer — it has to be passed to the tool afterwards, and parsing it out
     * of shell output would put a second fragile thing where this one already
     * is. Exit status is all that is read.
     */
    public static function configProbe(string $modulePath, string $name): string
    {
        return sprintf('test -f %s/%s', $modulePath, $name);
    }

    /**
     * PHPStan against the module, with whichever configuration applies.
     *
     * Relative paths inside a neon file resolve against the file's own
     * directory, so a module's config keeps meaning what it means from here.
     *
     * @param string  $modulePath the module's in-container path, already quoted
     * @param ?string $ownConfig  the config the module ships, when it ships one
     */
    public static function phpStan(string $modulePath, ?string $ownConfig): string
    {
        if ($ownConfig !== null) {
            return sprintf('phpstan analyze %1$s -c %1$s/%2$s', $modulePath, $ownConfig);
        }

        return implode(' && ', [
            // Braced, because `&&` and `||` bind equally and left to right:
            // ungrouped, a failed download falls through to the next step's
            // `||` and the analysis runs against a config nobody fetched.
            sprintf('{ test -e phpstan.neon || curl -sSOL %s/phpstan.neon; }', self::TEMPLATES),
            "sed -i 's/BASELINE_PLACEHOLDER/phpstan-baseline.neon/g' phpstan.neon",
            '{ test -e phpstan-baseline.neon || touch phpstan-baseline.neon; }',
            sprintf('phpstan analyze %s -c phpstan.neon', $modulePath),
        ]);
    }

    /**
     * PHPCS against the module, with whichever ruleset applies.
     *
     * `--basepath` is the module, so reported paths are module-relative as
     * CI's are (`--basepath=$DRUPAL_PROJECT_FOLDER` there) rather than absolute
     * container paths nobody can act on.
     *
     * @param string  $modulePath the module's in-container path, already quoted
     * @param ?string $ownConfig  the ruleset the module ships, when it ships one
     */
    public static function phpCs(string $modulePath, ?string $ownConfig): string
    {
        $report = sprintf(
            "-s --report-full --report-summary --report-source --basepath=%s --ignore='*/.ddev/*'",
            $modulePath,
        );

        if ($ownConfig !== null) {
            return sprintf('phpcs %s --standard=%s/%s %s', $report, $modulePath, $ownConfig, $modulePath);
        }

        return implode(' && ', [
            sprintf('{ test -e phpcs.xml.dist || curl -sSOL %s/phpcs.xml.dist; }', self::TEMPLATES),
            sprintf('phpcs %s --standard=phpcs.xml.dist %s', $report, $modulePath),
        ]);
    }
}

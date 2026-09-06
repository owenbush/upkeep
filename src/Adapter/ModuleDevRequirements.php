<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The dev dependencies a module declares for itself.
 *
 * Needed the moment upkeep started honouring a module's own phpcs and phpstan
 * configuration, because those configurations reference packages the *module*
 * requires rather than the site. field_visibility_conditions' ruleset says
 *
 *     <rule ref="./vendor/phpcompatibility/php-compatibility/PHPCompatibility"/>
 *
 * and its composer.json puts `phpcompatibility/php-compatibility` in
 * `require-dev`. In CI that resolves because `composer install` runs in the
 * module repository, so the module's dev dependencies are in its own vendor/.
 * Under ddev-drupal-contrib the module is a path repository of the site, and
 * **composer does not install a path dependency's require-dev** — so the sniff
 * was simply absent, and the check failed with "Referenced sniff … does not
 * exist" against a ruleset that is perfectly correct.
 *
 * Using a module's configuration and not installing what that configuration
 * needs is half a feature. This is the other half.
 *
 * Read from the file rather than asked of composer: the working copy is on
 * disk already, and shelling out for something a `json_decode` answers would
 * add a container round trip to every provision.
 */
final readonly class ModuleDevRequirements
{
    /**
     * The package names in the module's `require-dev`.
     *
     * Platform requirements are dropped — `php`, `ext-*` and `lib-*` are not
     * packages, and asking composer to require them into the site would fail a
     * provision over something no install can satisfy.
     *
     * Anything unreadable yields none. A module with no composer.json, or a
     * malformed one, is not a reason to refuse to check it: the checks still
     * run, and a configuration referencing something missing reports its own
     * clear error.
     *
     * @return list<string>
     */
    public static function of(string $moduleDir): array
    {
        $path = rtrim($moduleDir, '/') . '/composer.json';
        if (!is_file($path) || !is_readable($path)) {
            return [];
        }

        try {
            $manifest = json_decode((string) file_get_contents($path), true, 512, \JSON_THROW_ON_ERROR);
        } catch (\JsonException) {
            return [];
        }

        $require = \is_array($manifest) ? ($manifest['require-dev'] ?? null) : null;
        if (!\is_array($require)) {
            return [];
        }

        $packages = [];
        foreach (array_keys($require) as $package) {
            $package = (string) $package;
            if (str_contains($package, '/')) {
                $packages[] = $package;
            }
        }

        return $packages;
    }

    /**
     * What to say when they cannot be installed.
     *
     * A warning rather than a refusal, and the reason is precise: without them
     * the module's own configuration may reference a missing sniff, and phpcs
     * says so itself, loudly, naming the file. Failing the whole provision
     * instead would take down phpunit, the install check and the smoke test
     * over a linting dependency — the checks that were going to pass.
     *
     * @param list<string> $packages
     */
    public static function unavailable(array $packages): string
    {
        return sprintf(
            'Could not install the module\'s own dev dependencies (%s). Its phpcs or phpstan configuration may '
            . 'reference a sniff or extension that is now missing; the check will name it if so.',
            implode(', ', $packages),
        );
    }
}

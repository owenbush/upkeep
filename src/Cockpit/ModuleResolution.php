<?php

declare(strict_types=1);

namespace Upkeep\Cockpit;

use Upkeep\Adapter\ProjectName;

/**
 * Which module a command is about, whether or not the cockpit watches it.
 *
 * The registry does two jobs and they were the same job: it says how to find a
 * module on GitLab and which cores to test it on, *and* it says which modules
 * the dashboard surveys. Only the second is what a maintainer means by curating
 * one — see `docs/any-module.md`.
 *
 * The first job is now largely derivable. `project/<name>` is drupal.org's
 * convention, and upkeep already assumed exactly that in `PruneExecutor` for
 * anything it found outside the registry; the cores a run can use are the ones
 * base artifacts exist for, which is a fact about the disk rather than about a
 * config file. So a module nobody registered is workable, and the registry
 * becomes the watchlist it was always being used as.
 *
 * What is *not* dropped is the refusal. "Module is not registered" was
 * protecting configuration this can now derive, but it was also the only thing
 * catching `pathuato`, and quietly turning a typo into a clone of
 * `project/pathuato` would make the tool worse rather than freer. So a name
 * that cannot be a Drupal machine name is refused outright, and a derived name
 * that turns out not to exist gets told what it nearly matched (see
 * `didYouMean()`).
 */
final readonly class ModuleResolution
{
    /**
     * The module a command should act on.
     *
     * A registry entry wins: a maintainer's `core_versions` is a deliberate
     * statement about what they support and outranks anything inferred.
     *
     * @param array<string, Module> $registered
     * @param list<string>          $coresOnDisk base artifact versions, ascending
     *
     * @throws RegistryException when the name cannot be a module at all
     */
    public static function resolve(array $registered, string $name, array $coresOnDisk): Module
    {
        if (isset($registered[$name])) {
            return $registered[$name];
        }

        if (!ProjectName::isModuleName($name)) {
            throw new RegistryException(sprintf(
                'Not a Drupal module machine name: "%s". Expected lower-case letters, digits and underscores, '
                . 'starting with a letter — e.g. "pathauto" or "field_visibility_conditions".',
                $name,
            ));
        }

        if ($coresOnDisk === []) {
            throw new RegistryException(sprintf(
                "\"%s\" is not in the registry, so upkeep works out what to check it against — and there are no "
                . "base artifacts to check against yet.\nBuild one first:\n  upkeep base-artifacts:build "
                . '--version=11',
                $name,
            ));
        }

        return new Module($name, self::projectFor($name), $coresOnDisk);
    }

    /**
     * The drupal.org project path for a module machine name.
     *
     * The convention, and already relied on elsewhere in the tool. A module
     * whose project path is *not* this is exactly what a registry entry is for.
     */
    public static function projectFor(string $name): string
    {
        return 'project/' . $name;
    }

    /**
     * Whether this module came from the registry rather than being derived.
     *
     * @param array<string, Module> $registered
     */
    public static function isRegistered(array $registered, string $name): bool
    {
        return isset($registered[$name]);
    }

    /**
     * Why a module's GitLab project could not be resolved — and, when the name
     * was derived rather than watched, what it probably should have been.
     *
     * One wording for both places that report this, because two sentences for
     * one condition is how a tool comes to look like several tools. And the
     * suggestion belongs *here*, at the failure, rather than at resolution
     * time: until drupal.org says there is nothing at `project/<name>`, a name
     * upkeep has never heard of is an ordinary thing to ask about, which is
     * the entire point of the registry becoming a watchlist.
     *
     * A watched module that fails to resolve gets no suggestion. Its name is
     * one the maintainer wrote down deliberately, so the problem is the API's
     * answer, not the spelling.
     *
     * @param array<string, Module> $registered
     */
    public static function projectFailure(array $registered, Module $module, string $apiMessage): string
    {
        $message = sprintf(
            'Cannot resolve the GitLab project for module "%s" (%s): %s',
            $module->name,
            $module->project,
            $apiMessage,
        );

        if (self::isRegistered($registered, $module->name)) {
            return $message;
        }

        $meant = self::didYouMean($registered, $module->name);

        return $meant === null ? $message : $message . sprintf("\nDid you mean \"%s\"?", $meant);
    }

    /**
     * The registered name a failed lookup most likely meant, if any.
     *
     * Only consulted when the project could not be resolved, because until
     * then a name upkeep has never heard of is an ordinary thing to ask about
     * — that is the whole point of the change. It becomes a typo only once
     * drupal.org says there is nothing there.
     *
     * Levenshtein rather than a prefix match: the mistakes that happen are
     * transpositions and dropped letters (`pathuato`, `pathaut`), which a
     * prefix test misses entirely. The threshold scales with length so short
     * names are not matched to everything.
     *
     * @param array<string, Module> $registered
     */
    public static function didYouMean(array $registered, string $name): ?string
    {
        $best = null;
        $bestDistance = \PHP_INT_MAX;

        foreach (array_keys($registered) as $candidate) {
            $distance = levenshtein($name, (string) $candidate);
            if ($distance < $bestDistance) {
                $bestDistance = $distance;
                $best = (string) $candidate;
            }
        }

        if ($best === null) {
            return null;
        }

        return $bestDistance <= max(2, intdiv(mb_strlen($name), 4)) ? $best : null;
    }
}

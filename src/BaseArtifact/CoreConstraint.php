<?php

declare(strict_types=1);

namespace Upkeep\BaseArtifact;

use Composer\Semver\VersionParser;

/**
 * Which `drupal/recommended-project` a base artifact set is built from.
 *
 * `^12` resolves to nothing while Drupal 12 is in alpha — packagist carries
 * exactly one 12.x release, `12.0.0-alpha1` — so a build against it failed with
 * composer's own "could not find a matching version" and no hint that anything
 * could be done about it.
 *
 * That is not an edge case for this tool. A Drupal major spends months in alpha
 * and beta, and **that is precisely when compatibility work happens**: the
 * Project Update Bot compatibility merge requests upkeep's fast lane exists to
 * merge are about the *unreleased* core. Being unable to build an environment
 * for it means being unable to answer the question the tool is most often
 * asked.
 *
 * Deliberate rather than automatic. Falling back to a pre-release when a stable
 * constraint finds nothing would quietly build something different from what
 * was asked for, and a base artifact set is the thing every later verdict is
 * measured against — the one place a silent substitution is least acceptable.
 * So the stability is a flag, and the resolved version is recorded in the meta
 * either way (`ArtifactMeta::$coreVersion` comes from the lock, so an alpha
 * build reads `12.0.0-alpha1` and cannot be mistaken for a release).
 */
final readonly class CoreConstraint
{
    /**
     * Composer's stability names, loosest first.
     *
     * A stability is a *minimum*: `^12@alpha` still prefers a stable 12 once
     * one exists, so a cockpit built this way does not stay on the alpha after
     * release — it stops being pre-release at the next rebuild.
     */
    public const STABILITIES = ['dev', 'alpha', 'beta', 'RC', 'stable'];

    /** The package constraint `composer create-project` is given. */
    public static function for(string $coreMajor, ?string $stability): string
    {
        $constraint = sprintf('drupal/recommended-project:^%s', $coreMajor);

        return $stability === null ? $constraint : $constraint . '@' . $stability;
    }

    /**
     * @throws BuildException when the stability is not one composer knows
     */
    public static function assertStability(?string $stability): void
    {
        if ($stability === null || \in_array($stability, self::STABILITIES, true)) {
            return;
        }

        throw new BuildException(sprintf(
            'Unknown stability "%s". Composer knows: %s.',
            $stability,
            implode(', ', self::STABILITIES),
        ));
    }

    /**
     * The stability of a resolved core version, or null when it is stable.
     *
     * The maintenance-free half. Building against a pre-release is a decision
     * — hence the flag — but everything *downstream* of that decision should
     * follow the tree that was actually built rather than ask again. The
     * environment records the base artifact's resolved core version
     * (`EnvironmentMeta::$seedCoreVersion`, e.g. `12.0.0-alpha1`), so the
     * toolchain constraint can be derived from it: `drupal/core-dev:^12` also
     * resolves to nothing while 12 is in alpha, and would have failed the
     * first check on an environment built with `--stability=alpha`.
     *
     * Composer's own parser answers this, so there is no table of majors or
     * suffixes to keep current — 13, 14 and anything after them work with no
     * change here.
     */
    public static function stabilityOf(string $coreVersion): ?string
    {
        $stability = VersionParser::parseStability($coreVersion);

        return $stability === 'stable' ? null : $stability;
    }

    /**
     * A toolchain package constraint carrying whatever stability that core
     * version implies: `drupal/core-dev:^12@alpha` from `12.0.0-alpha1`, and
     * plain `drupal/core-dev:^11` from a release.
     *
     * Only templates that interpolate the core major get the suffix. The
     * stability belongs to *core's* constraint, and `drupal/coder@alpha` —
     * with no version constraint at all — would tell composer that any alpha
     * of a package unrelated to the seeded core is acceptable.
     */
    public static function packageFor(string $package, string $coreMajor, string $coreVersion): string
    {
        $constraint = sprintf($package, $coreMajor);
        $stability = self::stabilityOf($coreVersion);

        if ($stability === null || !str_contains($package, '%s')) {
            return $constraint;
        }

        return $constraint . '@' . $stability;
    }

    /**
     * What to add to a failed resolve, when the run asked for stable releases
     * only.
     *
     * Null once a stability *was* given: the constraint is then not the
     * obvious suspect, and repeating the suggestion that was already taken
     * would bury whatever composer actually said.
     */
    public static function unresolvableHint(string $coreMajor, ?string $stability): ?string
    {
        if ($stability !== null) {
            return null;
        }

        return sprintf(
            "\nIf core %s has no stable release yet, there is nothing for \"^%s\" to resolve to. A Drupal major "
            . "spends months in alpha and beta, which is when compatibility work happens — to build against the "
            . "pre-release:\n  upkeep base-artifacts:build --version=%s --stability=alpha",
            $coreMajor,
            $coreMajor,
            $coreMajor,
        );
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\BaseArtifact;

use PHPUnit\Framework\TestCase;
use Upkeep\BaseArtifact\BuildException;
use Upkeep\BaseArtifact\CoreConstraint;

/**
 * Which `drupal/recommended-project` a base artifact set is built from.
 *
 * `^12` resolves to nothing while Drupal 12 is in alpha — packagist carried
 * exactly one 12.x release, `12.0.0-alpha1`, when this was written — so a build
 * failed with composer's own "could not find a matching version" and nothing
 * suggesting a flag existed.
 *
 * That is not an edge case here. A Drupal major spends months in alpha and
 * beta, and that is *when compatibility work happens*: the Project Update Bot
 * merge requests upkeep's fast lane exists to merge are about the unreleased
 * core. Being unable to build an environment for it means being unable to
 * answer the question the tool is most often asked.
 */
final class CoreConstraintTest extends TestCase
{
    public function testTheDefaultIsAStableConstraint(): void
    {
        self::assertSame('drupal/recommended-project:^11', CoreConstraint::for('11', null));
    }

    /**
     * Deliberate, never inferred. Falling back to a pre-release when a stable
     * constraint finds nothing would quietly build something other than what
     * was asked for — and a base artifact set is what every later verdict is
     * measured against, which makes it the worst place for a silent
     * substitution.
     */
    public function testAStabilityIsAppendedOnlyWhenAskedFor(): void
    {
        self::assertSame('drupal/recommended-project:^12@alpha', CoreConstraint::for('12', 'alpha'));
        self::assertSame('drupal/recommended-project:^12@dev', CoreConstraint::for('12', 'dev'));
    }

    public function testTheStabilitiesAreComposersOwn(): void
    {
        self::assertSame(['dev', 'alpha', 'beta', 'RC', 'stable'], CoreConstraint::STABILITIES);

        // And every one of them is accepted, plus the absent case: a name
        // composer knows that this refused would be a restriction upkeep
        // invented rather than one composer imposes.
        $accepted = [];
        foreach ([...CoreConstraint::STABILITIES, null] as $stability) {
            CoreConstraint::assertStability($stability);
            $accepted[] = $stability;
        }

        self::assertSame(['dev', 'alpha', 'beta', 'RC', 'stable', null], $accepted);
    }

    /**
     * A misspelling is refused here rather than handed to composer, which
     * answers a bad `@` suffix with a parse error about the whole constraint.
     */
    public function testAnUnknownStabilityIsRefusedNamingTheRealOnes(): void
    {
        $this->expectException(BuildException::class);
        $this->expectExceptionMessage('Composer knows: dev, alpha, beta, RC, stable');

        CoreConstraint::assertStability('unstable');
    }

    /**
     * The hint that was missing. Composer's output says a version could not be
     * found; it does not say that the major has no stable release yet, or that
     * there is a flag for exactly that.
     */
    public function testAFailedStableResolveSuggestsThePreReleaseFlag(): void
    {
        $hint = CoreConstraint::unresolvableHint('12', null);

        self::assertNotNull($hint);
        self::assertStringContainsString('no stable release yet', $hint);
        self::assertStringContainsString('base-artifacts:build --version=12 --stability=alpha', $hint);
    }

    /**
     * And nothing once a stability was given: the constraint is then not the
     * obvious suspect, and repeating the advice already taken would bury
     * whatever composer actually said.
     */
    public function testNoHintWhenAStabilityWasAlreadyAskedFor(): void
    {
        self::assertNull(CoreConstraint::unresolvableHint('12', 'alpha'));
    }

    // ------------------------------------------- what follows from the build

    /**
     * The maintenance-free half, and the answer to "does this need a flag on
     * every other command too?" — no. Building against a pre-release is a
     * decision, so it is a flag; everything downstream follows the tree that
     * was actually built. Composer's own parser reads the stability out of the
     * resolved version, so there is no table of majors to keep current.
     */
    public function testTheStabilityOfAResolvedCoreVersionIsRead(): void
    {
        self::assertSame('alpha', CoreConstraint::stabilityOf('12.0.0-alpha1'));
        self::assertSame('beta', CoreConstraint::stabilityOf('13.0.0-beta2'));
        self::assertSame('RC', CoreConstraint::stabilityOf('13.0.0-rc1'));
        self::assertSame('dev', CoreConstraint::stabilityOf('14.x-dev'));
    }

    /** Null for a release, so a stable core adds nothing to any constraint. */
    public function testAReleasedCoreHasNoStabilityToCarry(): void
    {
        self::assertNull(CoreConstraint::stabilityOf('11.4.6'));
    }

    /**
     * `drupal/core-dev:^12` resolves to nothing for the same reason
     * `drupal/recommended-project:^12` does, so an environment seeded from an
     * alpha base artifact failed at its first check — after provisioning.
     */
    public function testTheToolchainConstraintFollowsTheSeededCore(): void
    {
        self::assertSame(
            'drupal/core-dev:^12@alpha',
            CoreConstraint::packageFor('drupal/core-dev:^%s', '12', '12.0.0-alpha1'),
        );
        self::assertSame(
            'drupal/core-dev:^11',
            CoreConstraint::packageFor('drupal/core-dev:^%s', '11', '11.4.6'),
        );
    }

    /**
     * And nothing else in the toolchain gets it. The stability belongs to
     * core's constraint; `drupal/coder@alpha` carries no version constraint at
     * all, so it would tell composer that an alpha of a package with nothing
     * to do with the seeded core is acceptable.
     */
    public function testAPackageWithNoCoreConstraintNeverCarriesTheStability(): void
    {
        self::assertSame('drupal/coder', CoreConstraint::packageFor('drupal/coder', '12', '12.0.0-alpha1'));
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Cockpit;

use PHPUnit\Framework\TestCase;
use Upkeep\Cockpit\Module;
use Upkeep\Cockpit\ModuleResolution;
use Upkeep\Cockpit\RegistryException;

/**
 * The registry as a watchlist rather than a gate.
 *
 * It was doing two jobs and they were the same job: how to find a module on
 * GitLab and which cores to test it on, *and* which modules the dashboard
 * surveys. Only the second is what a maintainer means by curating one, and the
 * first is now derivable — `project/<name>` is drupal.org's convention (upkeep
 * already assumed exactly that in PruneExecutor), and the cores a run can use
 * are the ones base artifacts exist for, which is a fact about the disk.
 *
 * So a module nobody registered is workable. What is deliberately *not* dropped
 * is the refusal: "not registered" was the only thing catching `pathuato`, and
 * turning a typo into a clone of `project/pathuato` would make the tool worse
 * rather than freer. See `docs/any-module.md`.
 */
final class ModuleResolutionTest extends TestCase
{
    /** @return array<string, Module> */
    private static function registry(): array
    {
        return [
            'pathauto' => new Module('pathauto', 'project/pathauto', ['10', '11']),
            'token' => new Module('token', 'project/token', ['11']),
        ];
    }

    /**
     * A registry entry wins outright. `core_versions` is a maintainer's
     * deliberate statement about what they support, and it outranks anything
     * inferred from what happens to be built on this machine.
     */
    public function testARegisteredModuleIsReturnedUntouched(): void
    {
        $module = ModuleResolution::resolve(self::registry(), 'pathauto', ['11', '12']);

        self::assertSame('project/pathauto', $module->project);
        self::assertSame(['10', '11'], $module->coreVersions, 'the registry, not the disk');
    }

    /** The change: a module nobody registered is a module you can work on. */
    public function testAnUnregisteredModuleIsDerivedRatherThanRefused(): void
    {
        $module = ModuleResolution::resolve(self::registry(), 'field_visibility_conditions', ['10', '11']);

        self::assertSame('field_visibility_conditions', $module->name);
        self::assertSame('project/field_visibility_conditions', $module->project);
        self::assertSame(['11', '10'], $module->coreVersions, 'what this machine can actually run');
    }

    /**
     * Newest first, because the first entry *is* the default.
     *
     * `selectCoreVersion()` documents `core_versions[0]` as what a run picks
     * without `--version`, and `ArtifactLayout::versionsOnDisk()` sorts
     * ascending — so passing it through unchanged made an unregistered module
     * answer for the *oldest* core built on the machine. Reported from a real
     * cockpit: `env:path paragraphs` said "on Drupal 10" with 11 also built.
     */
    public function testTheDefaultCoreIsTheNewestBuiltNotTheOldest(): void
    {
        $module = ModuleResolution::resolve(self::registry(), 'paragraphs', ['10', '11', '12']);

        self::assertSame(['12', '11', '10'], $module->coreVersions);
        self::assertSame('12', $module->coreVersions[0], 'what selectCoreVersion() will pick');
    }

    /**
     * A name that cannot be a Drupal machine name is refused before anything
     * goes near the network. There is no project to look for.
     */
    public function testAnImpossibleNameIsRefusedOutright(): void
    {
        foreach (['Pathauto', 'path-auto', '2fast', 'path auto', ''] as $name) {
            try {
                ModuleResolution::resolve(self::registry(), $name, ['11']);
                self::fail(sprintf('Expected "%s" to be refused.', $name));
            } catch (RegistryException $e) {
                self::assertStringContainsString('machine name', $e->getMessage(), $name);
            }
        }
    }

    /**
     * Nothing built means nothing to check against, and saying so beats
     * provisioning against a core version that was picked out of the air.
     * A registered module never reaches this: its own core_versions answer.
     */
    public function testWithNoBaseArtifactsTheRefusalNamesTheCommandThatFixesIt(): void
    {
        try {
            ModuleResolution::resolve(self::registry(), 'field_visibility_conditions', []);
            self::fail('Expected a refusal.');
        } catch (RegistryException $e) {
            self::assertStringContainsString('no base artifacts', $e->getMessage());
            self::assertStringContainsString('upkeep base-artifacts:build', $e->getMessage());
        }
    }

    public function testTheProjectPathIsTheDrupalOrgConvention(): void
    {
        self::assertSame('project/pathauto', ModuleResolution::projectFor('pathauto'));
    }

    public function testWhetherAModuleIsWatchedIsAnswerable(): void
    {
        self::assertTrue(ModuleResolution::isRegistered(self::registry(), 'token'));
        self::assertFalse(ModuleResolution::isRegistered(self::registry(), 'paragraphs'));
    }

    // ------------------------------------------------------------ typo help

    /**
     * The half of the old refusal worth keeping.
     *
     * It is consulted only once the project lookup has failed — until then a
     * name upkeep has never heard of is an ordinary thing to ask about, which
     * is the entire point. Transpositions and dropped letters are the mistakes
     * that actually happen, and a prefix match misses both.
     */
    public function testANearMissAgainstAWatchedModuleIsOffered(): void
    {
        self::assertSame('pathauto', ModuleResolution::didYouMean(self::registry(), 'pathuato'));
        self::assertSame('pathauto', ModuleResolution::didYouMean(self::registry(), 'pathaut'));
        self::assertSame('token', ModuleResolution::didYouMean(self::registry(), 'tokens'));
    }

    /**
     * And a module that is simply not watched is not a typo. Offering
     * "did you mean pathauto?" for `paragraphs` would be worse than silence:
     * it implies the name is wrong when the project genuinely may not exist.
     */
    public function testAnUnrelatedNameIsNotTreatedAsATypo(): void
    {
        self::assertNull(ModuleResolution::didYouMean(self::registry(), 'paragraphs'));
        self::assertNull(ModuleResolution::didYouMean(self::registry(), 'webform'));
    }

    /** With nothing watched there is nothing to have meant. */
    public function testAnEmptyRegistrySuggestsNothing(): void
    {
        self::assertNull(ModuleResolution::didYouMean([], 'pathuato'));
    }

    /**
     * The threshold scales with length, so a short name is not matched to
     * everything: `tokan` is a plausible slip for `token`, `abc` is not.
     */
    public function testTheSuggestionThresholdScalesWithTheNameLength(): void
    {
        self::assertSame('token', ModuleResolution::didYouMean(self::registry(), 'tokan'));
        self::assertNull(ModuleResolution::didYouMean(self::registry(), 'abc'));
    }
}

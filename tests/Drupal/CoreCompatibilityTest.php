<?php

declare(strict_types=1);

namespace Upkeep\Tests\Drupal;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Drupal\CoreCompatibility;

/**
 * Which Drupal cores a module branch declares.
 *
 * The fact the dashboard was missing, and the reason core was never part of a
 * row's identity in the first place: a *branch* supports several cores at
 * once. Every constraint below was read from a live module's info.yml.
 */
final class CoreCompatibilityTest extends TestCase
{
    /** The cores a registry might plausibly track. */
    private const TRACKED = ['9', '10', '11', '12'];

    /**
     * @param list<string> $expected
     */
    #[DataProvider('realConstraints')]
    public function testRealConstraintsResolveToTheCoresTheyDeclare(string $constraint, array $expected): void
    {
        $compatibility = CoreCompatibility::fromConstraint($constraint, self::TRACKED);

        self::assertNotNull($compatibility);
        self::assertSame($expected, $compatibility->cores);
    }

    /**
     * @return iterable<string, array{string, list<string>}>
     */
    public static function realConstraints(): iterable
    {
        // Read from git.drupalcode.org on 2026-09-03.
        yield 'pathauto 8.x-1.x' => ['^10.2 || ^11 || ^12', ['10', '11', '12']];
        yield 'token 8.x-1.x' => ['^10.3 || ^11 || ^12', ['10', '11', '12']];
        yield 'field_visibility_conditions 1.0.x' => ['^10 || ^11', ['10', '11']];
        yield 'field_visibility_conditions 2.0.x' => ['^10.1 || ^11 || ^12', ['10', '11', '12']];

        // Shapes that exist in contrib but not in the sample above.
        yield 'a single major' => ['^11', ['11']];
        yield 'a range' => ['>=10.2 <12', ['10', '11']];
        yield 'the legacy pipe spelling' => ['^10 || ^11 || ^12', ['10', '11', '12']];
    }

    /**
     * The reason this uses composer/semver rather than a regex over majors:
     * `^10.2` is a constraint on a *minor*, and a branch declaring it does
     * support core 10 — just not 10.0 or 10.1. Asked at major granularity, the
     * answer is yes, and only an interval intersection gets there.
     */
    public function testAMinorConstraintStillDeclaresItsMajor(): void
    {
        $compatibility = CoreCompatibility::fromConstraint('^10.2', ['10', '11']);

        self::assertNotNull($compatibility);
        self::assertTrue($compatibility->declares('10'));
        self::assertFalse($compatibility->declares('11'));
    }

    /**
     * Null is "cannot tell", never "supports nothing". Every caller falls back
     * to the tracked set whole, which is the behaviour that predates any of
     * this — an unreadable constraint is not evidence about a branch.
     */
    #[DataProvider('unreadableConstraints')]
    public function testAnUnreadableConstraintIsNullRatherThanEmpty(string $constraint): void
    {
        self::assertNull(CoreCompatibility::fromConstraint($constraint, self::TRACKED));
    }

    /**
     * @return iterable<string, array{string}>
     */
    public static function unreadableConstraints(): iterable
    {
        yield 'empty' => [''];
        yield 'whitespace' => ["  \n"];
        yield 'not a constraint at all' => ['garbage {{'];
        yield 'a sentence' => ['see the README'];
    }

    // ------------------------------------------------------------ info.yml

    public function testTheConstraintIsReadFromAnInfoYaml(): void
    {
        // The real shape, spacing and all.
        $info = "name : 'Pathauto'\n"
            . "description : 'Provides a mechanism for modules to automatically generate aliases.'\n"
            . "core_version_requirement: ^10.2 || ^11 || ^12\n"
            . "type: module\n\ndependencies:\n  - 'token:token'\n";

        $compatibility = CoreCompatibility::fromInfoYaml($info, self::TRACKED);

        self::assertNotNull($compatibility);
        self::assertSame(['10', '11', '12'], $compatibility->cores);
    }

    /** Quoted, because contrib spells it both ways. */
    public function testAQuotedConstraintIsRead(): void
    {
        $compatibility = CoreCompatibility::fromInfoYaml("core_version_requirement: '^10 || ^11'\n", self::TRACKED);

        self::assertNotNull($compatibility);
        self::assertSame(['10', '11'], $compatibility->cores);
    }

    /** An info.yml that does not declare one tells us nothing. */
    public function testAnInfoYamlWithoutTheKeyIsNull(): void
    {
        self::assertNull(CoreCompatibility::fromInfoYaml("name: Widget\ntype: module\n", self::TRACKED));
        self::assertNull(CoreCompatibility::fromInfoYaml('', self::TRACKED));
    }

    /**
     * A key that merely contains the name is not the key. `core_version_
     * requirement` is what Drupal reads; anything else is somebody's comment.
     */
    public function testAnAlmostMatchingKeyIsNotRead(): void
    {
        self::assertNull(CoreCompatibility::fromInfoYaml("# core_version_requirement: ^11\n", self::TRACKED));
    }

    // ------------------------------------------------------- applicableTo

    /**
     * The narrowing that makes a check mean something. Testing pathauto's
     * 8.x-1.x on core 9 produces a failure that says nothing about the module.
     */
    public function testTrackedCoresAreNarrowedToWhatTheBranchDeclares(): void
    {
        $compatibility = CoreCompatibility::fromConstraint('^10.2 || ^11 || ^12', self::TRACKED);

        self::assertNotNull($compatibility);
        self::assertSame(['10', '11'], $compatibility->applicableTo(['9', '10', '11']));
    }

    /**
     * An empty intersection yields the tracked set unchanged. A branch that
     * appears to support none of the cores you track is far likelier to be a
     * constraint this misread than a real state, and returning nothing would
     * make the module vanish from the dashboard entirely — the worst possible
     * failure mode for a tool whose job is to show you what needs attention.
     */
    public function testNoOverlapFallsBackToTheTrackedSetRatherThanHidingTheModule(): void
    {
        $compatibility = CoreCompatibility::fromConstraint('^7', self::TRACKED);

        self::assertNotNull($compatibility);
        self::assertSame([], $compatibility->cores);
        self::assertSame(['10', '11'], $compatibility->applicableTo(['10', '11']));
    }

    /** Non-numeric junk in the tracked set is ignored, not matched. */
    public function testANonNumericCandidateIsSkipped(): void
    {
        $compatibility = CoreCompatibility::fromConstraint('^11', ['11', 'next', '']);

        self::assertNotNull($compatibility);
        self::assertSame(['11'], $compatibility->cores);
    }

    /** Ascending, so the cell reads "10,11" rather than "11,10". */
    public function testCoresComeBackInOrder(): void
    {
        $compatibility = CoreCompatibility::fromConstraint('^10 || ^11 || ^12', ['12', '10', '11']);

        self::assertNotNull($compatibility);
        self::assertSame(['10', '11', '12'], $compatibility->cores);
    }
}

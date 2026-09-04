<?php

declare(strict_types=1);

namespace Upkeep\Drupal;

use Composer\Semver\Constraint\Constraint;
use Composer\Semver\Constraint\MultiConstraint;
use Composer\Semver\Intervals;
use Composer\Semver\VersionParser;

/**
 * Which Drupal cores a module branch declares support for.
 *
 * The fact the dashboard was missing. A *branch* supports several cores at
 * once — measured live: pathauto's single 8.x-1.x declares
 * `^10.2 || ^11 || ^12` — so treating a core version as part of a row's
 * identity multiplied every row by the size of a test matrix while saying
 * nothing new. Core belongs to the evidence; this is what says which cores the
 * evidence could meaningfully be gathered on.
 *
 * Matching is done with composer/semver rather than by hand. `^10.2` is the
 * reason: a regex over major versions cannot answer whether core 10.1 is
 * covered, and would answer confidently and wrongly. The question asked here
 * is "does this constraint allow *any* release in major N", so it is an
 * interval intersection (`>=N <N+1`) rather than a test of one chosen version,
 * which would need a patch level nobody has.
 */
final readonly class CoreCompatibility
{
    /**
     * @param list<string> $cores the major versions declared, ascending
     */
    private function __construct(public array $cores)
    {
    }

    /**
     * Reads a `core_version_requirement` constraint.
     *
     * Returns null when the constraint is absent or will not parse. Null means
     * "cannot tell", and every caller must fall back to the tracked set whole
     * — the behaviour before any of this existed. An unreadable constraint is
     * not evidence that a branch supports nothing.
     *
     * @param string $constraint e.g. "^10.2 || ^11 || ^12"
     * @param list<string> $candidates the major versions worth asking about
     */
    public static function fromConstraint(string $constraint, array $candidates): ?self
    {
        $constraint = trim($constraint);
        if ($constraint === '') {
            return null;
        }

        $parser = new VersionParser();

        try {
            $declared = $parser->parseConstraints($constraint);
        } catch (\UnexpectedValueException) {
            return null;
        }

        $cores = [];
        foreach ($candidates as $core) {
            if (!preg_match('/^\d+$/', $core)) {
                continue;
            }
            if (Intervals::haveIntersections($declared, self::major($core))) {
                $cores[] = $core;
            }
        }

        usort($cores, static fn (string $a, string $b): int => (int) $a <=> (int) $b);

        return new self($cores);
    }

    /**
     * Extracts the constraint from a module's info.yml *without parsing YAML*.
     *
     * A Drupal info.yml is YAML, but the only line wanted here is a scalar, and
     * a constraint containing `||` is exactly the kind of value a strict parser
     * rejects or mangles depending on quoting. Reading the one line is both
     * narrower and more robust than handing the whole file to a parser that
     * could refuse it over something unrelated further down.
     *
     * @param list<string> $candidates the major versions worth asking about
     */
    public static function fromInfoYaml(string $infoYaml, array $candidates): ?self
    {
        $constraint = self::constraintIn($infoYaml);

        return $constraint === null ? null : self::fromConstraint($constraint, $candidates);
    }

    /**
     * The raw constraint an info.yml declares, unparsed.
     *
     * What the snapshot stores. A cache holds *data*, not a resolved answer:
     * the tracked core list can change between the fetch and the read (a
     * registry edit costs nothing and goes to no network), and a snapshot
     * holding "10, 11" rather than "^10.2 || ^11 || ^12" would answer for a
     * question nobody asked yet.
     */
    public static function constraintIn(string $infoYaml): ?string
    {
        if (preg_match('/^core_version_requirement:\s*(.+?)\s*$/m', $infoYaml, $m) !== 1) {
            return null;
        }

        $constraint = trim($m[1], "'\" \t");

        return $constraint === '' ? null : $constraint;
    }

    /** Whether a tracked core is one this branch declares. */
    public function declares(string $core): bool
    {
        return \in_array($core, $this->cores, true);
    }

    /**
     * The cores worth testing on: what the registry tracks, narrowed to what
     * the branch declares.
     *
     * Wrong in both directions before this existed. Checking pathauto's
     * 8.x-1.x on a core it does not declare produces a failure that means
     * nothing, and a registry tracking 10 and 11 silently hid that the branch
     * also claims 12.
     *
     * An empty intersection yields the tracked set unchanged rather than
     * nothing: a branch that appears to support none of the cores you track is
     * far more likely to be a constraint this misread than a real state, and
     * showing no rows would hide the module entirely.
     *
     * @param list<string> $tracked
     * @return list<string>
     */
    public function applicableTo(array $tracked): array
    {
        $applicable = array_values(array_filter(
            $tracked,
            fn (string $core): bool => $this->declares($core),
        ));

        return $applicable === [] ? $tracked : $applicable;
    }

    /** The interval covering every release of one major version. */
    private static function major(string $core): MultiConstraint
    {
        return new MultiConstraint([
            new Constraint('>=', $core . '.0.0.0-dev'),
            new Constraint('<', ((int) $core + 1) . '.0.0.0-dev'),
        ]);
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Drupal;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Drupal\IssueVersion;

/**
 * Turning an issue's "Version" into the branch its patches were cut from.
 *
 * The bug: a patch on a 2.0.0 issue was applied to 1.0.x, because upkeep used
 * whatever the clone had checked out, and reported "does not apply to 1.0.x" —
 * true, and useless, since the patch was never meant for 1.0.x.
 *
 * The design point is that this class **decides nothing**. It proposes, and
 * the caller intersects with the project's real branches. Every version string
 * below was taken from live drupal.org issues, and they include `x.y.z`, which
 * appeared 56 times in one sample and means nothing at all. No parser is going
 * to be right about that set; proposing-and-checking is right about all of it,
 * because a candidate naming no real branch simply matches nothing.
 */
final class IssueVersionTest extends TestCase
{
    /**
     * @param list<string> $branches the project's real branches
     */
    #[DataProvider('versionsAgainstRealBranches')]
    public function testAVersionResolvesToABranchTheProjectActuallyHas(
        ?string $version,
        array $branches,
        ?string $expected,
    ): void {
        self::assertSame($expected, IssueVersion::resolveBranch($version, $branches));
    }

    /**
     * @return iterable<string, array{?string, list<string>, ?string}>
     */
    public static function versionsAgainstRealBranches(): iterable
    {
        // The case that started it: field_visibility_conditions has exactly
        // these two branches, and the issue is filed against 2.0.0.
        yield 'a release resolves to its branch' => ['2.0.0', ['1.0.x', '2.0.x'], '2.0.x'];

        yield 'a dev version drops the suffix' => ['2.0.x-dev', ['1.0.x', '2.0.x'], '2.0.x'];
        yield 'legacy contrib dev' => ['8.x-1.x-dev', ['8.x-1.x'], '8.x-1.x'];
        yield 'legacy contrib release' => ['8.x-1.4', ['8.x-1.x', '8.x-2.x'], '8.x-1.x'];
        yield 'a project branching at the major' => ['2.0.0', ['1.x', '2.x'], '2.x'];
        yield 'the exact string is a branch' => ['4.6.x-dev', ['4.6.x'], '4.6.x'];

        // The specific branch is preferred over the broader one.
        yield 'most specific wins' => ['2.0.0', ['2.x', '2.0.x'], '2.0.x'];

        // ...and all the ways it says nothing.
        yield 'the literal placeholder' => ['x.y.z', ['1.0.x', '2.0.x'], null];
        yield 'a version with no such branch' => ['9.9.9', ['1.0.x', '2.0.x'], null];
        yield 'no version at all' => [null, ['1.0.x'], null];
        yield 'an empty version' => ['', ['1.0.x'], null];
        yield 'whitespace' => ['   ', ['1.0.x'], null];
        yield 'a project with no branches read' => ['2.0.0', [], null];
    }

    /**
     * Candidates run specific to broad, because the caller takes the first
     * that exists: a project with both 2.0.x and 2.x must get 2.0.x.
     */
    public function testCandidatesAreOrderedMostSpecificFirst(): void
    {
        self::assertSame(['2.0.0', '2.0.x', '2.x'], IssueVersion::branchCandidates('2.0.0'));
        self::assertSame(['2.0.x', '2.x'], IssueVersion::branchCandidates('2.0.x-dev'));
    }

    /** Nothing usable proposes nothing, so the caller can skip the lookup. */
    public function testAnUnusableVersionProposesNothing(): void
    {
        self::assertSame([], IssueVersion::branchCandidates(null));
        self::assertSame([], IssueVersion::branchCandidates(''));
        self::assertSame([], IssueVersion::branchCandidates("  \n"));
    }

    /**
     * `x.y.z` is the one string that looks like a version and is not one. It
     * still proposes only itself — no numeric reading is possible — so the
     * intersection rejects it without any special case.
     */
    public function testThePlaceholderProposesOnlyItselfAndMatchesNothingReal(): void
    {
        self::assertSame(['x.y.z'], IssueVersion::branchCandidates('x.y.z'));
    }

    /** No duplicates, or the caller would check the same branch twice. */
    public function testCandidatesAreDistinct(): void
    {
        foreach (['2.0.x-dev', '2.0.x', '8.x-1.x', '2.0.0', 'x.y.z'] as $version) {
            $candidates = IssueVersion::branchCandidates($version);

            self::assertSame(array_values(array_unique($candidates)), $candidates, $version);
        }
    }
}

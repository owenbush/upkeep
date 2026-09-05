<?php

declare(strict_types=1);

namespace Upkeep\Tests\Gitlab;

use PHPUnit\Framework\TestCase;
use Upkeep\Gitlab\MergeRevision;

/**
 * Which revision a merge request's local evidence is about.
 *
 * One definition, because two would be a silent bug: the dashboard decides
 * whether a cached result is still current and `check` decides what to file it
 * under, and if those disagreed the evidence would read as fresh forever or
 * stale forever — both of which look like the tool working.
 */
final class MergeRevisionTest extends TestCase
{
    private const MERGE = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
    private const HEAD = 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';

    /**
     * The correction. upkeep checks the branch merged into the current tip of
     * its target, and that tree changes when *either* side moves — so keying
     * on the head SHA left evidence reading as current after the target gained
     * a commit, which is exactly the "evidence about a tree nobody checked"
     * that keying on a SHA exists to prevent.
     */
    public function testTheMergeRefWinsOverTheHead(): void
    {
        self::assertSame(self::MERGE, MergeRevision::of(self::MERGE, self::HEAD));
    }

    /**
     * The one case with no merge ref: GitLab could not merge the branch into
     * its target, normally a conflict. The adapter checks the branch alone
     * there, so the revision follows it — both halves fall back together, or
     * the key would never match what was checked.
     */
    public function testWithoutAMergeRefTheHeadIsTheRevision(): void
    {
        self::assertSame(self::HEAD, MergeRevision::of(null, self::HEAD));
        self::assertSame(self::HEAD, MergeRevision::of('', self::HEAD));
    }

    /**
     * Neither known means nothing can be cached. An entry keyed on a guess is
     * permanently-fresh evidence, and the fast-lane gate reads these.
     */
    public function testNeitherKnownIsNullRatherThanSomethingInvented(): void
    {
        self::assertNull(MergeRevision::of(null, null));
        self::assertNull(MergeRevision::of('', ''));
        self::assertNull(MergeRevision::of(null, ''));
    }
}

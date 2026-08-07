<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\ManagedBranch;
use Upkeep\Adapter\MrCheckout;
use Upkeep\Adapter\PatchApplication;
use Upkeep\Adapter\PatchCheckout;

final class PatchCheckoutTest extends TestCase
{
    private static function patch(): PatchApplication
    {
        return new PatchApplication(3597808, '3597808-9-fix.patch', '/tmp/cache/3597808/3597808-9-fix.patch');
    }

    /**
     * Keyed by issue rather than by file: re-applying a re-roll from the same
     * issue replaces the branch instead of leaving one branch per attempt.
     */
    public function testTheBranchIsNamedForTheIssue(): void
    {
        self::assertSame('patch-3597808', self::patch()->branchName());
        self::assertSame('patch-3597808', ManagedBranch::forPatch(3597808));
    }

    /**
     * -p1 because drupal.org patches are diffs cut at the repository root;
     * --index so the commit that follows needs no separate `git add` and a
     * partial application cannot leave staged and unstaged halves disagreeing.
     */
    public function testApplyArgsStageWhatTheyApply(): void
    {
        self::assertSame(
            ['apply', '--index', '-p1', '/tmp/p.patch'],
            PatchCheckout::applyArgs('/tmp/p.patch'),
        );
        self::assertSame(
            ['apply', '--index', '-p1', '--3way', '/tmp/p.patch'],
            PatchCheckout::threeWayApplyArgs('/tmp/p.patch'),
        );
    }

    /**
     * The message names the file, not just the issue: an issue routinely
     * carries several re-rolls and the working copy should record which one
     * it holds.
     */
    public function testTheCommitMessageNamesTheFileAndTheIssue(): void
    {
        self::assertSame(
            'Apply 3597808-9-fix.patch (issue #3597808) [upkeep]',
            PatchCheckout::commitMessage(self::patch()),
        );
    }

    /**
     * A patch that no longer applies has told the maintainer something true
     * about the contribution, so it is phrased as a review finding rather
     * than a tool error — and it names the base, because "does not apply" is
     * meaningless without saying to what.
     */
    public function testAnUnappliablePatchIsReportedAsNeedingAReRoll(): void
    {
        $e = PatchCheckout::unappliableException(self::patch(), '1.0.x', "error: patch failed: widget.module:12\n");

        self::assertInstanceOf(AdapterException::class, $e);
        self::assertStringContainsString('3597808-9-fix.patch', $e->getMessage());
        self::assertStringContainsString('issue #3597808', $e->getMessage());
        self::assertStringContainsString('"1.0.x"', $e->getMessage());
        self::assertStringContainsString('needs a re-roll', $e->getMessage());
        self::assertStringContainsString('patch failed: widget.module:12', $e->getMessage());
    }

    public function testASilentGitFailureStillProducesAReadableMessage(): void
    {
        $e = PatchCheckout::unappliableException(self::patch(), '1.0.x', "  \n ");

        self::assertStringContainsString('git reported no detail', $e->getMessage());
    }

    /**
     * Both apply paths create a branch, and neither branch is ever a valid
     * *base*. Resolving a base of patch-3597808 would silently test the next
     * contribution stacked on the previous one, so the two prefixes are
     * recognised together.
     */
    public function testAPatchBranchIsNeverMistakenForABase(): void
    {
        self::assertTrue(ManagedBranch::isManaged('patch-3597808'));
        self::assertTrue(ManagedBranch::isManaged('mr-7'));
        self::assertFalse(ManagedBranch::isManaged('1.0.x'));
        self::assertFalse(ManagedBranch::isManaged('patch-things-up'));
        self::assertFalse(ManagedBranch::isManaged('feature/mr-7'));

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessageMatches('/patch-3597808/');
        MrCheckout::resolveBaseBranch('patch-3597808', null);
    }

    public function testARecordedBaseStillWinsFromAPatchBranch(): void
    {
        self::assertSame('1.0.x', MrCheckout::resolveBaseBranch('patch-3597808', '1.0.x'));
    }
}

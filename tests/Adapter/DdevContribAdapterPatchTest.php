<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\IssueBranch;
use Upkeep\Adapter\PatchApplication;

/**
 * Applying a patch file to an already-provisioned environment.
 *
 * A merge request arrives as a ref to fetch; a patch arrives as a file, which
 * makes two things this adapter's problem that the MR path never has to think
 * about — leaving the tree clean enough for the checks to mean anything, and
 * deciding what a patch that will not apply *is*.
 */
final class DdevContribAdapterPatchTest extends DdevAdapterTestCase
{
    private string $patchFile;

    protected function setUp(): void
    {
        parent::setUp();

        mkdir($this->projectPath() . '/module', 0o700, true);
        $this->patchFile = $this->root . '/3597808-9-fix.patch';
        file_put_contents($this->patchFile, "diff --git a/x b/x\n--- a/x\n+++ b/x\n");
    }

    private function patch(): PatchApplication
    {
        return new PatchApplication(3597808, '3597808-9-fix.patch', $this->patchFile);
    }

    /**
     * The whole sequence, in order: stand on the base, reset the patch branch
     * from it, record the base, apply, commit, and follow with the composer
     * pin. The reset is what makes re-running with a re-roll test that re-roll
     * alone rather than the sum of everything applied before it.
     */
    public function testApplyingAPatchBranchesFromTheBaseAppliesAndCommits(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse HEAD' => "bbbbbbb\n",
        ]);

        $this->adapter($runner)->applyPatch($this->environment(), $this->patch());

        $lines = $runner->commandLines();
        $moduleDir = $this->projectPath() . '/module';

        $standOnBase = array_search('git -C ' . $moduleDir . ' checkout 1.0.x', $lines, true);
        $fetchBase = array_search('git -C ' . $moduleDir . ' fetch origin 1.0.x', $lines, true);
        // FETCH_HEAD, not the local 1.0.x: the working copy is cloned once and
        // its base then sits still while drupal.org's moves, and CI checks the
        // work merged into the *current* tip. Cutting from the local branch is
        // how a check passes against code months older than the one CI runs.
        $resetBranch = array_search('git -C ' . $moduleDir . ' checkout -B patch-3597808 FETCH_HEAD', $lines, true);
        self::assertIsInt($standOnBase);
        self::assertIsInt($fetchBase);
        self::assertIsInt($resetBranch);
        self::assertLessThan($fetchBase, $standOnBase);
        self::assertLessThan($resetBranch, $fetchBase);

        self::assertTrue($runner->issued('config upkeep.base-branch 1.0.x'));
        self::assertTrue($runner->issued('apply --index -p1 ' . $this->patchFile));
        self::assertTrue($runner->issued('commit --no-verify -m Apply 3597808-9-fix.patch (issue #3597808) [upkeep]'));
        // The identity is upkeep's own: the maintainer's git config must not
        // decide whether a throwaway commit in a throwaway tree can be made.
        self::assertTrue($runner->issued('-c user.name=upkeep'));
        // The composer pin follows the branch, or later resolutions break.
        self::assertTrue($runner->issued('composer require drupal/widget:dev-patch-3597808'));
        self::assertTrue($this->loggedContaining('Patch "3597808-9-fix.patch" applied'));
    }

    /**
     * The commit is not bookkeeping: without it the patch would read as local
     * modification to every later inspection, and the dirty-working-copy guard
     * would refuse the next apply.
     */
    public function testTheCommitHappensAfterTheApplyAndBeforeTheChecksCouldRun(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse HEAD' => "bbbbbbb\n",
        ]);

        $this->adapter($runner)->applyPatch($this->environment(), $this->patch());

        $lines = $runner->commandLines();
        $apply = null;
        $commit = null;
        foreach ($lines as $index => $line) {
            if (str_contains($line, 'apply --index')) {
                $apply ??= $index;
            }
            if (str_contains($line, 'commit --no-verify')) {
                $commit ??= $index;
            }
        }

        self::assertIsInt($apply);
        self::assertIsInt($commit);
        self::assertLessThan($commit, $apply);
    }

    public function testApplyingAPatchRefusesAnUncommittedWorkingCopy(): void
    {
        $runner = $this->engine([
            'status --porcelain' => "M  src/Widget.php\n",
            'symbolic-ref --short HEAD' => "1.0.x\n",
        ]);

        try {
            $this->adapter($runner)->applyPatch($this->environment(), $this->patch());
            self::fail('Expected the dirty working copy to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('Cannot apply patch "3597808-9-fix.patch"', $e->getMessage());
            self::assertStringContainsString('Staged changes not yet committed', $e->getMessage());
            self::assertFalse($runner->issued('apply --index'), 'Nothing may be applied over local changes.');
        }
    }

    /**
     * On a re-apply the working copy already sits on patch-<nid>, so the base
     * comes from the git config the previous apply recorded — the same
     * mechanism the MR path uses, and the reason a patch branch is never
     * itself mistaken for a base.
     */
    public function testReapplyingUsesTheRecordedBaseRatherThanThePatchBranch(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "patch-3597808\n",
            'config --get upkeep.base-branch' => "1.0.x\n",
            'rev-parse HEAD' => "bbbbbbb\n",
        ]);

        $this->adapter($runner)->applyPatch($this->environment(), $this->patch());

        self::assertTrue($runner->issued('fetch origin 1.0.x'), 'the recorded base is what gets refreshed');
        self::assertTrue($runner->issued('checkout -B patch-3597808 FETCH_HEAD'));
    }

    /**
     * Three-way resolves hunks that plain context matching rejects whenever
     * the blobs the patch was cut against are in the repository — the common
     * case for a drupal.org patch on its own project. So a straight failure is
     * a retry, not a verdict.
     */
    public function testAStraightApplyFailureIsRetriedThreeWay(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse HEAD' => "bbbbbbb\n",
            '--3way' => '',
            'apply --index -p1' => null,
        ]);

        $this->adapter($runner)->applyPatch($this->environment(), $this->patch());

        self::assertTrue($runner->issued('--3way'));
        self::assertTrue($runner->issued('commit --no-verify'), 'A three-way success still commits.');
        self::assertTrue($this->loggedContaining('Retrying with three-way'));
        self::assertFalse($runner->issued('-C1'), 'no need to loosen context when three-way worked');
    }

    /**
     * The rung that matters in practice, and the one whose absence made every
     * Project Update Bot patch look stale.
     *
     * drupal.org generates patches against an export whose files may carry a
     * trailing blank line the repository does not, so a hunk header promises
     * seven context lines for a six-line file. Both exact attempts fail; -C1
     * applies the whole thing. Verified against
     * entity_type_access_conditions #3597857, where nine files were refused
     * over one trailing newline.
     */
    public function testAPatchRefusedOnlyOverContextIsAppliedWithReducedContext(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse HEAD' => "bbbbbbb\n",
            '-C1' => '',
            'apply --index' => null,
        ]);

        $this->adapter($runner)->applyPatch($this->environment(), $this->patch());

        self::assertTrue($runner->issued('apply --index -p1 -C1'));
        self::assertTrue($runner->issued('commit --no-verify'), 'a reduced-context success still commits');
        // A hunk placed on one line of context is a weaker guarantee than one
        // placed on three; the operator is told which they got.
        self::assertTrue($this->loggedContaining('Applied via reduced context'));
        self::assertTrue($this->loggedContaining('did not match the working copy exactly'));
    }

    /**
     * All three rungs failing is the review finding: this patch really is
     * stale. The working copy goes back to the base rather than being left
     * half-patched for the next command to inherit, and the report names the
     * file that moved on.
     */
    public function testAPatchThatWillNotApplyIsReportedAndTheTreeIsPutBack(): void
    {
        $checkOutput = "Checking patch a.php...\n"
            . "error: while searching for:\nsome missing context\n"
            . "error: patch failed: tests/thing.info.yml:1\n"
            . "error: tests/thing.info.yml: patch does not apply\n";

        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'apply --stat' => " a.php | 2 +-\n 3 files changed, 2 insertions(+)\n",
            'apply --check' => $checkOutput,
            'apply --index' => null,
        ]);

        try {
            $this->adapter($runner)->applyPatch($this->environment(), $this->patch());
            self::fail('Expected the unappliable patch to be reported.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('does not apply to "1.0.x"', $e->getMessage());
            self::assertStringContainsString('3597808-9-fix.patch', $e->getMessage());
            self::assertStringContainsString('3 file(s); 2 apply, 1 do not', $e->getMessage());
            self::assertStringContainsString('tests/thing.info.yml', $e->getMessage());
            self::assertStringContainsString('some missing context', $e->getMessage());
        }

        // All three rungs were tried before giving up.
        self::assertTrue($runner->issued('--3way'));
        self::assertTrue($runner->issued('-C1'));
        self::assertTrue($runner->issued('reset --hard'));
        self::assertFalse($runner->issued('commit --no-verify'), 'Nothing may be committed after a failed apply.');
    }

    /**
     * A working copy on a managed branch with nothing recorded cannot say what
     * its base is, and guessing would stack one contribution on another.
     */
    public function testAPatchBranchWithNoRecordedBaseIsRefusedRatherThanGuessed(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "patch-3597808\n",
            'config --get upkeep.base-branch' => null,
        ]);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessageMatches('/upkeep-managed branch "patch-3597808"/');
        $this->adapter($runner)->applyPatch($this->environment(), $this->patch());
    }

    // ------------------------------------------------------------- promoting

    /**
     * Promoting routes through startWork(), and that is the whole point of it
     * being a separate operation: the branch a promoted patch lands on may be
     * the only place that work exists, so it must be *resumed* — never the
     * `checkout -B` the disposable patch branch gets.
     */
    public function testPromotingResumesTheWorkBranchAndNeverResetsIt(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --verify 3597808-fix-it' => "aaaaaaa\n",
            'rev-parse HEAD' => "ccccccc\n",
        ]);

        $sha = $this->adapter($runner)->promotePatch(
            $this->environment(),
            $this->patch(),
            IssueBranch::named(3597808, '3597808-fix-it'),
            "Issue #3597808 by someone: Fix it\n",
        );

        self::assertSame('ccccccc', $sha);
        self::assertTrue($runner->issued('checkout 3597808-fix-it'));
        self::assertTrue($runner->issued('apply --index -p1 ' . $this->patchFile));

        foreach ($runner->commandLines() as $line) {
            self::assertStringNotContainsString('checkout -B', $line, 'a work branch must never be reset');
        }
    }

    /**
     * The attribution reaches git verbatim. Everything else about promoting is
     * plumbing; this is the feature.
     */
    public function testTheGivenCommitMessageIsWhatIsCommitted(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --verify 3597808-fix-it' => "aaaaaaa\n",
            'rev-parse HEAD' => "ccccccc\n",
        ]);

        $message = "Issue #3597808 by hebatelhayah: Fix it\n\nPatch-author: hebatelhayah\n";
        $this->adapter($runner)->promotePatch(
            $this->environment(),
            $this->patch(),
            IssueBranch::named(3597808, '3597808-fix-it'),
            $message,
        );

        self::assertTrue($runner->issued('commit --no-verify -m ' . $message));
    }

    /**
     * A patch that will not apply leaves the work branch as it was found —
     * the recovery returns to the branch, not to the base, because on this
     * path the branch is the thing that must survive.
     */
    public function testAnUnappliablePatchLeavesTheWorkBranchIntact(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --verify 3597808-fix-it' => "aaaaaaa\n",
            'apply --stat' => " a.php | 2 +-\n 1 file changed, 2 insertions(+)\n",
            'apply --check' => "error: a.php: patch does not apply\n",
            'apply --index' => null,
        ]);

        try {
            $this->adapter($runner)->promotePatch(
                $this->environment(),
                $this->patch(),
                IssueBranch::named(3597808, '3597808-fix-it'),
                "Issue #3597808: Fix it\n",
            );
            self::fail('an unappliable patch should raise');
        } catch (AdapterException $e) {
            self::assertStringContainsString('3597808-fix-it', $e->getMessage());
        }

        self::assertFalse($runner->issued('commit --no-verify'));
    }
}

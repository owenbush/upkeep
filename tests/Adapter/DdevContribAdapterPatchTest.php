<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use Upkeep\Adapter\AdapterException;
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
        $resetBranch = array_search('git -C ' . $moduleDir . ' checkout -B patch-3597808 1.0.x', $lines, true);
        self::assertIsInt($standOnBase);
        self::assertIsInt($resetBranch);
        self::assertLessThan($resetBranch, $standOnBase);

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

        self::assertTrue($runner->issued('checkout -B patch-3597808 1.0.x'));
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
        self::assertTrue($this->loggedContaining('retrying three-way'));
    }

    /**
     * Both attempts failing is the review finding: this patch no longer
     * applies. The working copy goes back to the base rather than being left
     * half-patched for the next command to inherit.
     */
    public function testAPatchThatWillNotApplyIsReportedAndTheTreeIsPutBack(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'apply --index' => null,
        ]);

        try {
            $this->adapter($runner)->applyPatch($this->environment(), $this->patch());
            self::fail('Expected the unappliable patch to be reported.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('needs a re-roll', $e->getMessage());
            self::assertStringContainsString('3597808-9-fix.patch', $e->getMessage());
            self::assertStringContainsString('"1.0.x"', $e->getMessage());
        }

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
}

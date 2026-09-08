<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\EnvironmentMeta;
use Upkeep\Adapter\FixtureAddOn;
use Upkeep\Adapter\WorkingCopyStatus;
use Upkeep\Gitlab\MergeRequest;

/**
 * The operations that act on an already-provisioned environment: applying a
 * merge request, switching branches, loading a fixture, serving, and the
 * read-only lookups the maintenance surfaces use.
 */
final class DdevContribAdapterWorkingCopyTest extends DdevAdapterTestCase
{
    protected function setUp(): void
    {
        parent::setUp();

        mkdir($this->projectPath() . '/module', 0o700, true);
    }

    private static function mergeRequest(
        int $iid = 7,
        string $target = '1.0.x',
        ?string $headSha = 'aaaaaaa',
    ): MergeRequest {
        return new MergeRequest(
            iid: $iid,
            title: 'Fix the widget',
            state: 'opened',
            authorUsername: 'someone',
            authorId: 1,
            sourceBranch: $iid . '-fix',
            targetBranch: $target,
            draft: false,
            detailedMergeStatus: 'mergeable',
            headSha: $headSha,
            webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/' . $iid,
        );
    }

    public function testApplyingAnMrFetchesItStandsOnTheBaseFirstAndRecordsTheBase(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --abbrev-ref HEAD' => "mr-7\n",
            'rev-parse HEAD' => "aaaaaaa\n",
        ]);

        $this->adapter($runner)->applyMr($this->environment(), self::mergeRequest());

        $lines = $runner->commandLines();
        $moduleDir = $this->projectPath() . '/module';
        $checkoutBase = array_search('git -C ' . $moduleDir . ' checkout 1.0.x', $lines, true);
        // The *merge* ref, not the head: CI analyses the branch merged into
        // the current tip of its target, and on pathauto 23 of 25 open merge
        // requests have a merge tree that differs from their head tree.
        $fetch = array_search(
            'git -C ' . $moduleDir . ' fetch origin +refs/merge-requests/7/merge:mr-7',
            $lines,
            true,
        );
        self::assertIsInt($checkoutBase);
        self::assertIsInt($fetch);
        self::assertLessThan(
            $fetch,
            $checkoutBase,
            'git refuses to fetch into the checked-out branch, so the base must be checked out first.',
        );

        self::assertTrue($runner->issued('checkout mr-7'));
        self::assertTrue($runner->issued('config upkeep.base-branch 1.0.x'));
        // The composer pin follows the branch, or later resolutions break.
        self::assertTrue($runner->issued('composer require drupal/widget:dev-mr-7'));
        self::assertTrue($this->loggedContaining('MR !7 applied'));
    }

    /**
     * On a re-apply the working copy already sits on mr-<iid>, so the base is
     * read from the git config the previous apply recorded.
     */
    public function testReapplyingAnMrUsesTheRecordedBaseBranch(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "mr-7\n",
            'config --get upkeep.base-branch' => "1.0.x\n",
            'rev-parse --abbrev-ref HEAD' => "mr-7\n",
            'rev-parse HEAD' => "aaaaaaa\n",
        ]);

        $this->adapter($runner)->applyMr($this->environment(), self::mergeRequest());

        self::assertTrue($runner->issued('checkout 1.0.x'));
    }

    public function testApplyingAnMrRefusesAnUncommittedWorkingCopy(): void
    {
        $runner = $this->engine([
            'status --porcelain' => "M  src/Widget.php\n",
            'symbolic-ref --short HEAD' => "1.0.x\n",
        ]);

        try {
            $this->adapter($runner)->applyMr($this->environment(), self::mergeRequest());
            self::fail('Expected the dirty working copy to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('Cannot apply MR !7', $e->getMessage());
            self::assertStringContainsString('Staged changes not yet committed', $e->getMessage());
            self::assertFalse($runner->issued('fetch origin'), 'Nothing may be fetched over local changes.');
        }
    }

    public function testApplyingAnMrRefusesABaseOtherThanTheBranchItTargets(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "2.x\n",
            'config --get upkeep.base-branch' => null,
        ]);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('Backport testing is out of scope');

        $this->adapter($runner)->applyMr($this->environment(), self::mergeRequest());
    }

    public function testApplyingAnMrRefusesWhenTheCheckoutDidNotStick(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --abbrev-ref HEAD' => "1.0.x\n",
        ]);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('MR checkout did not stick: working copy is on "1.0.x", expected "mr-7"');

        $this->adapter($runner)->applyMr($this->environment(), self::mergeRequest());
    }

    /**
     * A merge request GitLab cannot merge publishes no merge ref, so the
     * branch is checked on its own — and that is announced, because a
     * branch-only verdict is not the one CI would give. Normally it means the
     * contribution conflicts with its target.
     */
    public function testAConflictingMrFallsBackToItsBranchAndSaysSo(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --abbrev-ref HEAD' => "mr-7\n",
            'rev-parse HEAD' => "aaaaaaa\n",
            'ls-remote' => "1111111111111111111111111111111111111111\trefs/merge-requests/7/head\n",
        ]);

        $this->adapter($runner)->applyMr($this->environment(), self::mergeRequest());

        self::assertTrue($runner->issued('fetch origin +refs/merge-requests/7/head:mr-7'));
        self::assertTrue($this->loggedContaining('has no merge ref'));
        self::assertTrue($this->loggedContaining('not what CI runs'));
    }

    /**
     * A head that moved since the MR was fetched is reported, not refused —
     * the checkout is still valid, it is just newer than the model.
     *
     * Only meaningful on the head ref. The merge ref is a commit GitLab made
     * by merging the branch into its target, so it is never the MR's head SHA
     * and comparing the two would warn on every healthy run.
     */
    public function testAHeadThatMovedSinceTheMrWasFetchedIsReported(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --abbrev-ref HEAD' => "mr-7\n",
            'rev-parse HEAD' => "bbbbbbb\n",
            'ls-remote' => "1111111111111111111111111111111111111111\trefs/merge-requests/7/head\n",
        ]);

        $this->adapter($runner)->applyMr($this->environment(), self::mergeRequest());

        self::assertTrue($this->loggedContaining('differs from the MR model\'s head aaaaaaa'));
    }

    /** Checking out the merge never warns about the head it is not. */
    public function testTheMergeRefDoesNotWarnAboutNotBeingTheHeadSha(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --abbrev-ref HEAD' => "mr-7\n",
            'rev-parse HEAD' => "ccccccc\n",
        ]);

        $this->adapter($runner)->applyMr($this->environment(), self::mergeRequest());

        self::assertFalse($this->loggedContaining('differs from the MR model'));
        self::assertTrue($this->loggedContaining('Checking MR !7 as CI does'));
    }

    /** No refs at all is the merge request number being wrong, not a state to guess about. */
    public function testAMergeRequestPublishingNoRefsIsRefused(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'ls-remote' => '',
        ]);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('publishes no refs on origin');

        $this->adapter($runner)->applyMr($this->environment(), self::mergeRequest());
    }

    public function testAnMrWithNoRecordedHeadIsAppliedWithoutAHeadComparison(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
            'config --get upkeep.base-branch' => null,
            'rev-parse --abbrev-ref HEAD' => "mr-7\n",
            'rev-parse HEAD' => "bbbbbbb\n",
        ]);

        $this->adapter($runner)->applyMr($this->environment(), self::mergeRequest(headSha: null));

        self::assertFalse($this->loggedContaining('differs from the MR model'));
    }

    public function testCheckingOutABranchThatIsAlreadyLocalSkipsTheFetch(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
        ]);

        $this->adapter($runner)->checkoutBranch($this->environment(), '2.x');

        self::assertFalse($runner->issued('fetch origin'));
        self::assertTrue($runner->issued('composer require drupal/widget:2.x-dev'));
        self::assertTrue($this->loggedContaining('Module working copy on branch "2.x"'));
    }

    public function testAnUnknownBranchIsFetchedBeforeTheSecondCheckoutAttempt(): void
    {
        $attempts = 0;
        $runner = new ScriptedCommandRunner(function (array $command, ?string $cwd) use (&$attempts): ?string {
            $line = implode(' ', $command);
            if (str_contains($line, 'checkout 2.x')) {
                ++$attempts;

                // Unknown locally the first time; present after the fetch.
                return $attempts === 1 ? null : '';
            }
            if (str_contains($line, 'status --porcelain')) {
                return '';
            }

            return ($this->engine())->tryRun($command, $cwd);
        });

        $this->adapter($runner)->checkoutBranch($this->environment(), '2.x');

        self::assertSame(2, $attempts);
        self::assertTrue($runner->issued('fetch origin'));
    }

    public function testCheckingOutABranchRefusesAnUncommittedWorkingCopy(): void
    {
        $runner = $this->engine([
            'status --porcelain' => " M src/Widget.php\n",
            'symbolic-ref --short HEAD' => "1.0.x\n",
        ]);

        try {
            $this->adapter($runner)->checkoutBranch($this->environment(), '2.x');
            self::fail('Expected the branch switch to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('Cannot switch branches', $e->getMessage());
            self::assertFalse($runner->issued('checkout 2.x'));
        }
    }

    public function testLoadingAFixtureInstallsTheAddOnOnceAndThenDelegatesToIt(): void
    {
        $runner = $this->engine();
        $adapter = $this->adapter($runner);

        $adapter->loadFixture($this->environment(), 'baseline');
        $adapter->loadFixture($this->environment(), 'baseline');

        self::assertSame(
            1,
            substr_count(implode("\n", $runner->commandLines()), 'add-on get ' . FixtureAddOn::source()),
            'The add-on probe is the marker file, so a second load must not reinstall it.',
        );
        // A literal, deliberately, and not FixtureAddOn::LOAD_COMMAND — a test
        // that follows the constant follows a change to it and stays green,
        // which is how upkeep came to invoke a command the add-on does not
        // publish. This is the name owenbush/ddev-upkeep installs;
        // tests/Integration/FixtureAddOnContractTest is what checks that
        // claim against the add-on rather than restating it.
        self::assertSame(
            2,
            substr_count(implode("\n", $runner->commandLines()), 'ddev upkeep-fixture-load baseline'),
        );
    }

    public function testServingInstallsTheModuleAndReportsTheLoginUrl(): void
    {
        $runner = $this->engine(['drush uli' => "https://widget.ddev.site/user/reset/1/abc\n"]);

        $result = $this->adapter($runner)->serve($this->environment());

        self::assertSame(self::PRIMARY_URL, $result->url);
        self::assertSame('https://widget.ddev.site/user/reset/1/abc', $result->loginUrl);
        self::assertTrue($runner->issued('drush pm:install widget -y'));
    }

    /**
     * An engine that cannot mint a one-time login reports no login URL rather
     * than an empty string the console would print as a blank link.
     */
    public function testServingReportsNoLoginUrlWhenTheEngineCannotMintOne(): void
    {
        foreach ([null, "\n"] as $uliOutcome) {
            $result = $this->adapter($this->engine(['drush uli' => $uliOutcome]))->serve($this->environment());

            self::assertNull($result->loginUrl);
            self::assertSame(self::PRIMARY_URL, $result->url);
        }
    }

    public function testResolvingAnEnvPathRequiresBothTheTreeAndTheCompletionMarker(): void
    {
        $adapter = $this->adapter($this->engine());

        // Tree present, marker absent: an interrupted provision, not an env.
        self::assertNull($adapter->resolveEnvPath(self::MODULE, self::CORE));

        file_put_contents($this->projectPath() . '/' . EnvironmentMeta::FILENAME, "module: widget\n");
        self::assertSame($this->projectPath(), $adapter->resolveEnvPath(self::MODULE, self::CORE));

        // A module that was never provisioned has no tree at all.
        self::assertNull($adapter->resolveEnvPath('other', self::CORE));
    }

    public function testInspectingAWorkingCopyReportsTheBranchItIsOn(): void
    {
        $runner = $this->engine([
            'status --porcelain' => '',
            'symbolic-ref --short HEAD' => "1.0.x\n",
        ]);
        file_put_contents($this->projectPath() . '/' . EnvironmentMeta::FILENAME, "module: widget\n");

        $status = $this->adapter($runner)->inspectWorkingCopy(self::MODULE, self::CORE);

        self::assertInstanceOf(WorkingCopyStatus::class, $status);
        self::assertSame('1.0.x', $status->currentBranch);
    }

    /**
     * Without a completion marker there is no environment, and with no clone
     * there is no working copy — either way there is nothing to report on, and
     * neither may be answered with a fabricated clean status.
     */
    public function testInspectingReportsNothingWithoutAnEnvironmentOrACheckout(): void
    {
        $adapter = $this->adapter($this->engine(['status --porcelain' => '']));

        // Tree present, completion marker absent.
        self::assertNull($adapter->inspectWorkingCopy(self::MODULE, self::CORE));

        // Marker present but the clone never landed.
        file_put_contents($this->projectPath() . '/' . EnvironmentMeta::FILENAME, "module: widget\n");
        exec('rm -rf ' . escapeshellarg($this->projectPath() . '/module'));
        self::assertNull($adapter->inspectWorkingCopy(self::MODULE, self::CORE));
    }
}

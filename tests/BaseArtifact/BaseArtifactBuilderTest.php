<?php

declare(strict_types=1);

namespace Upkeep\Tests\BaseArtifact;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\ThrowawaySite;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\BaseArtifact\ArtifactMeta;
use Upkeep\BaseArtifact\BaseArtifactBuilder;
use Upkeep\BaseArtifact\BuildException;

/**
 * The base-artifact build orchestration, exercised against a fake shell-out
 * seam: which commands are issued, in what order, and — the part that matters
 * most — what is left on disk when a step fails. An existing version directory
 * is the tool's "this build completed" marker, so a half-built one would make
 * every downstream environment seed from an artifact set that was never
 * finished.
 */
final class BaseArtifactBuilderTest extends TestCase
{
    private string $world;
    private ArtifactLayout $layout;
    private FakeCommandRunner $runner;

    /** @var list<string> */
    private array $log = [];

    protected function setUp(): void
    {
        $this->world = sys_get_temp_dir() . '/upkeep-builder-' . bin2hex(random_bytes(4));
        mkdir($this->world . '/base-artifacts', 0o700, true);
        $this->layout = new ArtifactLayout($this->world . '/base-artifacts');
        $this->runner = new FakeCommandRunner();
        $this->log = [];
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->world));
    }

    private function builder(?string $scratchDir = null): BaseArtifactBuilder
    {
        return new BaseArtifactBuilder(
            $this->layout,
            new ThrowawaySite($this->runner, $this->logger()),
            $scratchDir ?? $this->world . '/scratch',
            $this->logger(),
            $this->runner,
        );
    }

    /**
     * Staging directories currently on disk. Empty is the invariant: a build
     * either finishes and moves in, or cleans up after itself.
     *
     * @return list<string>
     */
    private function stagingDirectories(): array
    {
        $found = glob($this->layout->baseArtifactsDir . '/.building-d*', GLOB_ONLYDIR);

        return $found === false ? [] : $found;
    }

    private function logged(string $fragment): bool
    {
        return str_contains(implode("\n", $this->log), $fragment);
    }

    /**
     * Runs $hook part-way through a build — after the tree is resolved and
     * before the finished set is moved into place, which is the only window in
     * which a staged set and a live set both exist.
     *
     * @param \Closure(): void $hook
     */
    private function midBuild(\Closure $hook): void
    {
        $this->runner->after('ddev delete', $hook);
    }

    private function logger(): \Closure
    {
        return function (string $line): void {
            $this->log[] = $line;
        };
    }

    public function testABuildProducesTheTreeDumpMetaAndCanonicalMarkerAndTearsTheThrowawayDown(): void
    {
        $meta = $this->builder()->build('11', false);

        self::assertDirectoryExists($this->layout->treePath('11'));
        self::assertFileExists($this->layout->dumpPath('11'));
        self::assertFileExists($this->layout->canonicalMarkerPath('11'));
        self::assertStringContainsString('never auto-pruned', (string) file_get_contents(
            $this->layout->canonicalMarkerPath('11'),
        ));

        // The sidecar is what the scanner reads back, so it has to survive a
        // parse — not merely exist.
        $onDisk = ArtifactMeta::fromYaml((string) file_get_contents($this->layout->metaPath('11')));
        self::assertSame('11.4.4', $meta->coreVersion);
        self::assertSame('11.4.4', $onDisk->coreVersion);
        self::assertSame('11', $onDisk->coreMajor);
        self::assertSame('8.3.30', $onDisk->phpVersion);
        self::assertSame('mariadb:10.11', $onDisk->dbEngine);

        // The canonical tree is resolved by a full create-project and stays
        // module-free: drush is required only inside the throwaway copy.
        self::assertSame(
            ['composer create-project', 'composer validate', 'cp -a', 'ddev config'],
            array_slice($this->runner->summaries(), 0, 4),
        );
        self::assertContains('ddev delete', $this->runner->summaries(), 'The throwaway must always be torn down.');
        self::assertNotContains('composer require', $this->runner->summaries());

        // The build is long-running, so its progress is the operator's only
        // feedback that it is doing anything.
        self::assertStringContainsString('drupal/recommended-project:^11', implode("\n", $this->log));
        self::assertStringContainsString('Resolved drupal/core 11.4.4.', implode("\n", $this->log));
    }

    /**
     * The stability reaches the constraint, and only when asked for.
     *
     * `^12` resolves to nothing while Drupal 12 is in alpha, and a major
     * spends months there — which is exactly when compatibility work happens,
     * so the tool's main question is about the unreleased core.
     */
    public function testTheStabilityIsAppendedToTheConstraintWhenGiven(): void
    {
        $this->builder()->build('12', false, 'alpha');

        $lines = array_map(
            static fn (array $command): string => implode(' ', $command),
            $this->runner->commands(),
        );
        self::assertNotEmpty(array_filter(
            $lines,
            static fn (string $line): bool => str_contains($line, 'drupal/recommended-project:^12@alpha'),
        ));
    }

    /**
     * A failed stable resolve says what composer said, then what can be done
     * about it. Composer's own output reports that no version matched; nothing
     * in it suggests the major might be unreleased, or that a flag exists.
     */
    public function testAFailedStableResolveAddsThePreReleaseHintAfterComposersOwnWords(): void
    {
        $this->runner->failOn('composer create-project', 'Could not find a matching version of package');

        try {
            $this->builder()->build('12', false);
            self::fail('Expected the build to fail.');
        } catch (BuildException $e) {
            self::assertStringContainsString('Could not find a matching version', $e->getMessage());
            self::assertStringContainsString('--stability=alpha', $e->getMessage());
        }
    }

    /**
     * And no hint once a stability was asked for: the constraint is then not
     * the obvious suspect, and repeating advice already taken would bury
     * whatever composer actually said.
     */
    public function testAFailureWithAStabilityAlreadySetKeepsComposersMessageAlone(): void
    {
        $this->runner->failOn('composer create-project', 'Your requirements could not be resolved');

        try {
            $this->builder()->build('12', false, 'alpha');
            self::fail('Expected the build to fail.');
        } catch (BuildException $e) {
            self::assertStringContainsString('could not be resolved', $e->getMessage());
            self::assertStringNotContainsString('--stability', $e->getMessage());
        }
    }

    /** A misspelling is refused before composer is asked anything. */
    public function testAnUnknownStabilityIsRefusedBeforeAnythingIsRun(): void
    {
        try {
            $this->builder()->build('12', false, 'unstable');
            self::fail('Expected the build to be refused.');
        } catch (BuildException $e) {
            self::assertStringContainsString('Composer knows:', $e->getMessage());
        }

        self::assertSame([], $this->runner->summaries(), 'nothing was run');
    }

    public function testAnExistingArtifactSetIsRefusedUntilTheRebuildIsAskedForDeliberately(): void
    {
        // Constructed without a runner: the default really is a live process
        // runner, and this is the one path that must refuse before issuing a
        // single command.
        mkdir($this->layout->versionDir('11'), 0o700, true);
        $builder = new BaseArtifactBuilder(
            $this->layout,
            new ThrowawaySite($this->runner, $this->logger()),
            $this->world . '/scratch',
            $this->logger(),
        );

        $this->expectException(BuildException::class);
        $this->expectExceptionMessageMatches('/already exist.*--force/s');

        $builder->build('11', false);
    }

    /**
     * A rebuild replaces the previous set, and the previous set survives right
     * up to the moment the new one is complete.
     *
     * It used to be removed first, so the build resolved into the empty
     * directory it had just cleared — and a resolve that failed left the core
     * with nothing. The expensive, failure-prone part now happens beside the
     * live set; only a rename touches it.
     */
    public function testForceReplacesThePreviousArtifactSetWithoutRemovingItFirst(): void
    {
        mkdir($this->layout->treePath('11'), 0o700, true);
        file_put_contents($this->layout->treePath('11') . '/stale', 'from the previous build');

        $this->builder()->build('11', true);

        self::assertSame(
            'composer create-project',
            $this->runner->summaries()[0],
            'the build starts by resolving, not by deleting',
        );
        self::assertFileDoesNotExist($this->layout->treePath('11') . '/stale', 'the old set is gone afterwards');
        self::assertFileExists($this->layout->metaPath('11'), 'and the new one is in place');
    }

    /**
     * The point of the whole arrangement: a rebuild that fails leaves the
     * previous artifacts exactly where they were.
     *
     * Before, `--force` removed them and *then* resolved, so a network blip
     * during a routine "pick up the newer alpha" rebuild took the core out
     * entirely — every environment for it unusable until a build succeeded,
     * with the previous set unrecoverable.
     */
    public function testAFailedRebuildLeavesThePreviousArtifactSetIntact(): void
    {
        mkdir($this->layout->treePath('11'), 0o700, true);
        file_put_contents($this->layout->treePath('11') . '/stale', 'from the previous build');
        $this->runner->failOn('composer create-project', 'could not find a matching version');

        try {
            $this->builder()->build('11', true);
            self::fail('Expected a BuildException.');
        } catch (BuildException $e) {
            self::assertStringContainsString('could not find a matching version', $e->getMessage());
        }

        self::assertStringEqualsFile(
            $this->layout->treePath('11') . '/stale',
            'from the previous build',
            'the previous artifacts are untouched',
        );
        self::assertTrue(
            $this->logged('are untouched'),
            'and the run says so, because the alternative reading is that everything is gone',
        );
        self::assertSame([], $this->stagingDirectories(), 'the staged build is cleaned up');
    }

    /**
     * A build in progress must not read as a core anybody can be offered: it
     * would be handed out as a usable version by `base-artifacts:status`, by
     * prune, and by the core inference an unregistered module relies on.
     */
    public function testABuildInProgressIsInvisibleToEveryListing(): void
    {
        $seen = ['not run'];
        $this->midBuild(function () use (&$seen): void {
            $seen = $this->layout->versionsOnDisk();
        });

        $this->builder()->build('11', false);

        self::assertSame([], $seen, 'mid-build, nothing is listed');
        self::assertSame(['11'], $this->layout->versionsOnDisk(), 'and afterwards the finished set is');
    }

    /**
     * A build killed outright leaves a multi-gigabyte staging tree that
     * nothing else collects — prune protects the whole base-artifacts
     * directory, and the leading-dot name keeps it out of every listing. The
     * next build for that core is the next moment anyone is demonstrably not
     * relying on it.
     */
    public function testAStagingDirectoryLeftByAKilledBuildIsCollectedByTheNextOne(): void
    {
        $abandoned = $this->layout->baseArtifactsDir . '/.building-d11-deadbeef';
        mkdir($abandoned . '/11/tree', 0o700, true);

        $this->builder()->build('11', false);

        self::assertDirectoryDoesNotExist($abandoned);
        self::assertTrue($this->logged('interrupted build'));
        self::assertSame([], $this->stagingDirectories());
    }

    /**
     * The new set is built, and the old one cannot be moved out of the way.
     * Nothing is destroyed and the message says where both are, because the
     * expensive half of the work is finished and throwing it away over a
     * permissions problem would be the worst outcome available.
     */
    public function testAnUnmovableExistingSetFailsWithBothPathsNamed(): void
    {
        mkdir($this->layout->versionDir('11'), 0o700, true);
        $this->midBuild(function (): void {
            chmod($this->layout->baseArtifactsDir, 0o500);
        });

        try {
            $this->builder()->build('11', true);
            self::fail('Expected a BuildException.');
        } catch (BuildException $e) {
            self::assertStringContainsString('could not be moved aside', $e->getMessage());
            self::assertStringContainsString('is untouched', $e->getMessage());
            self::assertStringContainsString('.building-d11-', $e->getMessage(), 'where the new set is');
        } finally {
            chmod($this->layout->baseArtifactsDir, 0o700);
        }

        self::assertDirectoryExists($this->layout->versionDir('11'));
    }

    /**
     * And the same when there was nothing to replace: the finished set could
     * not take its name. Told where it is rather than deleted.
     */
    public function testAFinishedSetThatCannotTakeItsPlaceIsKeptAndNamed(): void
    {
        $this->midBuild(function (): void {
            chmod($this->layout->baseArtifactsDir, 0o500);
        });

        try {
            $this->builder()->build('11', false);
            self::fail('Expected a BuildException.');
        } catch (BuildException $e) {
            self::assertStringContainsString('could not be moved to', $e->getMessage());
            self::assertStringContainsString('Nothing has been deleted', $e->getMessage());
            self::assertStringContainsString('Move the finished set into place by hand', $e->getMessage());
        } finally {
            chmod($this->layout->baseArtifactsDir, 0o700);
        }
    }

    public function testAFailedStepRemovesThePartialArtifactSetSoItsPresenceStillMeansComplete(): void
    {
        $this->runner->failOn('composer validate', 'the base tree does not validate');

        try {
            $this->builder()->build('11', false);
            self::fail('Expected a BuildException.');
        } catch (BuildException $e) {
            self::assertStringContainsString('does not validate', $e->getMessage());
        }

        self::assertDirectoryDoesNotExist($this->layout->versionDir('11'));
        self::assertSame([], $this->stagingDirectories(), 'and nothing staged is left behind');
    }

    public function testAFailureInsideTheThrowawayInstallStillTearsTheEngineProjectDown(): void
    {
        // The engine project exists by this point, so a bare removal of the
        // tree would leave its containers running and its volumes allocated.
        $this->runner->failOn('ddev start', 'the throwaway project would not start');

        try {
            $this->builder()->build('11', false);
            self::fail('Expected the failure to propagate.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('would not start', $e->getMessage());
        }

        self::assertContains('ddev delete', $this->runner->summaries(), 'A failed build still tears down.');
        self::assertDirectoryDoesNotExist($this->layout->versionDir('11'));
    }

    public function testADumpThatWasNeverProducedFailsTheBuildRatherThanBeingRecordedAsCanonical(): void
    {
        $this->runner->skipDumpExport();

        try {
            $this->builder()->build('11', false);
            self::fail('Expected a BuildException.');
        } catch (BuildException $e) {
            self::assertStringContainsString('non-empty dump', $e->getMessage());
        }

        self::assertDirectoryDoesNotExist($this->layout->versionDir('11'));
    }

    public function testADirectoryThatCannotBeCreatedFailsTheBuildWithTheOffendingPath(): void
    {
        // A regular file where a directory has to go: unlike a permission bit
        // this is refused for every user, root included.
        file_put_contents($this->world . '/blocker', 'not a directory');
        $blockedLayout = new ArtifactLayout($this->world . '/blocker/base-artifacts');

        $builder = new BaseArtifactBuilder(
            $blockedLayout,
            new ThrowawaySite($this->runner, $this->logger()),
            $this->world . '/scratch',
            $this->logger(),
            $this->runner,
        );

        $this->expectException(BuildException::class);
        $this->expectExceptionMessageMatches('/Could not create artifact staging directory/');

        $builder->build('11', false);
    }

    public function testAScratchDirectoryThatCannotBeCreatedFailsTheBuild(): void
    {
        file_put_contents($this->world . '/blocker', 'not a directory');

        try {
            $this->builder($this->world . '/blocker/scratch')->build('11', false);
            self::fail('Expected a BuildException.');
        } catch (BuildException $e) {
            self::assertStringContainsString('scratch directory', $e->getMessage());
        }

        self::assertDirectoryDoesNotExist($this->layout->versionDir('11'));
    }
}

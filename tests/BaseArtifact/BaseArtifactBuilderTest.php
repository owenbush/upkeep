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

    public function testForceDiscardsThePreviousArtifactSetBeforeRebuilding(): void
    {
        mkdir($this->layout->treePath('11'), 0o700, true);
        file_put_contents($this->layout->treePath('11') . '/stale', 'from the previous build');

        $this->builder()->build('11', true);

        self::assertSame('rm -rf', $this->runner->summaries()[0]);
        self::assertSame($this->layout->versionDir('11'), $this->runner->commands()[0][2]);
        self::assertFileDoesNotExist($this->layout->treePath('11') . '/stale');
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
        $this->expectExceptionMessageMatches('/Could not create artifact directory/');

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

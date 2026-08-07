<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\PatchApplication;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\ServeResult;
use Upkeep\Adapter\WorkingCopyStatus;
use Upkeep\Adapter\VolumeProbe;
use Upkeep\Cockpit\Module;
use Upkeep\Command\PruneCommand;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Tests\Support\StubEngineAdapterFactory;
use Upkeep\Workflow\ExitCode;

final class PruneCommandTest extends TestCase
{
    private string $world;
    private string $cockpit;
    private string $projects;

    /** @var list<array{string, string}> */
    private array $teardowns = [];

    private string|false $originalHome;

    protected function setUp(): void
    {
        $this->teardowns = [];
        $this->world = (string) realpath(sys_get_temp_dir()) . '/upkeep-prune-cmd-test-' . bin2hex(random_bytes(4));
        // The projects root must resolve under $HOME (Docker providers only
        // mount the home directory). Point $HOME at the temp world so the
        // fixture stays in sys_get_temp_dir() and the real home is untouched.
        $this->originalHome = getenv('HOME');
        putenv('HOME=' . $this->world);
        $this->cockpit = $this->world . '/cockpit';
        $this->projects = $this->world . '/projects';

        mkdir($this->cockpit . '/base-artifacts/11/tree', 0755, true);
        file_put_contents($this->cockpit . '/base-artifacts/11/tree/index.php', 'x');
        file_put_contents($this->cockpit . '/base-artifacts/11/canonical', '');
        mkdir($this->cockpit . '/fixtures', 0755, true);
        file_put_contents($this->cockpit . '/fixtures/shared.sql.gz', 'library dump');
        file_put_contents($this->cockpit . '/registry.yml', implode("\n", [
            'modules:',
            '  conditions_helper:',
            '    project: project/conditions_helper',
            '    core_versions: ["10", "11"]',
        ]));

        $p = $this->projects . '/upkeep-conditions-helper-d11';
        mkdir($p . '/.ddev/upkeep/materialized', 0755, true);
        mkdir($p . '/.ddev/upkeep/snapshots', 0755, true);
        mkdir($p . '/module/tests/fixtures', 0755, true);
        file_put_contents($p . '/.upkeep-env.yml', implode("\n", [
            'module: conditions_helper',
            'core_major: \'11\'',
            'seed_core_version: 11.2.5',
            'addon_version: v1.0.0',
            'created_at: \'2026-01-01T00:00:00+00:00\'',
        ]));
        file_put_contents($p . '/.ddev/upkeep/materialized/older.sql', str_repeat('a', 1000));
        file_put_contents($p . '/.ddev/upkeep/snapshots/older.meta', "materialized_at=2026-05-01T00:00:00Z\n");
        file_put_contents($p . '/.ddev/upkeep/materialized/newer.sql', str_repeat('b', 1000));
        file_put_contents($p . '/.ddev/upkeep/snapshots/newer.meta', "materialized_at=2026-07-01T00:00:00Z\n");
        file_put_contents($p . '/.ddev/upkeep/materialized/kept.sql', str_repeat('c', 1000));
        file_put_contents($p . '/.ddev/upkeep/materialized/kept.sql.keep', '');
        file_put_contents($p . '/.ddev/upkeep/snapshots/kept.meta', "materialized_at=2026-07-20T00:00:00Z\n");
        file_put_contents($p . '/module/tests/fixtures/base.sql.gz', 'committed dump');

        // A keep-marked environment.
        mkdir($this->projects . '/upkeep-kept-env-d11', 0755, true);
        file_put_contents($this->projects . '/upkeep-kept-env-d11/.keep', '');

        // A plain prunable environment (no keep marks anywhere): the d11 env
        // above is tree-protected by its keep-marked snapshot (escalation).
        mkdir($this->projects . '/upkeep-conditions-helper-d10', 0755, true);
        file_put_contents($this->projects . '/upkeep-conditions-helper-d10/.upkeep-env.yml', implode("\n", [
            'module: conditions_helper',
            'core_major: \'10\'',
            'seed_core_version: 10.6.14',
            'addon_version: v1.0.0',
            'created_at: \'2026-01-01T00:00:00+00:00\'',
        ]));
    }

    protected function tearDown(): void
    {
        putenv($this->originalHome === false ? 'HOME' : 'HOME=' . $this->originalHome);
        exec('rm -rf ' . escapeshellarg($this->world));
    }

    /**
     * @param array<string, bool|string> $args
     */
    private function runPrune(array $args): CommandTester
    {
        $adapter = new class ($this->teardowns) implements EngineAdapterInterface {
            /** @param list<array{string, string}> $teardowns */
            public function __construct(private array &$teardowns)
            {
            }

            public function ensureEnv(Module $module, string $coreMajor): Environment
            {
                throw new \BadMethodCallException('not used');
            }

            public function applyMr(Environment $environment, MergeRequest $mergeRequest): void
            {
            }

            public function applyPatch(Environment $environment, PatchApplication $patch): void
            {
            }

            public function loadFixture(Environment $environment, string $fixtureName): void
            {
            }

            public function runChecks(Environment $environment, array $checks = []): CheckRunResult
            {
                throw new \BadMethodCallException('not used');
            }

            public function serve(Environment $environment): ServeResult
            {
                throw new \BadMethodCallException('not used');
            }

            public function resolveEnvPath(string $moduleName, string $coreMajor): ?string
            {
                return null;
            }

            public function teardown(Module $module, string $coreMajor): void
            {
                // Read-modify-write, not `[] =`: the property is a reference
                // alias to the test's own array, which is where the
                // assertion reads the recording from — so appending in
                // place would look write-only to static analysis.
                $this->teardowns = [...$this->teardowns, [$module->name, $coreMajor]];
            }
            public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus
            {
                return null;
            }
            public function checkoutBranch(Environment $environment, string $branch): void
            {
            }
        };

        $tester = new CommandTester(new PruneCommand(
            new StubEngineAdapterFactory($adapter),
            new VolumeProbe(static fn (array $c): ?string => null),
        ));
        $tester->execute([
            '--cockpit' => $this->cockpit,
            '--projects-root' => $this->projects,
            ...$args,
        ]);

        return $tester;
    }

    private function snapshotPath(string $name): string
    {
        return $this->projects . '/upkeep-conditions-helper-d11/.ddev/upkeep/materialized/' . $name . '.sql';
    }

    public function testEveryVariantIsADryRunWithoutYes(): void
    {
        $variants = [
            ['--trees' => true],
            ['--snapshots' => true],
            ['--projects' => true],
            ['--all' => true],
        ];
        foreach ($variants as $variant) {
            $tester = $this->runPrune($variant);

            $tester->assertCommandIsSuccessful();
            self::assertStringContainsString('Dry run: nothing was deleted', $tester->getDisplay());
            self::assertSame([], $this->teardowns, 'dry run must never tear anything down');
            self::assertFileExists($this->snapshotPath('older'));
            self::assertFileExists($this->snapshotPath('newer'));
            self::assertDirectoryExists($this->projects . '/upkeep-conditions-helper-d11');
        }
    }

    public function testDryRunListsCandidatesWithReclaimableTotal(): void
    {
        $tester = $this->runPrune(['--snapshots' => true]);

        $display = $tester->getDisplay();
        self::assertStringContainsString('older.sql', $display);
        self::assertStringContainsString('newer.sql', $display);
        self::assertStringNotContainsString('kept.sql', $display);
        self::assertStringContainsString('Total reclaimable:', $display);
    }

    public function testConfirmedSnapshotPruneDeletesArtifactsAndMetaButNeverProtectedFiles(): void
    {
        $tester = $this->runPrune(['--snapshots' => true, '--yes' => true]);

        $tester->assertCommandIsSuccessful();
        self::assertFileDoesNotExist($this->snapshotPath('older'));
        self::assertFileDoesNotExist($this->snapshotPath('newer'));
        self::assertFileDoesNotExist(
            $this->projects . '/upkeep-conditions-helper-d11/.ddev/upkeep/snapshots/older.meta',
        );
        // Protected: keep-marked snapshot, committed dumps, base artifacts.
        self::assertFileExists($this->snapshotPath('kept'));
        self::assertFileExists($this->projects . '/upkeep-conditions-helper-d11/module/tests/fixtures/base.sql.gz');
        self::assertFileExists($this->cockpit . '/fixtures/shared.sql.gz');
        self::assertDirectoryExists($this->cockpit . '/base-artifacts/11');
    }

    public function testConfirmedSnapshotPruneHonorsKeepLatestPerProject(): void
    {
        $this->runPrune(['--snapshots' => true, '--yes' => true, '--keep-latest' => '1']);

        self::assertFileDoesNotExist($this->snapshotPath('older'));
        self::assertFileExists(
            $this->snapshotPath('newer'),
            'keep-latest=1 must retain the newest unprotected snapshot',
        );
        self::assertFileExists($this->snapshotPath('kept'));
    }

    public function testConfirmedTreePruneGoesThroughAdapterTeardownAndSkipsKeepMarked(): void
    {
        $tester = $this->runPrune(['--trees' => true, '--yes' => true]);

        $tester->assertCommandIsSuccessful();
        // Only the plain d10 env is torn down: the d11 env is protected by its
        // keep-marked snapshot (escalation), upkeep-kept-env-d11 by its .keep.
        self::assertSame([['conditions_helper', '10']], $this->teardowns);
        self::assertDirectoryExists($this->projects . '/upkeep-kept-env-d11', 'keep-marked environment must survive');
        self::assertDirectoryExists(
            $this->projects . '/upkeep-conditions-helper-d11',
            'environment holding a keep-marked snapshot must survive a tree prune',
        );
        self::assertFileExists($this->snapshotPath('kept'));
        self::assertDirectoryExists($this->cockpit . '/base-artifacts/11');
    }

    public function testOlderThanExcludesRecentEnvironments(): void
    {
        // Environment created 2026-01-01; "older than 10000 days" matches nothing.
        $tester = $this->runPrune(['--trees' => true, '--older-than' => '10000d', '--yes' => true]);

        $tester->assertCommandIsSuccessful();
        self::assertStringContainsString('Nothing to prune', $tester->getDisplay());
        self::assertSame([], $this->teardowns);
    }

    public function testRejectsInvalidOlderThanSyntax(): void
    {
        $tester = $this->runPrune(['--trees' => true, '--older-than' => '30']);

        self::assertSame(ExitCode::INFRASTRUCTURE, $tester->getStatusCode());
        self::assertStringContainsString('Invalid duration', $tester->getDisplay());
    }

    /**
     * A tree upkeep cannot attribute to a registered (module x core) pair is
     * reported and left alone, not guessed at. Teardown goes through the
     * engine and needs to know *what* it is tearing down; deleting the
     * directory blind would leave the engine's containers and volumes behind.
     * The run still succeeds — everything attributable was reclaimed.
     */
    public function testAnUnattributableTreeIsWarnedAboutAndLeftOnDiskWhileTheRestIsPruned(): void
    {
        // No .upkeep-env.yml, and no (module x core) in the registry produces
        // this project name — d12 is not a tracked core version.
        $orphan = $this->projects . '/upkeep-conditions-helper-d12';
        mkdir($orphan, 0755, true);

        $tester = $this->runPrune(['--trees' => true, '--yes' => true]);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('Skipped', $display);
        self::assertStringContainsString('refusing to guess', $display);
        self::assertStringContainsString('Pruned 1 item(s)', $display);
        self::assertDirectoryExists($orphan);
        // The attributable environment was still torn down in the same run.
        self::assertSame([['conditions_helper', '10']], $this->teardowns);
    }

    /**
     * An under-reported inventory can only under-delete, so the run continues
     * — but the operator is told what could not be looked at, because a prune
     * that silently skipped half the disk would read as "nothing left to
     * reclaim".
     */
    public function testAnUnreadablePartOfTheInventoryIsWarnedAboutBeforeThePlanIsShown(): void
    {
        $project = $this->projects . '/upkeep-conditions-helper-d11';
        exec('rm -rf ' . escapeshellarg($project . '/.ddev/upkeep/materialized'));
        symlink($this->world, $project . '/.ddev/upkeep/materialized');

        $tester = $this->runPrune(['--snapshots' => true]);

        $tester->assertCommandIsSuccessful();
        $display = $tester->getDisplay();
        self::assertStringContainsString('is a symlink — skipped', $display);
        self::assertStringNotContainsString('older.sql', $display);
    }

    public function testRequiresExactlyOneScopeFlag(): void
    {
        $none = $this->runPrune([]);
        self::assertSame(ExitCode::INFRASTRUCTURE, $none->getStatusCode());

        $two = $this->runPrune(['--trees' => true, '--snapshots' => true]);
        self::assertSame(ExitCode::INFRASTRUCTURE, $two->getStatusCode());
    }
}

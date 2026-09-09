<?php

declare(strict_types=1);

namespace Upkeep\Tests\Maintenance;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\BaseRefresh;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\GitRemote;
use Upkeep\Adapter\IssueBranch;
use Upkeep\Adapter\PatchApplication;
use Upkeep\Adapter\PatchPromotion;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\ServeResult;
use Upkeep\Adapter\WorkingCopyStatus;
use Upkeep\Cockpit\Module;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Adapter\AdapterException;
use Upkeep\Maintenance\Category;
use Upkeep\Maintenance\InventoryItem;
use Upkeep\Maintenance\PruneExecutor;
use Upkeep\Maintenance\PruneSelector;

final class PruneExecutorTest extends TestCase
{
    private string $world;

    /** @var list<array{string, string}> */
    private array $teardowns = [];

    protected function setUp(): void
    {
        $this->world = sys_get_temp_dir() . '/upkeep-executor-test-' . bin2hex(random_bytes(4));
        mkdir($this->world . '/upkeep-conditions-helper-d11/.ddev/upkeep/materialized', 0755, true);
        mkdir($this->world . '/upkeep-conditions-helper-d11/.ddev/upkeep/snapshots', 0755, true);
        file_put_contents(
            $this->world . '/upkeep-conditions-helper-d11/.ddev/upkeep/materialized/alpha.sql',
            'snapshot',
        );
        file_put_contents(
            $this->world . '/upkeep-conditions-helper-d11/.ddev/upkeep/snapshots/alpha.meta',
            "engine=mariadb\n",
        );
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->world));
    }

    private function executor(): PruneExecutor
    {
        return new PruneExecutor(
            new PruneSelector(['/cockpit/base-artifacts', '/cockpit/fixtures']),
            $this->adapter(),
            ['conditions_helper' => new Module('conditions_helper', 'project/conditions_helper', ['10', '11'])],
            static function (string $line): void {
            },
        );
    }

    private function adapter(): EngineAdapterInterface
    {
        return new class ($this->teardowns) implements EngineAdapterInterface {
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

            public function applyPatch(
                Environment $environment,
                PatchApplication $patch,
                BaseRefresh $refresh = BaseRefresh::Update,
            ): void {
            }

            public function startWork(
                Environment $environment,
                IssueBranch $branch,
                ?string $baseBranch = null,
                BaseRefresh $refresh = BaseRefresh::Update,
            ): bool {
                return false;
            }

            public function pushWork(Environment $environment, IssueBranch $branch, GitRemote $remote): string
            {
                return 'abc1234';
            }

            public function recordedBaseBranch(Environment $environment): ?string
            {
                return null;
            }

            public function promotePatch(
                Environment $environment,
                PatchApplication $patch,
                IssueBranch $branch,
                string $commitMessage,
                BaseRefresh $refresh = BaseRefresh::Update,
                bool $allowPartial = false,
            ): PatchPromotion {
                throw new \BadMethodCallException();
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
    }

    public function testTreeCandidateIsTornDownThroughTheAdapter(): void
    {
        $tree = new InventoryItem(
            path: $this->world . '/upkeep-conditions-helper-d11',
            category: Category::ProjectTree,
            sizeBytes: 4096,
            module: 'conditions_helper',
            coreMajor: '11',
            projectName: 'upkeep-conditions-helper-d11',
        );

        $outcome = $this->executor()->execute([$tree]);

        self::assertSame([['conditions_helper', '11']], $this->teardowns);
        self::assertSame(4096, $outcome->freedBytes);
        self::assertSame([], $outcome->skipped);
    }

    public function testUnattributableTreeIsResolvedViaTheRegistryNameMap(): void
    {
        // No meta dotfile (partial provision) but the directory name matches a
        // registered module x core pair: resolvable, torn down.
        $tree = new InventoryItem(
            path: $this->world . '/upkeep-conditions-helper-d11',
            category: Category::ProjectTree,
            sizeBytes: 100,
            projectName: 'upkeep-conditions-helper-d11',
        );

        $this->executor()->execute([$tree]);

        self::assertSame([['conditions_helper', '11']], $this->teardowns);
    }

    public function testUnresolvableTreeIsSkippedWithReasonNotDeleted(): void
    {
        $tree = new InventoryItem(
            path: $this->world . '/upkeep-unknown-thing-d9',
            category: Category::ProjectTree,
            sizeBytes: 100,
            projectName: 'upkeep-unknown-thing-d9',
        );

        $outcome = $this->executor()->execute([$tree]);

        self::assertSame([], $this->teardowns);
        self::assertSame(0, $outcome->freedBytes);
        self::assertCount(1, $outcome->skipped);
    }

    public function testSnapshotDeletionRemovesArtifactAndItsMetaFile(): void
    {
        $artifact = $this->world . '/upkeep-conditions-helper-d11/.ddev/upkeep/materialized/alpha.sql';
        $meta = $this->world . '/upkeep-conditions-helper-d11/.ddev/upkeep/snapshots/alpha.meta';
        $snapshot = new InventoryItem(
            path: $artifact,
            category: Category::Snapshot,
            sizeBytes: 8,
            module: 'conditions_helper',
            projectName: 'upkeep-conditions-helper-d11',
        );

        $outcome = $this->executor()->execute([$snapshot]);

        self::assertFileDoesNotExist($artifact);
        self::assertFileDoesNotExist($meta);
        self::assertSame(8, $outcome->freedBytes);
    }

    public function testVolumeBytesAreCountedWithTheirProjectsTeardown(): void
    {
        $tree = new InventoryItem(
            path: $this->world . '/upkeep-conditions-helper-d11',
            category: Category::ProjectTree,
            sizeBytes: 1000,
            module: 'conditions_helper',
            coreMajor: '11',
            projectName: 'upkeep-conditions-helper-d11',
        );
        $volume = new InventoryItem(
            path: 'upkeep-conditions-helper-d11-mariadb',
            category: Category::ProjectVolume,
            sizeBytes: 500,
            projectName: 'upkeep-conditions-helper-d11',
        );

        $outcome = $this->executor()->execute([$tree, $volume]);

        self::assertSame([['conditions_helper', '11']], $this->teardowns);
        self::assertSame(1500, $outcome->freedBytes);
    }

    public function testOrphanVolumeWithoutTreeCandidateIsSkipped(): void
    {
        $volume = new InventoryItem(
            path: 'upkeep-conditions-helper-d11-mariadb',
            category: Category::ProjectVolume,
            sizeBytes: 500,
            projectName: 'upkeep-conditions-helper-d11',
        );

        $outcome = $this->executor()->execute([$volume]);

        self::assertSame([], $this->teardowns);
        self::assertSame(0, $outcome->freedBytes);
        self::assertCount(1, $outcome->skipped);
    }

    public function testTeardownFailureSkipsItemInsteadOfAborting(): void
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

            public function applyPatch(
                Environment $environment,
                PatchApplication $patch,
                BaseRefresh $refresh = BaseRefresh::Update,
            ): void {
            }

            public function startWork(
                Environment $environment,
                IssueBranch $branch,
                ?string $baseBranch = null,
                BaseRefresh $refresh = BaseRefresh::Update,
            ): bool {
                return false;
            }

            public function pushWork(Environment $environment, IssueBranch $branch, GitRemote $remote): string
            {
                return 'abc1234';
            }

            public function recordedBaseBranch(Environment $environment): ?string
            {
                return null;
            }

            public function promotePatch(
                Environment $environment,
                PatchApplication $patch,
                IssueBranch $branch,
                string $commitMessage,
                BaseRefresh $refresh = BaseRefresh::Update,
                bool $allowPartial = false,
            ): PatchPromotion {
                throw new \BadMethodCallException();
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
                if ($coreMajor === '11') {
                    throw new AdapterException('local work detected');
                }
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

        $executor = new PruneExecutor(
            new PruneSelector(['/cockpit/base-artifacts', '/cockpit/fixtures']),
            $adapter,
            ['conditions_helper' => new Module('conditions_helper', 'project/conditions_helper', ['10', '11'])],
            static function (): void {
            },
        );

        $dirty = new InventoryItem(
            path: $this->world . '/upkeep-conditions-helper-d11',
            category: Category::ProjectTree,
            sizeBytes: 4096,
            module: 'conditions_helper',
            coreMajor: '11',
            projectName: 'upkeep-conditions-helper-d11',
        );
        $clean = new InventoryItem(
            path: $this->world . '/upkeep-conditions-helper-d10',
            category: Category::ProjectTree,
            sizeBytes: 2048,
            module: 'conditions_helper',
            coreMajor: '10',
            projectName: 'upkeep-conditions-helper-d10',
        );

        $outcome = $executor->execute([$dirty, $clean]);

        self::assertSame([['conditions_helper', '10']], $this->teardowns);
        self::assertSame(2048, $outcome->freedBytes);
        self::assertCount(1, $outcome->skipped);
        self::assertStringContainsString('local work detected', $outcome->skipped[0][1]);
    }

    /**
     * prune is the only destructive command and its report is the operator's
     * only feedback. Over-reporting reclaimed space on a deletion that did not
     * happen is the wrong direction for a defect.
     */
    public function testASnapshotThatCannotBeRemovedIsSkippedNotReportedAsFreedSpace(): void
    {
        $dir = $this->world . '/upkeep-conditions-helper-d11/.ddev/upkeep/materialized';
        $artifact = $dir . '/alpha.sql';
        $snapshot = new InventoryItem(
            path: $artifact,
            category: Category::Snapshot,
            sizeBytes: 8,
            module: 'conditions_helper',
            projectName: 'upkeep-conditions-helper-d11',
        );
        chmod($dir, 0o500);

        try {
            $outcome = $this->executor()->execute([$snapshot]);

            self::assertFileExists($artifact);
            self::assertSame(0, $outcome->freedBytes, 'Bytes that were not reclaimed must not be reported as freed.');
            self::assertSame([], $outcome->deleted);
            self::assertCount(1, $outcome->skipped);
            self::assertStringContainsString('could not be removed', $outcome->skipped[0][1]);
        } finally {
            chmod($dir, 0o700);
        }
    }

    public function testASnapshotWhoseSidecarSurvivesIsStillReclaimedAndTheLeftoverIsReported(): void
    {
        // The sidecar lives in a different directory from the artifact, so it
        // can be unremovable on its own. The snapshot bytes really were
        // reclaimed, so the run must count them — but the operator is told the
        // .meta was left behind rather than being silently lied to.
        $project = $this->world . '/upkeep-conditions-helper-d11';
        $artifact = $project . '/.ddev/upkeep/materialized/alpha.sql';
        $metaDir = $project . '/.ddev/upkeep/snapshots';
        $lines = [];
        $executor = new PruneExecutor(
            new PruneSelector([]),
            $this->adapter(),
            [],
            static function (string $line) use (&$lines): void {
                $lines[] = $line;
            },
        );
        chmod($metaDir, 0o500);

        try {
            $outcome = $executor->execute([new InventoryItem(
                path: $artifact,
                category: Category::Snapshot,
                sizeBytes: 8,
                projectName: 'upkeep-conditions-helper-d11',
            )]);

            self::assertFileDoesNotExist($artifact);
            self::assertFileExists($metaDir . '/alpha.meta');
            self::assertSame(8, $outcome->freedBytes);
            self::assertCount(1, $outcome->deleted);
            self::assertStringContainsString('sidecar', implode("\n", $lines));
        } finally {
            chmod($metaDir, 0o700);
        }
    }

    public function testATreeAttributedToAnUnregisteredModuleIsStillTornDownThroughTheAdapter(): void
    {
        // The dotfile attribution is authoritative: an environment provisioned
        // for a module that has since been removed from registry.yml must still
        // be disposable, and never with a bare rm -rf.
        $tree = new InventoryItem(
            path: $this->world . '/upkeep-conditions-helper-d11',
            category: Category::ProjectTree,
            sizeBytes: 4096,
            module: 'deregistered_module',
            coreMajor: '10',
            projectName: 'upkeep-deregistered-module-d10',
        );

        $outcome = $this->executor()->execute([$tree]);

        self::assertSame([['deregistered_module', '10']], $this->teardowns);
        self::assertSame(4096, $outcome->freedBytes);
        self::assertSame([], $outcome->skipped);
    }

    public function testExecutorRefusesProtectedItemsEvenIfHandedThemDirectly(): void
    {
        // Defense in depth: even a candidate list that bypassed the selector
        // must never delete a protected path.
        $protected = new InventoryItem(
            path: '/cockpit/base-artifacts/11',
            category: Category::ProjectTree, // mislabeled on purpose
            sizeBytes: 100,
            projectName: 'upkeep-conditions-helper-d11',
        );

        $this->expectException(\RuntimeException::class);
        $this->executor()->execute([$protected]);
    }
}

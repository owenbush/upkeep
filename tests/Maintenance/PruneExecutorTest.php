<?php

declare(strict_types=1);

namespace Upkeep\Tests\Maintenance;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\ServeResult;
use Upkeep\Cockpit\Module;
use Upkeep\Gitlab\MergeRequest;
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
        file_put_contents($this->world . '/upkeep-conditions-helper-d11/.ddev/upkeep/materialized/alpha.sql', 'snapshot');
        file_put_contents($this->world . '/upkeep-conditions-helper-d11/.ddev/upkeep/snapshots/alpha.meta', "engine=mariadb\n");
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->world));
    }

    private function executor(): PruneExecutor
    {
        $adapter = new class($this->teardowns) implements EngineAdapterInterface {
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

            public function teardown(Module $module, string $coreMajor): void
            {
                $this->teardowns[] = [$module->name, $coreMajor];
            }
        };

        return new PruneExecutor(
            new PruneSelector(['/cockpit/base-artifacts', '/cockpit/fixtures']),
            $adapter,
            ['conditions_helper' => new Module('conditions_helper', 'project/conditions_helper', ['10', '11'])],
            static function (string $line): void {
            },
        );
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

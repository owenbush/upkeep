<?php

declare(strict_types=1);

namespace Upkeep\Tests\Maintenance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Maintenance\Category;
use Upkeep\Maintenance\InventoryItem;
use Upkeep\Maintenance\InventoryScanner;

final class InventoryScannerTest extends TestCase
{
    private string $world;
    private string $cockpitRoot;
    private string $projectsRoot;

    protected function setUp(): void
    {
        $this->world = sys_get_temp_dir() . '/upkeep-scanner-test-' . bin2hex(random_bytes(4));
        $this->cockpitRoot = $this->world . '/cockpit';
        $this->projectsRoot = $this->world . '/projects';

        // Cockpit: one base artifact set, one library dump.
        mkdir($this->cockpitRoot . '/base-artifacts/11/tree', 0755, true);
        file_put_contents($this->cockpitRoot . '/base-artifacts/11/tree/index.php', str_repeat('x', 2048));
        file_put_contents($this->cockpitRoot . '/base-artifacts/11/canonical', '');
        file_put_contents($this->cockpitRoot . '/base-artifacts/11/clean-install.sql.gz', 'dump');
        mkdir($this->cockpitRoot . '/fixtures', 0755, true);
        file_put_contents($this->cockpitRoot . '/fixtures/shared.sql.gz', 'library dump');

        // A fully provisioned project with snapshots and a committed module dump.
        $p = $this->projectsRoot . '/upkeep-conditions-helper-d11';
        mkdir($p . '/.ddev/upkeep/materialized', 0755, true);
        mkdir($p . '/.ddev/upkeep/snapshots', 0755, true);
        mkdir($p . '/module/tests/fixtures', 0755, true);
        file_put_contents($p . '/.upkeep-env.yml', implode("\n", [
            'module: conditions_helper',
            'core_major: \'11\'',
            'seed_core_version: 11.2.5',
            'addon_version: v1.0.0',
            'created_at: \'2026-05-01T00:00:00+00:00\'',
            'last_used_at: \'2026-06-01T00:00:00+00:00\'',
        ]));
        file_put_contents($p . '/.ddev/upkeep/materialized/alpha.sql', str_repeat('a', 100_000));
        file_put_contents(
            $p . '/.ddev/upkeep/snapshots/alpha.meta',
            "engine=mariadb:10.11\nmaterialized_at=2026-06-10T00:00:00Z\n",
        );
        file_put_contents($p . '/.ddev/upkeep/materialized/beta.sql', str_repeat('b', 50_000));
        file_put_contents($p . '/.ddev/upkeep/materialized/beta.sql.keep', '');
        file_put_contents(
            $p . '/.ddev/upkeep/snapshots/beta.meta',
            "engine=mariadb:10.11\nmaterialized_at=2026-06-20T00:00:00Z\n",
        );
        file_put_contents($p . '/module/tests/fixtures/base.sql.gz', 'committed dump');

        // A partial provision: no completion marker dotfile.
        mkdir($this->projectsRoot . '/upkeep-partial-d10', 0755, true);
        file_put_contents($this->projectsRoot . '/upkeep-partial-d10/leftover.txt', 'x');

        // A keep-marked tree.
        mkdir($this->projectsRoot . '/upkeep-kept-d11', 0755, true);
        file_put_contents($this->projectsRoot . '/upkeep-kept-d11/.keep', '');

        // Not an upkeep environment: must be ignored entirely.
        mkdir($this->projectsRoot . '/random-dir', 0755, true);
        file_put_contents($this->projectsRoot . '/random-dir/data.bin', str_repeat('z', 4096));
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->world));
    }

    /**
     * @return list<InventoryItem>
     */
    private function scan(): array
    {
        return (new InventoryScanner(new Cockpit($this->cockpitRoot), $this->projectsRoot))->scan();
    }

    /**
     * @param list<InventoryItem> $items
     */
    private static function byPathSuffix(array $items, string $suffix): InventoryItem
    {
        foreach ($items as $item) {
            if (str_ends_with($item->path, $suffix)) {
                return $item;
            }
        }
        self::fail('No inventory item with path suffix ' . $suffix);
    }

    public function testBaseArtifactVersionDirIsInventoriedAsBaseArtifact(): void
    {
        $item = self::byPathSuffix($this->scan(), '/base-artifacts/11');

        self::assertSame(Category::BaseArtifact, $item->category);
        self::assertSame('11', $item->coreMajor);
        self::assertGreaterThan(0, $item->sizeBytes);
    }

    public function testProjectTreeIsAttributedFromItsMetaDotfile(): void
    {
        $item = self::byPathSuffix($this->scan(), '/upkeep-conditions-helper-d11');

        self::assertSame(Category::ProjectTree, $item->category);
        self::assertSame('conditions_helper', $item->module);
        self::assertSame('11', $item->coreMajor);
        self::assertSame('upkeep-conditions-helper-d11', $item->projectName);
        // last_used_at wins over created_at for age purposes.
        self::assertSame('2026-06-01T00:00:00+00:00', $item->lastUsedAt?->format(\DateTimeInterface::ATOM));
        self::assertFalse($item->keepMarked);
    }

    public function testProjectTreeSizeExcludesItsMaterializedSnapshots(): void
    {
        $items = $this->scan();
        $tree = self::byPathSuffix($items, '/upkeep-conditions-helper-d11');
        $alpha = self::byPathSuffix($items, '/materialized/alpha.sql');
        $beta = self::byPathSuffix($items, '/materialized/beta.sql');

        $duWholeTree = (int) exec(
            'du -sk ' . escapeshellarg($this->projectsRoot . '/upkeep-conditions-helper-d11'),
        ) * 1024;

        self::assertGreaterThan(0, $tree->sizeBytes);
        self::assertLessThan($duWholeTree, $tree->sizeBytes, 'tree size must not double-count snapshot bytes');
        self::assertGreaterThan(0, $alpha->sizeBytes);
        self::assertGreaterThan(0, $beta->sizeBytes);
    }

    public function testSnapshotsCarryAgeFromMetaAndKeepMarker(): void
    {
        $items = $this->scan();
        $alpha = self::byPathSuffix($items, '/materialized/alpha.sql');
        $beta = self::byPathSuffix($items, '/materialized/beta.sql');

        self::assertSame(Category::Snapshot, $alpha->category);
        self::assertSame('conditions_helper', $alpha->module);
        self::assertSame('upkeep-conditions-helper-d11', $alpha->projectName);
        self::assertSame('2026-06-10', $alpha->lastUsedAt?->format('Y-m-d'));
        self::assertFalse($alpha->keepMarked);
        self::assertTrue($beta->keepMarked, 'beta.sql.keep marker must flag the snapshot as kept');
    }

    public function testCommittedDumpsAreInventoriedAsFixtureDumps(): void
    {
        $items = $this->scan();
        $moduleDump = self::byPathSuffix($items, '/module/tests/fixtures/base.sql.gz');
        $libraryDump = self::byPathSuffix($items, '/fixtures/shared.sql.gz');

        self::assertSame(Category::FixtureDump, $moduleDump->category);
        self::assertSame('conditions_helper', $moduleDump->module);
        self::assertSame(Category::FixtureDump, $libraryDump->category);
    }

    public function testPartialProvisionHasUnknownAge(): void
    {
        $item = self::byPathSuffix($this->scan(), '/upkeep-partial-d10');

        self::assertSame(Category::ProjectTree, $item->category);
        self::assertNull($item->lastUsedAt);
        self::assertNull($item->module);
    }

    public function testKeepMarkedTreeIsFlagged(): void
    {
        $item = self::byPathSuffix($this->scan(), '/upkeep-kept-d11');

        self::assertTrue($item->keepMarked);
    }

    public function testNonUpkeepDirectoriesAreIgnored(): void
    {
        foreach ($this->scan() as $item) {
            self::assertStringNotContainsString('random-dir', $item->path);
        }
    }

    public function testMissingRootsYieldEmptyInventorySlices(): void
    {
        $scanner = new InventoryScanner(new Cockpit($this->world . '/nope'), $this->world . '/also-nope');

        self::assertSame([], $scanner->scan());
    }

    /**
     * glob() resolves through symlinked path components, so a symlinked
     * materialized/ directory would enumerate files outside the environment
     * tree — and prune would then unlink them.
     */
    public function testASymlinkedMaterializedDirectoryIsNotEnumerated(): void
    {
        $elsewhere = $this->world . '/elsewhere';
        mkdir($elsewhere, 0o700, true);
        file_put_contents($elsewhere . '/not-ours.sql', 'someone else\'s data');

        $project = $this->projectsRoot . '/upkeep-symlinked-d11';
        mkdir($project . '/.ddev/upkeep', 0o700, true);
        symlink($elsewhere, $project . '/.ddev/upkeep/materialized');

        foreach ($this->scan() as $item) {
            self::assertStringNotContainsString('not-ours.sql', $item->path);
        }
    }

    public function testAnUnreadableDirectoryIsWarnedAboutRatherThanReportedAsEmpty(): void
    {
        chmod($this->cockpitRoot . '/fixtures', 0o000);

        try {
            $scanner = new InventoryScanner(new Cockpit($this->cockpitRoot), $this->projectsRoot);
            $scanner->scan();

            self::assertNotSame([], $scanner->warnings());
            self::assertStringContainsString('/fixtures', implode("\n", $scanner->warnings()));
        } finally {
            chmod($this->cockpitRoot . '/fixtures', 0o755);
        }
    }

    public function testAnUnreadableProjectsRootIsWarnedAboutRatherThanReportedAsNoEnvironments(): void
    {
        chmod($this->projectsRoot, 0o000);

        try {
            $scanner = new InventoryScanner(new Cockpit($this->cockpitRoot), $this->projectsRoot);
            $items = $scanner->scan();

            // Under-reporting fails safe for prune, but "there are no
            // environments" and "I could not look" must not read the same.
            foreach ($items as $item) {
                self::assertStringNotContainsString('/projects/', $item->path);
            }
            self::assertStringContainsString($this->projectsRoot, implode("\n", $scanner->warnings()));
        } finally {
            chmod($this->projectsRoot, 0o755);
        }
    }

    /**
     * @return array<string, array{string}>
     */
    public static function unusableMetaProvider(): array
    {
        return [
            'malformed YAML' => ["module: [conditions_helper\ncore_major: '11'\n"],
            'not a mapping' => ["just a string\n"],
        ];
    }

    #[DataProvider('unusableMetaProvider')]
    public function testAnUnusableEnvMetaDotfileDegradesToUnknownAttributionNotAFailedScan(string $meta): void
    {
        // A partial or hand-mangled provision is still disk usage the operator
        // needs to see; only its attribution and age are unknown.
        $project = $this->projectsRoot . '/upkeep-mangled-d11';
        mkdir($project, 0o755, true);
        file_put_contents($project . '/.upkeep-env.yml', $meta);

        $item = self::byPathSuffix($this->scan(), '/upkeep-mangled-d11');

        self::assertSame(Category::ProjectTree, $item->category);
        self::assertNull($item->module);
        self::assertNull($item->coreMajor);
        self::assertNull($item->lastUsedAt);
    }

    public function testAnUnparseableLastUsedAtReadsAsUnknownAgeRatherThanNow(): void
    {
        // Age drives prune --older-than. A timestamp that cannot be parsed must
        // yield "unknown", which the selector excludes, rather than a date that
        // would make the environment look prunable.
        $project = $this->projectsRoot . '/upkeep-badstamp-d11';
        mkdir($project, 0o755, true);
        file_put_contents($project . '/.upkeep-env.yml', implode("\n", [
            'module: badstamp',
            "core_major: '11'",
            "last_used_at: 'not a timestamp'",
        ]));

        $item = self::byPathSuffix($this->scan(), '/upkeep-badstamp-d11');

        self::assertSame('badstamp', $item->module);
        self::assertNull($item->lastUsedAt);
    }

    public function testLastUsedAtIsPreferredOverCreatedAtAndCreatedAtRemainsTheFallback(): void
    {
        // Task 7 behaviour change: .upkeep-env.yml now carries last_used_at,
        // stamped at provision and re-stamped on reuse. An environment written
        // before that (created_at only) must still report an age rather than
        // reading as unknown and never expiring.
        $legacy = $this->projectsRoot . '/upkeep-legacy-d11';
        mkdir($legacy, 0o755, true);
        file_put_contents($legacy . '/.upkeep-env.yml', implode("\n", [
            'module: legacy',
            "core_major: '11'",
            "created_at: '2026-01-02T03:04:05+00:00'",
        ]));

        $items = $this->scan();
        $reused = self::byPathSuffix($items, '/upkeep-conditions-helper-d11');

        self::assertSame(
            '2026-06-01T00:00:00+00:00',
            $reused->lastUsedAt?->format(\DateTimeInterface::ATOM),
            'last_used_at must win over the older created_at.',
        );
        self::assertSame(
            '2026-01-02T03:04:05+00:00',
            self::byPathSuffix($items, '/upkeep-legacy-d11')->lastUsedAt?->format(\DateTimeInterface::ATOM),
        );
    }

    public function testACleanScanReportsNoWarnings(): void
    {
        $scanner = new InventoryScanner(new Cockpit($this->cockpitRoot), $this->projectsRoot);
        $scanner->scan();

        self::assertSame([], $scanner->warnings());
    }

    public function testAnUnreadableBaseArtifactsDirectoryDegradesToAWarningRatherThanFailingThePruneScan(): void
    {
        chmod($this->cockpitRoot . '/base-artifacts', 0o000);

        try {
            $scanner = new InventoryScanner(new Cockpit($this->cockpitRoot), $this->projectsRoot);
            $items = $scanner->scan();

            self::assertNotSame([], $items, 'The rest of the inventory must still be reported.');
            self::assertStringContainsString('/base-artifacts', implode("\n", $scanner->warnings()));
        } finally {
            chmod($this->cockpitRoot . '/base-artifacts', 0o755);
        }
    }
}

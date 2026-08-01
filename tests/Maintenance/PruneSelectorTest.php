<?php

declare(strict_types=1);

namespace Upkeep\Tests\Maintenance;

use PHPUnit\Framework\TestCase;
use Upkeep\Maintenance\Category;
use Upkeep\Maintenance\InventoryItem;
use Upkeep\Maintenance\PruneScope;
use Upkeep\Maintenance\PruneSelector;

final class PruneSelectorTest extends TestCase
{
    private const COCKPIT = '/cockpit';
    private const PROJECTS = '/projects';

    private \DateTimeImmutable $now;

    protected function setUp(): void
    {
        $this->now = new \DateTimeImmutable('2026-07-30T12:00:00Z');
    }

    private function selector(): PruneSelector
    {
        return new PruneSelector([
            self::COCKPIT . '/base-artifacts',
            self::COCKPIT . '/fixtures',
        ]);
    }

    private function tree(string $name, ?string $age = '60 days', bool $keep = false, string $root = self::PROJECTS): InventoryItem
    {
        return new InventoryItem(
            path: $root . '/' . $name,
            category: Category::ProjectTree,
            sizeBytes: 1000,
            module: 'conditions_helper',
            coreMajor: '11',
            projectName: $name,
            lastUsedAt: $age === null ? null : $this->now->modify('-' . $age),
            keepMarked: $keep,
        );
    }

    private function snapshot(string $project, string $name, string $age, bool $keep = false): InventoryItem
    {
        return new InventoryItem(
            path: self::PROJECTS . "/$project/.ddev/upkeep/materialized/$name.sql",
            category: Category::Snapshot,
            sizeBytes: 500,
            module: 'conditions_helper',
            coreMajor: '11',
            projectName: $project,
            lastUsedAt: $this->now->modify('-' . $age),
            keepMarked: $keep,
        );
    }

    // --- Protection guarantees -------------------------------------------

    public function testBaseArtifactsAreNeverCandidatesInAnyScope(): void
    {
        $baseArtifact = new InventoryItem(
            path: self::COCKPIT . '/base-artifacts/11',
            category: Category::BaseArtifact,
            sizeBytes: 999999,
            coreMajor: '11',
            lastUsedAt: $this->now->modify('-400 days'),
        );

        foreach (PruneScope::cases() as $scope) {
            $candidates = $this->selector()->select([$baseArtifact], $scope, null, $this->now);
            self::assertSame([], $candidates, 'base artifact leaked into scope ' . $scope->value);
        }
    }

    public function testFixtureDumpsAreNeverCandidatesInAnyScope(): void
    {
        $moduleDump = new InventoryItem(
            path: self::PROJECTS . '/upkeep-conditions-helper-d11/module/tests/fixtures/base.sql.gz',
            category: Category::FixtureDump,
            sizeBytes: 100,
            module: 'conditions_helper',
            lastUsedAt: $this->now->modify('-400 days'),
        );
        $libraryDump = new InventoryItem(
            path: self::COCKPIT . '/fixtures/shared.sql.gz',
            category: Category::FixtureDump,
            sizeBytes: 100,
            lastUsedAt: $this->now->modify('-400 days'),
        );

        foreach (PruneScope::cases() as $scope) {
            self::assertSame([], $this->selector()->select([$moduleDump, $libraryDump], $scope, null, $this->now));
        }
    }

    public function testKeepMarkedItemsAreNeverCandidates(): void
    {
        $keptTree = $this->tree('upkeep-conditions-helper-d11', keep: true);
        $keptSnapshot = $this->snapshot('upkeep-conditions-helper-d11', 'base', '400 days', keep: true);

        foreach (PruneScope::cases() as $scope) {
            self::assertSame([], $this->selector()->select([$keptTree, $keptSnapshot], $scope, null, $this->now));
        }
    }

    public function testMislabeledItemUnderProtectedRootIsStillExcluded(): void
    {
        // Even if the scanner ever mis-categorized something living inside
        // base-artifacts/ or the fixture library as a disposable tree or
        // snapshot, the path guard must still refuse it.
        $mislabeledTree = $this->tree('11/tree', root: self::COCKPIT . '/base-artifacts');
        $mislabeledSnapshot = new InventoryItem(
            path: self::COCKPIT . '/fixtures/evil.sql',
            category: Category::Snapshot,
            sizeBytes: 5,
            lastUsedAt: $this->now->modify('-400 days'),
        );

        self::assertSame([], $this->selector()->select([$mislabeledTree, $mislabeledSnapshot], PruneScope::All, null, $this->now));
    }

    public function testPathContainingTestsFixturesSegmentIsExcludedRegardlessOfCategory(): void
    {
        $mislabeled = new InventoryItem(
            path: self::PROJECTS . '/upkeep-conditions-helper-d11/module/tests/fixtures/base.sql.gz',
            category: Category::Snapshot,
            sizeBytes: 5,
            lastUsedAt: $this->now->modify('-400 days'),
        );

        self::assertSame([], $this->selector()->select([$mislabeled], PruneScope::All, null, $this->now));
    }

    public function testProtectedRootPrefixMatchIsPerPathSegmentNotPerCharacter(): void
    {
        // /cockpit/base-artifacts-old is NOT under /cockpit/base-artifacts.
        $tree = $this->tree('x', root: self::COCKPIT . '/base-artifacts-old');

        $candidates = $this->selector()->select([$tree], PruneScope::Trees, null, $this->now);

        self::assertCount(1, $candidates);
    }

    public function testTreeContainingKeepMarkedSnapshotIsProtectedFromTreeAndProjectPruning(): void
    {
        // Pruning the tree would destroy the kept snapshot with it: the keep
        // mark escalates to the containing environment (tree and volumes).
        $tree = $this->tree('upkeep-conditions-helper-d11');
        $volume = new InventoryItem(
            path: 'upkeep-conditions-helper-d11-mariadb',
            category: Category::ProjectVolume,
            sizeBytes: 200,
            projectName: 'upkeep-conditions-helper-d11',
            lastUsedAt: $this->now->modify('-60 days'),
        );
        $keptSnapshot = $this->snapshot('upkeep-conditions-helper-d11', 'base', '10 days', keep: true);
        $otherTree = $this->tree('upkeep-other-d11');

        foreach ([PruneScope::Trees, PruneScope::Projects, PruneScope::All] as $scope) {
            $candidates = $this->selector()->select([$tree, $volume, $keptSnapshot, $otherTree], $scope, null, $this->now);
            self::assertSame([$otherTree], $candidates, 'kept-snapshot escalation failed for scope ' . $scope->value);
        }
    }

    public function testCommittedDumpsDoNotEscalateToTheirTree(): void
    {
        // Unlike keep marks, committed dumps are durable upstream (the module
        // clone is regenerable), so they never protect the containing tree.
        $tree = $this->tree('upkeep-conditions-helper-d11');
        $dump = new InventoryItem(
            path: self::PROJECTS . '/upkeep-conditions-helper-d11/module/tests/fixtures/base.sql.gz',
            category: Category::FixtureDump,
            sizeBytes: 100,
            module: 'conditions_helper',
            projectName: 'upkeep-conditions-helper-d11',
        );

        self::assertSame([$tree], $this->selector()->select([$tree, $dump], PruneScope::Trees, null, $this->now));
    }

    public function testNoScopeAgeOrKeepLatestCombinationEverSelectsAProtectedItem(): void
    {
        // Property-style sweep: every protection rule × every scope × the
        // age-filter and keep-latest flag values. Whatever flags are passed,
        // a protected item must never become a candidate — and (non-vacuity)
        // only the two disposable decoys ever may.
        $protected = [
            'base artifact' => new InventoryItem(
                path: self::COCKPIT . '/base-artifacts/11',
                category: Category::BaseArtifact,
                sizeBytes: 1,
                coreMajor: '11',
                lastUsedAt: $this->now->modify('-400 days'),
            ),
            'committed module dump' => new InventoryItem(
                path: self::PROJECTS . '/upkeep-conditions-helper-d11/module/tests/fixtures/base.sql.gz',
                category: Category::FixtureDump,
                sizeBytes: 1,
                module: 'conditions_helper',
                lastUsedAt: $this->now->modify('-400 days'),
            ),
            'library fixture dump' => new InventoryItem(
                path: self::COCKPIT . '/fixtures/shared.sql.gz',
                category: Category::FixtureDump,
                sizeBytes: 1,
                lastUsedAt: $this->now->modify('-400 days'),
            ),
            'keep-marked tree' => $this->tree('upkeep-kept-tree-d11', age: '400 days', keep: true),
            'keep-marked snapshot' => $this->snapshot('upkeep-kept-snap-d11', 'kept', '400 days', keep: true),
            'mislabeled tree under protected root' => $this->tree('11/tree', age: '400 days', root: self::COCKPIT . '/base-artifacts'),
            'mislabeled snapshot under protected root' => new InventoryItem(
                path: self::COCKPIT . '/fixtures/evil.sql',
                category: Category::Snapshot,
                sizeBytes: 1,
                lastUsedAt: $this->now->modify('-400 days'),
            ),
            'tree escalated by its kept snapshot' => $this->tree('upkeep-kept-snap-d11', age: '400 days'),
            'volume escalated by its kept snapshot' => new InventoryItem(
                path: 'upkeep-kept-snap-d11-mariadb',
                category: Category::ProjectVolume,
                sizeBytes: 1,
                projectName: 'upkeep-kept-snap-d11',
                lastUsedAt: $this->now->modify('-400 days'),
            ),
        ];
        $decoyTree = $this->tree('upkeep-decoy-d11', age: '400 days');
        $decoySnapshot = $this->snapshot('upkeep-decoy-d11', 'old', '400 days');
        $items = [...array_values($protected), $decoyTree, $decoySnapshot];

        foreach (PruneScope::cases() as $scope) {
            foreach ([null, 0, 30 * 86400] as $olderThan) {
                foreach ([0, 1, 5] as $keepLatest) {
                    $combination = sprintf(
                        'scope=%s olderThan=%s keepLatest=%d',
                        $scope->value,
                        $olderThan === null ? 'null' : (string) $olderThan,
                        $keepLatest,
                    );
                    $candidates = $this->selector()->select($items, $scope, $olderThan, $this->now, keepLatest: $keepLatest);
                    foreach ($candidates as $candidate) {
                        self::assertContains(
                            $candidate,
                            [$decoyTree, $decoySnapshot],
                            'a protected item leaked into the candidates for ' . $combination,
                        );
                    }
                }
            }
        }

        // Non-vacuity: with no restricting flags the sweep DID select both
        // disposable decoys — the loop above is not passing on empty output.
        self::assertEqualsCanonicalizing(
            [$decoyTree, $decoySnapshot],
            $this->selector()->select($items, PruneScope::All, null, $this->now),
        );
    }

    // --- Scope filtering --------------------------------------------------

    public function testTreesScopeSelectsOnlyProjectTrees(): void
    {
        $tree = $this->tree('upkeep-conditions-helper-d11');
        $snapshot = $this->snapshot('upkeep-conditions-helper-d11', 'base', '60 days');
        $volume = new InventoryItem(
            path: 'upkeep-conditions-helper-d11-mariadb',
            category: Category::ProjectVolume,
            sizeBytes: 200,
            projectName: 'upkeep-conditions-helper-d11',
            lastUsedAt: $this->now->modify('-60 days'),
        );

        $candidates = $this->selector()->select([$tree, $snapshot, $volume], PruneScope::Trees, null, $this->now);

        self::assertSame([$tree], $candidates);
    }

    public function testProjectsScopeAlsoItemizesProjectVolumes(): void
    {
        $tree = $this->tree('upkeep-conditions-helper-d11');
        $volume = new InventoryItem(
            path: 'upkeep-conditions-helper-d11-mariadb',
            category: Category::ProjectVolume,
            sizeBytes: 200,
            projectName: 'upkeep-conditions-helper-d11',
            lastUsedAt: $this->now->modify('-60 days'),
        );
        $snapshot = $this->snapshot('upkeep-conditions-helper-d11', 'base', '60 days');

        $candidates = $this->selector()->select([$tree, $volume, $snapshot], PruneScope::Projects, null, $this->now);

        self::assertSame([$tree, $volume], $candidates);
    }

    public function testAllScopeSelectsTreesVolumesAndSnapshots(): void
    {
        $tree = $this->tree('upkeep-conditions-helper-d11');
        $snapshot = $this->snapshot('upkeep-conditions-helper-d11', 'base', '60 days');

        $candidates = $this->selector()->select([$snapshot, $tree], PruneScope::All, null, $this->now);

        self::assertSame([$tree, $snapshot], $candidates);
    }

    // --- Age filtering ----------------------------------------------------

    public function testOlderThanFilterExcludesYoungerItems(): void
    {
        $old = $this->tree('upkeep-old-d11', age: '31 days');
        $young = $this->tree('upkeep-young-d11', age: '29 days');

        $candidates = $this->selector()->select([$old, $young], PruneScope::Trees, 30 * 86400, $this->now);

        self::assertSame([$old], $candidates);
    }

    public function testUnknownAgeItemsAreExcludedWhenAgeFilterGiven(): void
    {
        // A tree without a parseable meta dotfile has no reliable age; with
        // an --older-than filter present we refuse to guess.
        $unknownAge = $this->tree('upkeep-partial-d11', age: null);

        self::assertSame([], $this->selector()->select([$unknownAge], PruneScope::Trees, 30 * 86400, $this->now));
    }

    public function testUnknownAgeItemsAreCandidatesWithoutAgeFilter(): void
    {
        $unknownAge = $this->tree('upkeep-partial-d11', age: null);

        self::assertSame([$unknownAge], $this->selector()->select([$unknownAge], PruneScope::Trees, null, $this->now));
    }

    // --- Snapshot keep-latest-N ------------------------------------------

    public function testKeepLatestRetainsNewestSnapshotsPerProject(): void
    {
        $p1new = $this->snapshot('upkeep-a-d11', 'new', '1 days');
        $p1mid = $this->snapshot('upkeep-a-d11', 'mid', '10 days');
        $p1old = $this->snapshot('upkeep-a-d11', 'old', '20 days');
        $p2new = $this->snapshot('upkeep-b-d11', 'new', '2 days');
        $p2old = $this->snapshot('upkeep-b-d11', 'old', '30 days');

        $candidates = $this->selector()->select(
            [$p1old, $p1new, $p1mid, $p2old, $p2new],
            PruneScope::Snapshots,
            null,
            $this->now,
            keepLatest: 1,
        );

        self::assertEqualsCanonicalizing([$p1mid, $p1old, $p2old], $candidates);
    }

    public function testKeepMarkedSnapshotsDoNotConsumeTheKeepLatestBudget(): void
    {
        $kept = $this->snapshot('upkeep-a-d11', 'kept', '1 days', keep: true);
        $newest = $this->snapshot('upkeep-a-d11', 'newest', '2 days');
        $older = $this->snapshot('upkeep-a-d11', 'older', '3 days');

        $candidates = $this->selector()->select([$kept, $newest, $older], PruneScope::Snapshots, null, $this->now, keepLatest: 1);

        // keep-marked is protected outright; the newest unprotected snapshot
        // fills the keep-latest budget; only the older one is a candidate.
        self::assertSame([$older], $candidates);
    }

    public function testKeepLatestZeroKeepsNothingExtra(): void
    {
        $a = $this->snapshot('upkeep-a-d11', 'a', '1 days');
        $b = $this->snapshot('upkeep-a-d11', 'b', '2 days');

        $candidates = $this->selector()->select([$a, $b], PruneScope::Snapshots, null, $this->now, keepLatest: 0);

        self::assertEqualsCanonicalizing([$a, $b], $candidates);
    }

    public function testKeepLatestCombinesWithAgeFilter(): void
    {
        $new = $this->snapshot('upkeep-a-d11', 'new', '1 days');
        $mid = $this->snapshot('upkeep-a-d11', 'mid', '40 days');
        $old = $this->snapshot('upkeep-a-d11', 'old', '50 days');

        // keep-latest keeps "new"; age filter then drops nothing older-than-30d? No:
        // both mid and old are older than 30d and outside the keep budget.
        $candidates = $this->selector()->select([$new, $mid, $old], PruneScope::Snapshots, 30 * 86400, $this->now, keepLatest: 1);

        self::assertEqualsCanonicalizing([$mid, $old], $candidates);
    }
}

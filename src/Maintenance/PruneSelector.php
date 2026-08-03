<?php

declare(strict_types=1);

namespace Upkeep\Maintenance;

/**
 * Pure candidate selection over an inventory snapshot. This is the safety
 * boundary of the prune surface: whatever flags are passed, an item is only
 * ever a candidate when NONE of the protection rules apply.
 *
 * Protection rules (each independently sufficient to exclude):
 *   - category is BaseArtifact or FixtureDump (canonical by definition);
 *   - the item is keep-marked (a `.keep` marker exists next to it);
 *   - its path lies under a protected root (the cockpit's base-artifacts/
 *     and fixtures/ directories) — guards against scanner mislabeling;
 *   - its path contains a `tests/fixtures/` segment (committed module dumps);
 *   - it is the tree (or a volume) of an environment holding a keep-marked
 *     snapshot: pruning the tree would destroy the kept snapshot with it, so
 *     the keep mark escalates to the whole environment. Committed dumps do
 *     NOT escalate — the module clone is regenerable from upstream.
 */
final readonly class PruneSelector
{
    /**
     * @param list<string> $protectedRoots absolute directory prefixes nothing may ever be selected from
     */
    public function __construct(private array $protectedRoots)
    {
    }

    /**
     * @param list<InventoryItem> $items
     * @param int|null $olderThanSeconds only items at least this old qualify; items of
     *                                   unknown age are excluded when a filter is given
     * @param int $keepLatest snapshots only: per project, this many newest
     *                        unprotected snapshots are retained
     *
     * @return list<InventoryItem> deletion candidates, grouped by the scope's category order
     */
    public function select(
        array $items,
        PruneScope $scope,
        ?int $olderThanSeconds,
        \DateTimeImmutable $now,
        int $keepLatest = 0,
    ): array {
        $keptProjects = self::projectsWithKeepMarkedSnapshots($items);

        $inScope = array_values(array_filter(
            $items,
            fn (InventoryItem $item): bool => \in_array($item->category, $scope->categories(), true)
                && $this->protectionReason($item) === null
                && !self::keepMarkEscalates($item, $keptProjects),
        ));

        // keep-latest budgets are computed over ALL unprotected snapshots
        // (before the age filter): the newest snapshots fill the budget even
        // when they are too young to be deletion candidates anyway.
        $inScope = self::applyKeepLatest($inScope, $keepLatest);

        $inScope = array_values(array_filter(
            $inScope,
            static fn (InventoryItem $item): bool => self::oldEnough($item, $olderThanSeconds, $now),
        ));

        // Stable-order by the scope's category order (trees before volumes
        // before snapshots) so the rendered candidate list reads grouped.
        $categoryOrder = array_flip(array_map(static fn (Category $c) => $c->value, $scope->categories()));
        usort($inScope, static fn (InventoryItem $a, InventoryItem $b): int =>
            $categoryOrder[$a->category->value] <=> $categoryOrder[$b->category->value]);

        return $inScope;
    }

    /**
     * Why an item may never be deleted, or null when it is disposable.
     * Public so the executor can re-assert it immediately before deletion.
     */
    public function protectionReason(InventoryItem $item): ?string
    {
        if ($item->category === Category::BaseArtifact) {
            return 'canonical base artifact';
        }
        if ($item->category === Category::FixtureDump) {
            return 'committed fixture dump';
        }
        if ($item->keepMarked) {
            return 'keep-marked (.keep)';
        }
        foreach ($this->protectedRoots as $root) {
            $prefix = rtrim($root, '/');
            if ($item->path === $prefix || str_starts_with($item->path, $prefix . '/')) {
                return sprintf('under protected root %s', $prefix);
            }
        }
        if (str_contains($item->path, '/tests/fixtures/')) {
            return 'inside a committed tests/fixtures/ directory';
        }

        return null;
    }

    /**
     * @param list<InventoryItem> $items
     *
     * @return array<string, true> project names whose snapshot store holds a keep-marked snapshot
     */
    private static function projectsWithKeepMarkedSnapshots(array $items): array
    {
        $projects = [];
        foreach ($items as $item) {
            if ($item->category === Category::Snapshot && $item->keepMarked && $item->projectName !== null) {
                $projects[$item->projectName] = true;
            }
        }

        return $projects;
    }

    /**
     * @param array<string, true> $keptProjects
     */
    private static function keepMarkEscalates(InventoryItem $item, array $keptProjects): bool
    {
        return ($item->category === Category::ProjectTree || $item->category === Category::ProjectVolume)
            && $item->projectName !== null
            && isset($keptProjects[$item->projectName]);
    }

    private static function oldEnough(InventoryItem $item, ?int $olderThanSeconds, \DateTimeImmutable $now): bool
    {
        if ($olderThanSeconds === null) {
            return true;
        }

        $age = $item->ageSeconds($now);

        return $age !== null && $age >= $olderThanSeconds;
    }

    /**
     * Retains, per project, the N newest unprotected snapshots.
     *
     * @param list<InventoryItem> $candidates
     *
     * @return list<InventoryItem>
     */
    private static function applyKeepLatest(array $candidates, int $keepLatest): array
    {
        if ($keepLatest <= 0) {
            return $candidates;
        }

        /** @var array<string, list<InventoryItem>> $snapshotsByProject */
        $snapshotsByProject = [];
        foreach ($candidates as $item) {
            if ($item->category === Category::Snapshot) {
                $snapshotsByProject[$item->projectName ?? ''][] = $item;
            }
        }

        $retained = new \SplObjectStorage();
        foreach ($snapshotsByProject as $snapshots) {
            usort($snapshots, static fn (InventoryItem $a, InventoryItem $b): int =>
                ($b->lastUsedAt?->getTimestamp() ?? PHP_INT_MIN) <=> ($a->lastUsedAt?->getTimestamp() ?? PHP_INT_MIN));
            foreach (\array_slice($snapshots, 0, $keepLatest) as $keep) {
                $retained->attach($keep);
            }
        }

        return array_values(array_filter(
            $candidates,
            static fn (InventoryItem $item): bool =>
                !($item->category === Category::Snapshot && $retained->contains($item)),
        ));
    }
}

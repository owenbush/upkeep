<?php

declare(strict_types=1);

namespace Upkeep\Maintenance;

use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\ProjectName;
use Upkeep\Adapter\SnapshotLayout;
use Upkeep\Cockpit\Module;

/**
 * Executes a confirmed prune over selector-approved candidates.
 *
 * Environment disposal ALWAYS goes through the adapter's teardown — never a
 * raw rm -rf, which would leave the engine project's containers running and
 * its named volumes allocated. Volume candidates are reclaimed by their
 * project's teardown, never touched directly. Snapshot candidates are plain
 * file removals (artifact + its .meta).
 *
 * Defense in depth: every item is re-checked against the selector's
 * protection rules immediately before deletion; a protected item in the
 * candidate list aborts the run.
 */
final readonly class PruneExecutor
{
    /**
     * @param array<string, Module> $registryModules keyed by machine name
     * @param \Closure(string): void $log
     */
    public function __construct(
        private PruneSelector $selector,
        private EngineAdapterInterface $adapter,
        private array $registryModules,
        private \Closure $log,
    ) {
    }

    /**
     * @param list<InventoryItem> $candidates
     */
    public function execute(array $candidates): PruneOutcome
    {
        foreach ($candidates as $item) {
            $reason = $this->selector->protectionReason($item);
            if ($reason !== null) {
                throw new \RuntimeException(sprintf(
                    'Refusing to prune protected item "%s" (%s) — candidate selection was bypassed; aborting '
                        . 'without deleting anything.',
                    $item->path,
                    $reason,
                ));
            }
        }

        $freed = 0;
        $deleted = [];
        $skipped = [];

        $tornDownProjects = [];
        foreach ($candidates as $item) {
            if ($item->category !== Category::ProjectTree) {
                continue;
            }
            $resolved = $this->resolveEnvironment($item);
            if ($resolved === null) {
                $skipped[] = [
                    $item,
                    'cannot attribute this tree to a registered (module x core) pair — refusing to guess; '
                        . 'tear it down manually',
                ];
                continue;
            }
            [$module, $coreMajor] = $resolved;
            ($this->log)(sprintf(
                'Tearing down environment %s (module %s, Drupal %s) via the adapter ...',
                $item->projectName ?? $item->path,
                $module->name,
                $coreMajor,
            ));
            try {
                $this->adapter->teardown($module, $coreMajor);
            } catch (AdapterException $e) {
                $skipped[] = [$item, $e->getMessage()];
                continue;
            }
            $tornDownProjects[$item->projectName ?? ''] = true;
            $freed += $item->sizeBytes;
            $deleted[] = $item;
        }

        foreach ($candidates as $item) {
            if ($item->category !== Category::ProjectVolume) {
                continue;
            }
            if (isset($tornDownProjects[$item->projectName ?? ''])) {
                // Freed by the teardown above; only account for it here.
                $freed += $item->sizeBytes;
                $deleted[] = $item;
            } else {
                $skipped[] = [
                    $item,
                    'volumes are reclaimed via their project\'s teardown; its tree was not pruned in this run',
                ];
            }
        }

        foreach ($candidates as $item) {
            if ($item->category !== Category::Snapshot) {
                continue;
            }
            $freed += $item->sizeBytes;
            $deleted[] = $item;
            ($this->log)(sprintf('Removing materialized snapshot %s ...', $item->path));
            @unlink($item->path);
            $metaPath = SnapshotLayout::metaPathForArtifact($item->path);
            if ($metaPath !== null && is_file($metaPath)) {
                @unlink($metaPath);
            }
        }

        return new PruneOutcome($freed, $deleted, $skipped);
    }

    /**
     * Maps a tree candidate back to the (Module, core major) identity the
     * adapter's teardown needs: the meta dotfile attribution when present,
     * otherwise a registry name-map lookup (registered module x core pairs
     * whose ProjectName matches the directory name). Unresolvable trees are
     * never guessed at.
     *
     * @return array{Module, string}|null
     */
    private function resolveEnvironment(InventoryItem $item): ?array
    {
        if ($item->module !== null && $item->coreMajor !== null) {
            $module = $this->registryModules[$item->module]
                ?? new Module($item->module, 'project/' . $item->module, [$item->coreMajor]);

            return [$module, $item->coreMajor];
        }

        if ($item->projectName !== null) {
            foreach ($this->registryModules as $module) {
                foreach ($module->coreVersions as $coreMajor) {
                    if (ProjectName::for($module->name, $coreMajor) === $item->projectName) {
                        return [$module, $coreMajor];
                    }
                }
            }
        }

        return null;
    }
}

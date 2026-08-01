<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The engine add-on's per-project snapshot layout (owned by the ddev-upkeep
 * add-on, mirrored here as the one place outside the add-on that may know
 * it): materialized fixture snapshots live at
 * `<project>/.ddev/upkeep/materialized/<name>.sql` with a sidecar meta file
 * at `<project>/.ddev/upkeep/snapshots/<name>.meta` recording, among other
 * facts, `materialized_at=<iso8601>`.
 */
final readonly class SnapshotLayout
{
    public const MATERIALIZED_DIR = '.ddev/upkeep/materialized';
    public const META_DIR = '.ddev/upkeep/snapshots';

    /**
     * materialized/<name>.sql -> <project>/.ddev/upkeep/snapshots/<name>.meta,
     * or null when the path is not a materialized snapshot artifact.
     */
    public static function metaPathForArtifact(string $artifactPath): ?string
    {
        $materializedDir = \dirname($artifactPath);
        if (basename($materializedDir) !== basename(self::MATERIALIZED_DIR)) {
            return null;
        }

        return \dirname($materializedDir) . '/' . basename(self::META_DIR) . '/' . basename($artifactPath, '.sql') . '.meta';
    }
}

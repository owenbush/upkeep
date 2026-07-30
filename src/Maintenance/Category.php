<?php

declare(strict_types=1);

namespace Upkeep\Maintenance;

/**
 * The disk-inventory categories `status --disk` reports and `prune` reasons
 * about. Two of them are canonical by definition and can never be pruned:
 * base artifacts and committed fixture dumps.
 */
enum Category: string
{
    /** A per-core-version base artifact directory (<cockpit>/base-artifacts/<major>). Always canonical. */
    case BaseArtifact = 'base-artifact';

    /** A provisioned environment tree under the projects root (upkeep-<module>-d<major>). Disposable. */
    case ProjectTree = 'project-tree';

    /** A materialized fixture snapshot (SnapshotLayout::MATERIALIZED_DIR/<name>.sql + its .meta). Disposable cache. */
    case Snapshot = 'snapshot';

    /** A committed .sql.gz dump (module tests/fixtures/ or the cockpit fixture library). Always canonical. */
    case FixtureDump = 'fixture-dump';

    /** A docker named volume belonging to an engine project. Reclaimed only via adapter teardown. */
    case ProjectVolume = 'project-volume';

    public function label(): string
    {
        return match ($this) {
            self::BaseArtifact => 'base artifact',
            self::ProjectTree => 'project tree',
            self::Snapshot => 'materialized snapshot',
            self::FixtureDump => 'fixture dump',
            self::ProjectVolume => 'project volume',
        };
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Maintenance;

/**
 * What a `prune` invocation targets. Trees and Projects both dispose of
 * environments through the adapter's teardown (a tree can never be deleted
 * with a bare rm -rf: that leaves its containers running); they differ in
 * what the candidate list itemizes — Projects additionally itemizes the
 * engine projects' named volumes so the reclaim total includes them.
 */
enum PruneScope: string
{
    case Trees = 'trees';
    case Snapshots = 'snapshots';
    case Projects = 'projects';
    case All = 'all';

    /**
     * @return list<Category>
     */
    public function categories(): array
    {
        return match ($this) {
            self::Trees => [Category::ProjectTree],
            self::Snapshots => [Category::Snapshot],
            self::Projects => [Category::ProjectTree, Category::ProjectVolume],
            self::All => [Category::ProjectTree, Category::ProjectVolume, Category::Snapshot],
        };
    }
}

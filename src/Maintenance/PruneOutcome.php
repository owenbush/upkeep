<?php

declare(strict_types=1);

namespace Upkeep\Maintenance;

/**
 * What a confirmed prune actually did: bytes freed, items deleted, and items
 * skipped with the reason (an unresolvable environment is never guessed at).
 */
final readonly class PruneOutcome
{
    /**
     * @param list<InventoryItem> $deleted
     * @param list<array{InventoryItem, string}> $skipped item + reason
     */
    public function __construct(
        public int $freedBytes,
        public array $deleted,
        public array $skipped,
    ) {
    }
}

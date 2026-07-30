<?php

declare(strict_types=1);

namespace Upkeep\Maintenance;

/**
 * One entry in the disk inventory snapshot: a path (or, for volumes, a docker
 * volume name) with its category, attribution (module, core major, project),
 * measured size, and the facts the prune selector needs (age, keep marker).
 */
final readonly class InventoryItem
{
    public function __construct(
        public string $path,
        public Category $category,
        public int $sizeBytes,
        public ?string $module = null,
        public ?string $coreMajor = null,
        public ?string $projectName = null,
        public ?\DateTimeImmutable $lastUsedAt = null,
        public bool $keepMarked = false,
    ) {
    }

    public function ageSeconds(\DateTimeImmutable $now): ?int
    {
        return $this->lastUsedAt === null ? null : max(0, $now->getTimestamp() - $this->lastUsedAt->getTimestamp());
    }
}

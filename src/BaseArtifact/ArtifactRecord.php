<?php

declare(strict_types=1);

namespace Upkeep\BaseArtifact;

/**
 * The observed state of one per-core-version artifact set on disk.
 */
final readonly class ArtifactRecord
{
    /**
     * @param list<string> $missing pieces absent or unreadable; empty when complete
     */
    public function __construct(
        public string $version,
        public bool $complete,
        public ?ArtifactMeta $meta,
        public int $treeSizeBytes,
        public int $dumpSizeBytes,
        public array $missing,
    ) {
    }
}

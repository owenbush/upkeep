<?php

declare(strict_types=1);

namespace Upkeep\Cockpit;

/**
 * A single registered module: what it is called, where it lives, which cores it tracks.
 */
final readonly class Module
{
    /**
     * @param list<string> $coreVersions
     */
    public function __construct(
        public string $name,
        public string $project,
        public array $coreVersions,
    ) {
    }
}

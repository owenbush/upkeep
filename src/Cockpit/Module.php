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
        /**
         * Whether this came from the registry rather than being derived.
         *
         * Provenance, because it changes what a refusal can honestly say. A
         * watched module's `core_versions` is a line in registry.yml a
         * maintainer wrote; a derived module's is the list of base artifacts
         * on this machine. Telling somebody to "add it to core_versions in
         * registry.yml" for a module that has no entry there sends them to
         * edit a file that does not mention it.
         *
         * Defaults to true so the registry loader and every existing caller
         * are unchanged; only Cockpit\ModuleResolution passes false.
         */
        public bool $watched = true,
    ) {
    }
}

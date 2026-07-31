<?php

declare(strict_types=1);

namespace Upkeep\Cockpit;

use Symfony\Component\Yaml\Yaml;

/**
 * Appends modules to an existing registry file.
 *
 * Writes go through parse → merge → validate → dump: the merged registry is
 * re-validated with the same rules ModuleRegistry::fromFile enforces before
 * anything touches disk, so a bad entry can never corrupt the file. Dumping
 * regenerates the YAML, so hand-written comments in the file do not survive
 * an add — the scaffold's commented example is consumed the first time this
 * editor writes.
 */
final readonly class RegistryEditor
{
    public function __construct(private string $registryPath)
    {
    }

    /**
     * Adds the given modules, skipping any machine name already registered
     * (existing definitions always win). Returns the names actually added.
     *
     * @param list<Module> $modules
     * @return list<string>
     * @throws RegistryException when the existing file or a new entry is invalid
     */
    public function add(array $modules): array
    {
        $existing = ModuleRegistry::fromFile($this->registryPath)->modules();

        $entries = [];
        foreach ($existing as $name => $module) {
            $entries[$name] = ['project' => $module->project, 'core_versions' => $module->coreVersions];
        }

        $added = [];
        foreach ($modules as $module) {
            if (isset($entries[$module->name])) {
                continue;
            }
            $entries[$module->name] = ['project' => $module->project, 'core_versions' => $module->coreVersions];
            $added[] = $module->name;
        }

        if ($added === []) {
            return [];
        }

        $yaml = Yaml::dump(['modules' => $entries], 4, 2);
        // Validate the merged result through the real parser before writing.
        $probe = $this->registryPath . '.probe';
        file_put_contents($probe, $yaml);
        try {
            ModuleRegistry::fromFile($probe);
        } finally {
            @unlink($probe);
        }

        file_put_contents($this->registryPath, $yaml);

        return $added;
    }
}

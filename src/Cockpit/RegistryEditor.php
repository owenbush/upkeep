<?php

declare(strict_types=1);

namespace Upkeep\Cockpit;

use Symfony\Component\Yaml\Yaml;
use Upkeep\Filesystem\FilesystemException;
use Upkeep\Filesystem\FileWriter;

/**
 * Appends modules to an existing registry file.
 *
 * Writes go through parse → merge → validate → publish: the merged registry is
 * written to a sibling temp file, re-validated from that file with the same
 * rules ModuleRegistry::fromFile enforces, and only then rename()d over the
 * real registry. Both halves matter — validation stops a semantically bad
 * entry, and the rename stops a torn write, so neither a rejected entry nor a
 * crash mid-write can corrupt the tool's single source of truth. Dumping
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
     * @throws FilesystemException when the new registry cannot be written
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

        // The exact final bytes, written next to the registry. They are
        // validated from that file — through the real parser, so what is
        // checked is what will be published — and then renamed into place.
        $temp = FileWriter::writeTemporary($this->registryPath, $yaml, null);
        try {
            ModuleRegistry::fromFile($temp);
        } catch (RegistryException $e) {
            @unlink($temp);
            // Report the registry the user asked to change, not the temp file.
            throw new RegistryException(
                str_replace($temp, $this->registryPath, $e->getMessage()),
                $e->getCode(),
                $e,
            );
        } catch (\Throwable $e) {
            @unlink($temp);
            throw $e;
        }

        FileWriter::commit($temp, $this->registryPath);

        return $added;
    }
}

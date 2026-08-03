<?php

declare(strict_types=1);

namespace Upkeep\Cockpit;

use Symfony\Component\Yaml\Exception\ParseException;
use Symfony\Component\Yaml\Yaml;

/**
 * Loads and validates the cockpit module registry (registry.yml).
 */
final readonly class ModuleRegistry
{
    /**
     * @param array<string, Module> $modules keyed by machine name
     */
    private function __construct(private array $modules)
    {
    }

    public static function fromFile(string $path): self
    {
        if (!is_file($path)) {
            throw new RegistryException(sprintf(
                'Module registry not found at "%s". Run "upkeep init" to create a cockpit, or point --cockpit / '
                    . 'UPKEEP_COCKPIT at an existing one.',
                $path,
            ));
        }

        try {
            $raw = Yaml::parseFile($path);
        } catch (ParseException $e) {
            throw new RegistryException(
                sprintf('Module registry "%s" is not valid YAML: %s', $path, $e->getMessage()),
                0,
                $e,
            );
        }

        if (!\is_array($raw) || !\array_key_exists('modules', $raw)) {
            throw new RegistryException(sprintf(
                'Module registry "%s" must be a YAML mapping with a top-level "modules" key.',
                $path,
            ));
        }

        $entries = $raw['modules'] ?? [];
        if (!\is_array($entries)) {
            throw new RegistryException(sprintf(
                'The "modules" key in "%s" must be a mapping of machine name to module definition.',
                $path,
            ));
        }

        $modules = [];
        foreach ($entries as $name => $definition) {
            $modules[$name] = self::buildModule($path, (string) $name, $definition);
        }

        return new self($modules);
    }

    private static function buildModule(string $path, string $name, mixed $definition): Module
    {
        if (!\is_array($definition)) {
            throw new RegistryException(sprintf(
                'Module "%s" in "%s" must be a mapping with "project" and "core_versions" keys.',
                $name,
                $path,
            ));
        }

        $project = $definition['project'] ?? null;
        if (!\is_string($project) || $project === '') {
            throw new RegistryException(sprintf(
                'Module "%s" in "%s" is missing a non-empty "project" (its git.drupalcode.org project path, '
                    . 'e.g. "project/%s").',
                $name,
                $path,
                $name,
            ));
        }

        $coreVersions = $definition['core_versions'] ?? null;
        if (!\is_array($coreVersions) || $coreVersions === [] || !array_is_list($coreVersions)) {
            throw new RegistryException(sprintf(
                'Module "%s" in "%s" must declare "core_versions" as a non-empty list (e.g. ["10", "11"]).',
                $name,
                $path,
            ));
        }

        return new Module($name, $project, array_map(strval(...), $coreVersions));
    }

    /**
     * @return array<string, Module> keyed by machine name
     */
    public function modules(): array
    {
        return $this->modules;
    }
}

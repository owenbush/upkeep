<?php

declare(strict_types=1);

namespace Upkeep\Cockpit;

use Symfony\Component\Yaml\Exception\ParseException;
use Symfony\Component\Yaml\Yaml;
use Upkeep\Adapter\ProjectName;

/**
 * Loads and validates the cockpit module registry (registry.yml).
 *
 * Validation is total: every key and value that later becomes a path segment
 * or a project name is checked here, at load, so downstream consumers
 * (results cache, dashboard cache, prune) can treat registry content as
 * trusted and no command has to re-validate it.
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

        // Keyed by the *validated* name rather than by the raw YAML key: an
        // unquoted numeric key parses as an int, and buildModule() is what
        // decides whether a key is a usable machine name at all. Machine names
        // start with a letter, so the result is a genuine string-keyed map
        // that PHP will not coerce back to integer keys.
        $modules = [];
        foreach ($entries as $name => $definition) {
            $module = self::buildModule($path, (string) $name, $definition);
            $modules[$module->name] = $module;
        }

        return new self($modules);
    }

    private static function buildModule(string $path, string $name, mixed $definition): Module
    {
        // The machine name becomes a path segment (<cockpit>/results/<module>/,
        // <cockpit>/cache/dashboard/<module>.json) and an engine project name.
        // Validating it here, at load, is the single point that keeps a
        // traversal sequence out of every derived path — and keeps the
        // failure from surfacing as an uncaught InvalidArgumentException deep
        // inside prune, after environments have already been torn down.
        if (!ProjectName::isModuleName($name)) {
            throw new RegistryException(sprintf(
                'Module key "%s" in "%s" is not a Drupal machine name ([a-z][a-z0-9_]*). Registry keys are used as '
                    . 'directory names and engine project names, so they must be machine names.',
                $name,
                $path,
            ));
        }

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

        $versions = [];
        foreach ($coreVersions as $version) {
            // Core versions are path segments too (results/<module>/<mr>/<core>).
            if (!is_scalar($version) || !ProjectName::isCoreMajor((string) $version)) {
                throw new RegistryException(sprintf(
                    'Module "%s" in "%s" lists a "core_versions" entry that is not a whole major version number '
                        . '(e.g. "11"): %s',
                    $name,
                    $path,
                    is_scalar($version) ? sprintf('"%s"', (string) $version) : get_debug_type($version),
                ));
            }
            $versions[] = (string) $version;
        }

        return new Module($name, $project, $versions);
    }

    /**
     * @return array<string, Module> keyed by machine name
     */
    public function modules(): array
    {
        return $this->modules;
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Cockpit;

/**
 * The cockpit: a control-project directory holding the module registry,
 * base artifacts, and the shared fixture library.
 */
final readonly class Cockpit
{
    public const REGISTRY_FILENAME = 'registry.yml';
    public const BASE_ARTIFACTS_DIR = 'base-artifacts';
    public const FIXTURES_DIR = 'fixtures';
    public const PROJECTS_DIR = 'projects';
    public const ENV_VAR = 'UPKEEP_COCKPIT';

    public function __construct(public string $root)
    {
    }

    /**
     * Resolves the cockpit directory: explicit --cockpit flag first, then the
     * UPKEEP_COCKPIT environment variable, then the current working directory.
     */
    public static function resolve(?string $option): self
    {
        $env = getenv(self::ENV_VAR);

        return new self($option ?? ($env !== false && $env !== '' ? $env : getcwd()));
    }

    public function registryPath(): string
    {
        return $this->root . '/' . self::REGISTRY_FILENAME;
    }

    public function baseArtifactsPath(): string
    {
        return $this->root . '/' . self::BASE_ARTIFACTS_DIR;
    }

    public function fixturesPath(): string
    {
        return $this->root . '/' . self::FIXTURES_DIR;
    }

    public function projectsPath(): string
    {
        return $this->root . '/' . self::PROJECTS_DIR;
    }

    public function loadRegistry(): ModuleRegistry
    {
        return ModuleRegistry::fromFile($this->registryPath());
    }
}

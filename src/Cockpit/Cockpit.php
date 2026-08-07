<?php

declare(strict_types=1);

namespace Upkeep\Cockpit;

use Upkeep\Filesystem\FilesystemException;
use Upkeep\Filesystem\PathGuard;

/**
 * The cockpit: a control-project directory holding the module registry,
 * base artifacts, and the shared fixture library.
 *
 * The root is canonicalised on construction, so `..` sequences and symlinked
 * spellings are resolved once here instead of surviving into every derived
 * path — which also means two spellings of one cockpit compare equal, which
 * the prune surface's protected-root check depends on.
 */
final readonly class Cockpit
{
    public const REGISTRY_FILENAME = 'registry.yml';
    public const BASE_ARTIFACTS_DIR = 'base-artifacts';
    public const FIXTURES_DIR = 'fixtures';
    public const PROJECTS_DIR = 'projects';
    public const RESULTS_DIR = 'results';
    public const DASHBOARD_CACHE_DIR = 'cache/dashboard';
    public const PATCH_CACHE_DIR = 'cache/patches';
    public const ENV_VAR = 'UPKEEP_COCKPIT';

    public string $root;

    /**
     * @throws FilesystemException when the path is empty or unresolvable
     */
    public function __construct(string $root)
    {
        $this->root = rtrim(PathGuard::canonicalize($root), '/');
    }

    /**
     * Resolves the cockpit directory: explicit --cockpit flag first, then the
     * UPKEEP_COCKPIT environment variable, then the current working directory.
     *
     * @throws FilesystemException when the resulting path is empty or unresolvable
     */
    public static function resolve(?string $option): self
    {
        if ($option !== null) {
            if ($option === '') {
                throw new FilesystemException(
                    'An empty --cockpit was given. Pass the path to a cockpit directory, or omit --cockpit to use '
                    . '$' . self::ENV_VAR . ' or the current directory.',
                );
            }

            return new self($option);
        }

        $env = getenv(self::ENV_VAR);
        if ($env !== false && $env !== '') {
            return new self($env);
        }

        $cwd = getcwd();
        if ($cwd === false) {
            throw new FilesystemException(
                'Cannot use the current directory as the cockpit: it is unavailable (it may have been deleted). '
                . 'Re-run from an existing directory, or pass --cockpit / $' . self::ENV_VAR . '.',
            );
        }

        return new self($cwd);
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

    /** Where `check` persists per-run results and the dashboard/gate read them. */
    public function resultsPath(): string
    {
        return $this->root . '/' . self::RESULTS_DIR;
    }

    /** Where the dashboard caches remote GitLab/drupal.org state per module. */
    public function dashboardCachePath(): string
    {
        return $this->root . '/' . self::DASHBOARD_CACHE_DIR;
    }

    /** Where the patch commands cache the diff files they download, by issue. */
    public function patchCachePath(): string
    {
        return $this->root . '/' . self::PATCH_CACHE_DIR;
    }

    public function loadRegistry(): ModuleRegistry
    {
        return ModuleRegistry::fromFile($this->registryPath());
    }
}

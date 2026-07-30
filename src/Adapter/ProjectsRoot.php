<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Resolves the directory that holds all engine-managed module environments.
 *
 * Resolution order: explicit configuration (cockpit config / CLI flag), the
 * UPKEEP_PROJECTS_ROOT environment variable, then `~/.upkeep/projects`.
 *
 * The default deliberately lives under $HOME and there is no temp-dir
 * fallback: environments are bind-mounted into the Docker VM, and macOS
 * providers (colima, Docker Desktop) only share the home directory by
 * default — a projects root under /tmp can never start.
 */
final readonly class ProjectsRoot
{
    public const string ENV_VAR = 'UPKEEP_PROJECTS_ROOT';

    public static function resolve(?string $explicit): string
    {
        if ($explicit !== null && $explicit !== '') {
            return $explicit;
        }

        $env = getenv(self::ENV_VAR);
        if ($env !== false && $env !== '') {
            return $env;
        }

        $home = getenv('HOME');
        if ($home === false || $home === '') {
            throw new AdapterException(sprintf(
                'Cannot resolve a projects root: $HOME is not set and neither explicit configuration nor $%s was given. Refusing to fall back to a temp dir — Docker providers only mount the home directory.',
                self::ENV_VAR,
            ));
        }

        return $home . '/.upkeep/projects';
    }
}

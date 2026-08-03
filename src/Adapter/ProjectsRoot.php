<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Resolves the directory that holds all engine-managed module environments.
 *
 * Resolution order: explicit --projects-root flag, the UPKEEP_PROJECTS_ROOT
 * environment variable, a projects/ directory inside the cockpit (if it
 * exists), then `~/.upkeep/projects`.
 *
 * Every one of those sources is then canonicalised and required to stay under
 * $HOME (see MountablePath): environments are bind-mounted into the Docker VM,
 * and macOS providers only share the home directory by default, so a projects
 * root outside it can never start. That is a functional requirement, not only
 * a hardening measure, and it applies to the explicit flag and the environment
 * variable exactly as it applies to the default.
 */
final readonly class ProjectsRoot
{
    public const ENV_VAR = 'UPKEEP_PROJECTS_ROOT';

    private const WHAT = 'projects root';

    /**
     * @return string the canonical projects root
     *
     * @throws AdapterException when the root cannot be resolved or lies outside $HOME
     */
    public static function resolve(?string $explicit, ?string $cockpitRoot = null): string
    {
        return MountablePath::requireUnderHome(
            self::candidate($explicit, $cockpitRoot),
            self::WHAT,
            self::howToSet(),
        );
    }

    private static function candidate(?string $explicit, ?string $cockpitRoot): string
    {
        if ($explicit !== null && $explicit !== '') {
            return $explicit;
        }

        $env = getenv(self::ENV_VAR);
        if ($env !== false && $env !== '') {
            return $env;
        }

        if ($cockpitRoot !== null && $cockpitRoot !== '') {
            $cockpitProjects = rtrim($cockpitRoot, '/') . '/projects';
            if (is_dir($cockpitProjects)) {
                return $cockpitProjects;
            }
        }

        return MountablePath::home(self::WHAT, self::howToSet()) . '/.upkeep/projects';
    }

    private static function howToSet(): string
    {
        return '--projects-root or $' . self::ENV_VAR;
    }
}

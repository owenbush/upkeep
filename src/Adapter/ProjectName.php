<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The one place the per-(module x core-version) engine project naming
 * convention lives: `upkeep-<module>-d<core-major>`, with the module's
 * machine-name underscores translated to hyphens because engine project names
 * become DNS labels (`<name>.ddev.site`).
 */
final readonly class ProjectName
{
    private const MODULE_PATTERN = '/^[a-z][a-z0-9_]*$/';
    private const CORE_PATTERN = '/^\d+$/';

    /**
     * Whether a string is a Drupal machine name. Public because the same rule
     * has to hold wherever a module name becomes a path segment or a project
     * name — the registry validates with it at load so the failure never
     * surfaces mid-prune.
     */
    public static function isModuleName(string $moduleName): bool
    {
        return preg_match(self::MODULE_PATTERN, $moduleName) === 1;
    }

    /** Whether a string is a whole core major version number. */
    public static function isCoreMajor(string $coreMajor): bool
    {
        return preg_match(self::CORE_PATTERN, $coreMajor) === 1;
    }

    public static function for(string $moduleName, string $coreMajor): string
    {
        if (preg_match(self::MODULE_PATTERN, $moduleName) !== 1) {
            throw new \InvalidArgumentException(sprintf(
                'Module name must be a Drupal machine name ([a-z][a-z0-9_]*), got "%s".',
                $moduleName,
            ));
        }
        if (preg_match(self::CORE_PATTERN, $coreMajor) !== 1) {
            throw new \InvalidArgumentException(sprintf(
                'Core version must be a whole major version number, got "%s".',
                $coreMajor,
            ));
        }

        return sprintf('upkeep-%s-d%s', str_replace('_', '-', $moduleName), $coreMajor);
    }
}

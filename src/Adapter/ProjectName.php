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
    private const string MODULE_PATTERN = '/^[a-z][a-z0-9_]*$/';
    private const string CORE_PATTERN = '/^\d+$/';

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

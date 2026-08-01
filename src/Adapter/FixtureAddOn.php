<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Identity of the ddev-upkeep add-on: the fixture commands (`ddev
 * upkeep-fixture-load` etc.) the adapter shells to for loadFixture().
 * Installed into every environment during provisioning, and lazily on first
 * loadFixture() for environments provisioned before the add-on became part
 * of the layout.
 */
final readonly class FixtureAddOn
{
    /** Published add-on source, as `ddev add-on get` accepts it. */
    public const NAME = 'owenbush/ddev-upkeep';

    /**
     * Env override for the add-on source — a local checkout path during
     * add-on development, e.g. UPKEEP_ADDON_SOURCE=/path/to/ddev-upkeep.
     */
    public const SOURCE_ENV = 'UPKEEP_ADDON_SOURCE';

    /**
     * A file the add-on installs into <project>/.ddev/ — its presence is the
     * "already installed" probe.
     */
    public const MARKER = 'commands/host/upkeep-fixture-load';

    public static function source(): string
    {
        $override = getenv(self::SOURCE_ENV);

        return $override !== false && $override !== '' ? $override : self::NAME;
    }
}

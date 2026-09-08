<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Identity of the ddev-upkeep add-on: the fixture commands (`ddev
 * fixture-load` etc.) the adapter shells to for loadFixture().
 * Installed into every environment during provisioning, and lazily on first
 * loadFixture() for environments provisioned before the add-on became part
 * of the layout.
 *
 * The command name is declared once, here, and both the invocation and the
 * installed-probe are built from it. They disagreed: the adapter ran
 * `ddev upkeep-fixture-load` while the add-on has always published
 * `ddev fixture-load` — its README, its bats tests and its recorded
 * end-to-end run all use the short name, and the prefix appears there only on
 * snapshot names and environment variables. So `--fixture` could not have
 * worked, and the probe looked for a file that is never installed, which
 * re-fetched the add-on on every single call. Two strings for one fact, in
 * two repositories, with nothing comparing them.
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
     * The host command the add-on publishes for loading a fixture, as it is
     * invoked and as it is named on disk.
     */
    public const LOAD_COMMAND = 'fixture-load';

    /**
     * A file the add-on installs into <project>/.ddev/ — its presence is the
     * "already installed" probe. Derived from LOAD_COMMAND: ddev names a host
     * command after the file it came from, so the probe and the invocation
     * cannot drift apart.
     */
    public const MARKER = 'commands/host/' . self::LOAD_COMMAND;

    public static function source(): string
    {
        $override = getenv(self::SOURCE_ENV);

        return $override !== false && $override !== '' ? $override : self::NAME;
    }
}

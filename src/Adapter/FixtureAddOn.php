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
 * installed-probe are built from it. They used to be two strings and they
 * disagreed: the adapter ran `ddev upkeep-fixture-load` while the add-on
 * published `ddev fixture-load`, so `--fixture` could not have worked and the
 * probe looked for a file that is never installed, re-fetching the add-on on
 * every single call. One fact, two strings, two repositories, nothing
 * comparing them.
 *
 * The name it settled on is the namespaced one, because ddev gives every
 * add-on's host commands one flat namespace per project and `fixture-load`
 * claims ground this add-on has no business claiming. That rename is
 * owenbush/ddev-upkeep#1, and it had to be on that repository's main branch
 * before this shipped: the probe looks for the command *file*, so a mismatch
 * reinstalls the add-on on every call and then fails on an unknown command.
 * tests/Integration/FixtureAddOnContractTest is what says whether they agree.
 *
 * **The release is pinned**, exactly as EngineAddOn pins ddev-drupal-contrib.
 * Until the add-on was published there was nothing to float to; a published
 * add-on with no pin means every environment silently tracks whatever its
 * latest release happens to be, so a breaking change over there — the command
 * rename above would have been one — arrives in every environment with no
 * warning and nothing that could have caught it. The contract test reads the
 * default branch, which is not what an environment installs.
 */
final readonly class FixtureAddOn
{
    /** Published add-on source, as `ddev add-on get` accepts it. */
    public const NAME = 'owenbush/ddev-upkeep';

    /**
     * The pinned release. Bumping it re-installs the add-on in every existing
     * environment on next use, because the stamp below stops matching.
     */
    public const VERSION = '1.0.0';

    /**
     * Where the installed version is recorded, under the directory the add-on
     * owns.
     *
     * upkeep's own file rather than ddev's add-on metadata: the location and
     * shape of that metadata is a ddev implementation detail that has moved
     * before, and a probe that silently stops finding it would read as "not
     * installed" and reinstall on every call — which is the bug this file has
     * already had once, from the other direction.
     */
    public const STAMP = 'upkeep/.upkeep-addon-version';

    /**
     * Env override for the add-on source — a local checkout path during
     * add-on development, e.g. UPKEEP_ADDON_SOURCE=/path/to/ddev-upkeep.
     */
    public const SOURCE_ENV = 'UPKEEP_ADDON_SOURCE';

    /**
     * The host command the add-on publishes for loading a fixture, as it is
     * invoked and as it is named on disk.
     */
    public const LOAD_COMMAND = 'upkeep-fixture-load';

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

    /** Whether the add-on comes from somewhere other than the published release. */
    public static function isOverridden(): bool
    {
        return self::source() !== self::NAME;
    }

    /**
     * The `ddev add-on get` arguments.
     *
     * `--version` only for the published add-on: an override is a local
     * checkout or an arbitrary source, where a release tag means nothing and
     * naming one is an error rather than a constraint.
     *
     * @return list<string>
     */
    public static function installArguments(): array
    {
        return self::isOverridden() ? [self::source()] : [self::NAME, '--version', self::VERSION];
    }

    /**
     * What an environment should have recorded once this add-on is installed.
     *
     * An override stamps its source, so moving between a local checkout and
     * the published release re-installs rather than trusting whichever landed
     * first — developing the two repositories together is exactly when a stale
     * add-on is hardest to notice.
     */
    public static function expectedStamp(): string
    {
        return self::isOverridden() ? 'source:' . self::source() : self::VERSION;
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Upkeep\Filesystem\FilesystemException;
use Upkeep\Filesystem\PathGuard;

/**
 * The `$HOME` containment rule for any directory whose contents get
 * bind-mounted into the Docker VM.
 *
 * This is a functional requirement first and a hardening measure second:
 * macOS Docker providers (colima, Docker Desktop) share only the home
 * directory by default, so a project or scratch tree created outside it can
 * never start. Refusing up front turns that into an explicable error instead
 * of an opaque mount failure minutes into a provision.
 *
 * Containment is decided on the canonicalised path, so neither a `..`
 * sequence nor a symlink pointing out of $HOME satisfies it.
 */
final readonly class MountablePath
{
    /**
     * @param string $what      what the path is, for the error message ("projects root")
     * @param string $howToSet  how the user supplies it ("--projects-root or $UPKEEP_PROJECTS_ROOT")
     *
     * @return string the canonical path
     *
     * @throws AdapterException when $HOME is unknown or the path is outside it
     */
    public static function requireUnderHome(string $candidate, string $what, string $howToSet): string
    {
        $home = self::home($what, $howToSet);

        try {
            $resolved = PathGuard::canonicalize($candidate);
            $contained = PathGuard::isWithin($home, $resolved);
        } catch (FilesystemException $e) {
            throw new AdapterException(
                sprintf('Cannot use "%s" as the %s: %s', $candidate, $what, $e->getMessage()),
                previous: $e,
            );
        }

        if (!$contained) {
            throw new AdapterException(sprintf(
                'Refusing the %s "%s" (resolves to "%s"): it is outside your home directory "%s". Its contents are '
                . 'bind-mounted into the Docker VM, and macOS Docker providers (colima, Docker Desktop) only share '
                . 'the home directory by default — an environment created outside it can never start. Point %s at a '
                . 'path under "%s".',
                $what,
                $candidate,
                $resolved,
                $home,
                $howToSet,
                $home,
            ));
        }

        return $resolved;
    }

    /**
     * @throws AdapterException when $HOME is unset or empty
     */
    public static function home(string $what, string $howToSet): string
    {
        $home = getenv('HOME');
        if ($home === false || $home === '') {
            throw new AdapterException(sprintf(
                'Cannot resolve a %s: $HOME is not set, so there is no default and nothing to verify a given path '
                . 'against. Refusing to fall back to a temp dir — Docker providers only mount the home directory, '
                . 'so anything created outside it can never start. Set $HOME, or point %s inside it.',
                $what,
                $howToSet,
            ));
        }

        return $home;
    }
}

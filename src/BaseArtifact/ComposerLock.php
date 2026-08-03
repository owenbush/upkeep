<?php

declare(strict_types=1);

namespace Upkeep\BaseArtifact;

/**
 * Minimal composer.lock reader: extracts the exact resolved drupal/core
 * version from a base tree's lock file for recording in meta.yml.
 */
final class ComposerLock
{
    public static function coreVersion(string $lockJson): string
    {
        try {
            $lock = json_decode($lockJson, true, flags: JSON_THROW_ON_ERROR);
        } catch (\JsonException $e) {
            throw new MetaException('composer.lock is not valid JSON: ' . $e->getMessage(), previous: $e);
        }

        // A lock file is untrusted input: only a package entry that is an
        // object naming drupal/core with a string version answers the
        // question. Anything else falls through to the failure below rather
        // than being coerced into a version that was never resolved.
        $packages = is_array($lock) ? ($lock['packages'] ?? null) : null;
        foreach (is_array($packages) ? $packages : [] as $package) {
            if (!is_array($package) || ($package['name'] ?? null) !== 'drupal/core') {
                continue;
            }

            $version = $package['version'] ?? null;
            if (is_string($version)) {
                return $version;
            }
        }

        throw new MetaException('composer.lock does not contain a resolved drupal/core package.');
    }
}

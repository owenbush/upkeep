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

        foreach ($lock['packages'] ?? [] as $package) {
            if (($package['name'] ?? null) === 'drupal/core' && is_string($package['version'] ?? null)) {
                return $package['version'];
            }
        }

        throw new MetaException('composer.lock does not contain a resolved drupal/core package.');
    }
}

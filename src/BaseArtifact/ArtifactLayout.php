<?php

declare(strict_types=1);

namespace Upkeep\BaseArtifact;

/**
 * Resolves the canonical on-disk layout of per-core-version base artifacts:
 *
 *   <cockpit>/base-artifacts/<core-major>/
 *       tree/                  resolved drupal/recommended-project base tree
 *       clean-install.sql.gz   gzipped module-free clean-install DB dump
 *       meta.yml               build metadata (exact core, PHP, DB, timestamp)
 *       canonical              marker file: this artifact set is canonical and
 *                              must be excluded from pruning (task 16 keys on
 *                              this marker / the base-artifacts dir itself).
 */
final readonly class ArtifactLayout
{
    public const TREE_DIR = 'tree';
    public const DUMP_FILENAME = 'clean-install.sql.gz';
    public const META_FILENAME = 'meta.yml';
    public const CANONICAL_MARKER = 'canonical';

    private const VERSION_PATTERN = '/^\d+$/';

    public function __construct(public string $baseArtifactsDir)
    {
    }

    public function versionDir(string $coreMajor): string
    {
        self::assertVersion($coreMajor);

        return $this->baseArtifactsDir . '/' . $coreMajor;
    }

    public function treePath(string $coreMajor): string
    {
        return $this->versionDir($coreMajor) . '/' . self::TREE_DIR;
    }

    public function dumpPath(string $coreMajor): string
    {
        return $this->versionDir($coreMajor) . '/' . self::DUMP_FILENAME;
    }

    public function metaPath(string $coreMajor): string
    {
        return $this->versionDir($coreMajor) . '/' . self::META_FILENAME;
    }

    public function canonicalMarkerPath(string $coreMajor): string
    {
        return $this->versionDir($coreMajor) . '/' . self::CANONICAL_MARKER;
    }

    /**
     * Core-major version directories present on disk, sorted numerically.
     *
     * @return list<string>
     */
    public function versionsOnDisk(): array
    {
        if (!is_dir($this->baseArtifactsDir)) {
            return [];
        }

        $versions = [];
        foreach (scandir($this->baseArtifactsDir) ?: [] as $entry) {
            if (preg_match(self::VERSION_PATTERN, $entry) === 1 && is_dir($this->baseArtifactsDir . '/' . $entry)) {
                $versions[] = $entry;
            }
        }
        usort($versions, static fn (string $a, string $b) => (int) $a <=> (int) $b);

        return $versions;
    }

    private static function assertVersion(string $coreMajor): void
    {
        if (preg_match(self::VERSION_PATTERN, $coreMajor) !== 1) {
            throw new \InvalidArgumentException(sprintf(
                'Core version must be a whole major version number, got "%s".',
                $coreMajor,
            ));
        }
    }
}

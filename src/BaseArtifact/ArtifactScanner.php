<?php

declare(strict_types=1);

namespace Upkeep\BaseArtifact;

/**
 * Reads the base-artifacts directory and reports, per core version, whether
 * the artifact set is complete (tree, dump, parseable meta, canonical marker),
 * its build metadata, and its on-disk sizes. Backs `base-artifacts:status`.
 */
final readonly class ArtifactScanner
{
    public function __construct(private ArtifactLayout $layout)
    {
    }

    /**
     * @return list<ArtifactRecord>
     */
    public function scan(): array
    {
        $records = [];
        foreach ($this->layout->versionsOnDisk() as $version) {
            $records[] = $this->scanVersion($version);
        }

        return $records;
    }

    private function scanVersion(string $version): ArtifactRecord
    {
        $missing = [];

        $treePath = $this->layout->treePath($version);
        if (!is_dir($treePath)) {
            $missing[] = ArtifactLayout::TREE_DIR . '/';
        }

        $dumpPath = $this->layout->dumpPath($version);
        if (!is_file($dumpPath)) {
            $missing[] = ArtifactLayout::DUMP_FILENAME;
        }

        $meta = null;
        $metaPath = $this->layout->metaPath($version);
        if (!is_file($metaPath)) {
            $missing[] = ArtifactLayout::META_FILENAME;
        } else {
            try {
                $meta = ArtifactMeta::fromYaml((string) file_get_contents($metaPath));
            } catch (MetaException) {
                $missing[] = ArtifactLayout::META_FILENAME . ' (unreadable)';
            }
        }

        if (!is_file($this->layout->canonicalMarkerPath($version))) {
            $missing[] = ArtifactLayout::CANONICAL_MARKER;
        }

        return new ArtifactRecord(
            $version,
            $missing === [],
            $meta,
            is_dir($treePath) ? self::directorySize($treePath) : 0,
            is_file($dumpPath) ? (int) filesize($dumpPath) : 0,
            $missing,
        );
    }

    /**
     * Symlinked directories are deliberately NOT followed: FOLLOW_SYMLINKS is
     * unset and RecursiveDirectoryIterator::hasChildren() defaults to
     * $allowLinks = false, so the measure stays inside the artifact tree.
     *
     * CATCH_GET_CHILD makes an unreadable subtree an under-count rather than
     * an UnexpectedValueException out of `base-artifacts:status` — a size
     * report is not worth failing the command over.
     */
    private static function directorySize(string $dir): int
    {
        $bytes = 0;
        $iterator = new \RecursiveIteratorIterator(
            new \RecursiveDirectoryIterator($dir, \FilesystemIterator::SKIP_DOTS),
            \RecursiveIteratorIterator::LEAVES_ONLY,
            \RecursiveIteratorIterator::CATCH_GET_CHILD,
        );
        foreach ($iterator as $file) {
            if ($file->isFile()) {
                $bytes += $file->getSize();
            }
        }

        return $bytes;
    }
}

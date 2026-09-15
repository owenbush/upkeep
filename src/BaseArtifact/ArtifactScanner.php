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
     * No symlink is followed, and it takes two mechanisms to say that.
     * FOLLOW_SYMLINKS is unset and RecursiveDirectoryIterator::hasChildren()
     * defaults to $allowLinks = false, which stops the walk descending into a
     * linked *directory* — and the isLink() test below is what stops a linked
     * *file* being counted, which that alone does not.
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
            // CURRENT_AS_FILEINFO is the directory iterator's default, so
            // every entry is an SplFileInfo — checked rather than assumed.
            // getSize() is false for a file that vanished mid-walk; an
            // under-count is the documented failure mode here, so such an
            // entry contributes nothing rather than aborting the measure.
            //
            // isLink() is checked *before* isFile(), and that is the whole
            // point: FOLLOW_SYMLINKS being unset stops the walk descending
            // into a linked directory, but a link to a *file* is a leaf, and
            // SplFileInfo follows it — isFile() answers for the target and
            // getSize() reports the target's bytes. So the measure did not
            // stay inside the tree, whatever the comment below says: a link
            // out of it was counted as though it were in it, and every
            // vendor/bin/* entry was counted a second time on top of the real
            // file it points at.
            if (!$file instanceof \SplFileInfo || $file->isLink() || !$file->isFile()) {
                continue;
            }

            $size = $file->getSize();
            if ($size !== false) {
                $bytes += $size;
            }
        }

        return $bytes;
    }
}

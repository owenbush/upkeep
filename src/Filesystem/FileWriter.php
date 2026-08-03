<?php

declare(strict_types=1);

namespace Upkeep\Filesystem;

/**
 * The single write path for every file this tool generates.
 *
 * Two guarantees, both of which the raw `file_put_contents` calls this
 * replaces did not give:
 *
 *   - **Atomic.** Bytes land in a temp file created in the *same* directory
 *     and are then `rename()`d over the target. rename(2) is atomic within a
 *     filesystem, so a concurrent reader sees either the complete old file or
 *     the complete new one, never a truncated one — and the target never stops
 *     existing, which matters for files that double as completion markers.
 *   - **Loud.** Every step is checked. A write that does not land raises
 *     FilesystemException naming the path, instead of returning false into a
 *     discarded value while the caller reports success.
 *
 * Modes are explicit because `file_put_contents` cannot take one and the umask
 * default (typically 0644) is wrong for the caches that carry token-scoped
 * remote data and raw check output.
 *
 * Not final, and only because of that second guarantee: the raw byte write and
 * the mode change are isolated into two overridable static primitives so the
 * failures this class exists to detect — a short write, a refused chmod — can
 * be simulated. They cannot be provoked on a working filesystem, and an
 * untested error path is exactly the kind that reports success over a write
 * that never landed. Subclassing is for that seam and nothing else: every
 * production write goes through FileWriter itself.
 */
class FileWriter
{
    /** Owner-only: files carrying token-scoped remote data or raw check output. */
    public const MODE_PRIVATE = 0o600;
    public const MODE_PRIVATE_DIR = 0o700;

    /** Ordinary cockpit content: registries, markers, metadata. */
    public const MODE_SHARED = 0o644;
    public const MODE_SHARED_DIR = 0o755;

    /**
     * Writes $contents to $path atomically.
     *
     * @param int|null $mode permissions for the resulting file; null keeps the
     *                       mode the file already has (MODE_SHARED when new)
     *
     * @throws FilesystemException when the write does not land
     */
    public static function write(string $path, string $contents, ?int $mode = null): void
    {
        self::commit(self::writeTemporary($path, $contents, $mode), $path);
    }

    /**
     * Writes $contents to a sibling temp file of $path and returns that temp
     * path, without touching $path. For callers that must inspect or validate
     * the exact final bytes before they become the real file; finish with
     * commit(), or unlink the temp file on rejection.
     *
     * @throws FilesystemException when the temp write does not land
     */
    public static function writeTemporary(string $path, string $contents, ?int $mode = null): string
    {
        $dir = \dirname($path);
        if (!is_dir($dir)) {
            throw new FilesystemException(sprintf(
                'Cannot write "%s": its directory "%s" does not exist.',
                $path,
                $dir,
            ));
        }
        if (is_dir($path)) {
            throw new FilesystemException(sprintf('Cannot write "%s": a directory exists at that path.', $path));
        }

        $effectiveMode = $mode ?? (is_file($path) ? (fileperms($path) & 0o777) : self::MODE_SHARED);

        // tempnam() falls back to the system temp directory when $dir is not
        // usable; a temp file there could not be rename()d atomically onto the
        // target, so that outcome is rejected rather than silently accepted.
        $temp = @tempnam($dir, '.' . basename($path) . '.');
        if ($temp === false || realpath(\dirname($temp)) !== realpath($dir)) {
            if ($temp !== false) {
                @unlink($temp);
            }

            throw new FilesystemException(sprintf(
                'Cannot create a temporary file next to "%s" — the directory "%s" is not writable.',
                $path,
                $dir,
            ));
        }

        $written = static::putContents($temp, $contents);
        if ($written === false || $written !== \strlen($contents)) {
            @unlink($temp);

            throw new FilesystemException(sprintf(
                'Failed to write %d byte(s) to "%s" (wrote %s).',
                \strlen($contents),
                $path,
                $written === false ? 'nothing' : (string) $written,
            ));
        }

        if (!static::setMode($temp, $effectiveMode)) {
            @unlink($temp);

            throw new FilesystemException(sprintf('Cannot set mode %o on "%s".', $effectiveMode, $path));
        }

        return $temp;
    }

    /**
     * The raw byte write. See the class comment: this is a seam, not an
     * extension point.
     *
     * @return int<0, max>|false bytes written, or false when nothing was
     */
    protected static function putContents(string $path, string $contents): int|false
    {
        return @file_put_contents($path, $contents);
    }

    /** The raw mode change. See the class comment: this is a seam. */
    protected static function setMode(string $path, int $mode): bool
    {
        return @chmod($path, $mode);
    }

    /**
     * Renames a temp file produced by writeTemporary() over its target.
     *
     * @throws FilesystemException when the rename fails
     */
    public static function commit(string $temp, string $path): void
    {
        if (!@rename($temp, $path)) {
            @unlink($temp);

            throw new FilesystemException(sprintf('Failed to move the completed write into place at "%s".', $path));
        }
    }

    /**
     * Creates a directory tree if it is not already there, tolerating a
     * concurrent creator.
     *
     * @throws FilesystemException when the directory does not exist afterwards
     */
    public static function ensureDirectory(string $path, int $mode = self::MODE_SHARED_DIR): void
    {
        if (is_dir($path)) {
            return;
        }
        if (!@mkdir($path, $mode, true) && !is_dir($path)) {
            throw new FilesystemException(sprintf('Could not create directory "%s".', $path));
        }
    }
}

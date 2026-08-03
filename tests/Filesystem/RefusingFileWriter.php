<?php

declare(strict_types=1);

namespace Upkeep\Tests\Filesystem;

use Upkeep\Filesystem\FileWriter;

/**
 * A writer that lands nothing at all — the `file_put_contents` returning false
 * that used to flow into a discarded value while the caller reported success.
 * Simulated through FileWriter's documented seam.
 */
final class RefusingFileWriter extends FileWriter
{
    protected static function putContents(string $path, string $contents): int|false
    {
        return false;
    }
}

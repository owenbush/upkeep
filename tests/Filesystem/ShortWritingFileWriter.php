<?php

declare(strict_types=1);

namespace Upkeep\Tests\Filesystem;

use Upkeep\Filesystem\FileWriter;

/**
 * A writer whose bytes do not all land. A partial write cannot be provoked on
 * a working filesystem, so it is simulated through the seam FileWriter exposes
 * for exactly that (see its class comment) — and it is the failure the class
 * was written to catch, so leaving it untested would defeat the point.
 */
final class ShortWritingFileWriter extends FileWriter
{
    protected static function putContents(string $path, string $contents): int|false
    {
        return @file_put_contents($path, substr($contents, 0, -1));
    }
}

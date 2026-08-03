<?php

declare(strict_types=1);

namespace Upkeep\Tests\Filesystem;

use Upkeep\Filesystem\FileWriter;

/**
 * A writer whose mode change is refused. Publishing the file anyway would put
 * token-scoped remote data and raw check output on disk at whatever mode the
 * temp file happened to have. Simulated through FileWriter's documented seam.
 */
final class UnchmodableFileWriter extends FileWriter
{
    protected static function setMode(string $path, int $mode): bool
    {
        return false;
    }
}

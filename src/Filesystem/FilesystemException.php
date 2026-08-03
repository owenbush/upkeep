<?php

declare(strict_types=1);

namespace Upkeep\Filesystem;

/**
 * A filesystem operation the tool cannot silently continue past: a refused
 * path, or a write that did not land.
 */
final class FilesystemException extends \RuntimeException
{
}

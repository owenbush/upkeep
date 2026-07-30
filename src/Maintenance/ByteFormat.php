<?php

declare(strict_types=1);

namespace Upkeep\Maintenance;

/**
 * Human-readable byte rendering for the maintenance tables (binary units,
 * matching what `du` measures).
 */
final readonly class ByteFormat
{
    public static function human(int $bytes): string
    {
        if ($bytes >= 1024 ** 3) {
            return sprintf('%.1f GiB', $bytes / 1024 ** 3);
        }
        if ($bytes >= 1024 ** 2) {
            return sprintf('%.1f MiB', $bytes / 1024 ** 2);
        }
        if ($bytes >= 1024) {
            return sprintf('%.1f KiB', $bytes / 1024);
        }

        return $bytes . ' B';
    }
}

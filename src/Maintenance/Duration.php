<?php

declare(strict_types=1);

namespace Upkeep\Maintenance;

/**
 * Parses the `--older-than` duration syntax (`30d`, `12h`, `90m`, `45s`,
 * `2w`) into seconds. Deliberately strict: a bare number is rejected so a
 * typo like `--older-than=30` never silently means "30 seconds".
 */
final readonly class Duration
{
    private const array UNIT_SECONDS = [
        'w' => 7 * 86400,
        'd' => 86400,
        'h' => 3600,
        'm' => 60,
        's' => 1,
    ];

    public static function parseToSeconds(string $input): int
    {
        if (preg_match('/^(\d+)([wdhms])$/', $input, $matches) !== 1) {
            throw new \InvalidArgumentException(sprintf(
                'Invalid duration "%s". Use <number><unit> with unit one of w, d, h, m, s (e.g. "30d", "12h").',
                $input,
            ));
        }

        return (int) $matches[1] * self::UNIT_SECONDS[$matches[2]];
    }
}

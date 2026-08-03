<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * POSIX single-quoting for values interpolated into a shell script body.
 *
 * A few engine invocations must be a `bash -c <script>` because the engine's
 * own commands assume a layout this tool does not use. The argv stays
 * list-form, so nothing can be injected into the *argument vector* — but the
 * script text itself is shell, and any value spliced into it is shell too.
 *
 * escapeshellarg() is not used here: its output is platform-dependent (it
 * double-quotes on Windows), while the script always runs in a Linux
 * container. This produces the same, deterministic single-quoted form on
 * every host.
 */
final readonly class ShellArgument
{
    /**
     * Wraps a value so a shell reads it as exactly one literal word.
     */
    public static function quote(string $value): string
    {
        return "'" . str_replace("'", "'\\''", $value) . "'";
    }
}

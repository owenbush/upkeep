<?php

declare(strict_types=1);

namespace Upkeep\Maintenance;

use Symfony\Component\Process\Process;
use Upkeep\Security\CredentialEnvironment;

/**
 * Measures real on-disk usage the same way an operator would check it:
 * `du -sk` (portable across macOS/BSD and GNU), reported in bytes.
 */
final readonly class DiskUsage
{
    public static function bytes(string $path): int
    {
        if (!file_exists($path)) {
            return 0;
        }

        $process = new Process(['du', '-sk', $path], null, CredentialEnvironment::scrubbed(), timeout: 300);
        $process->run();
        if (!$process->isSuccessful()) {
            return 0;
        }

        return (int) strtok($process->getOutput(), "\t ") * 1024;
    }
}

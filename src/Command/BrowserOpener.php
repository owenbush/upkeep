<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Process\Process;
use Upkeep\Security\CredentialEnvironment;

/**
 * Opens a URL in the operator's browser, best effort.
 *
 * One copy of the platform choice, and one place where the guarantee that the
 * opener — a child process like any other — never inherits the credential
 * environment is enforced.
 */
final readonly class BrowserOpener
{
    /** @return bool whether the browser was actually launched */
    public static function open(string $url): bool
    {
        $opener = \PHP_OS_FAMILY === 'Darwin' ? 'open' : 'xdg-open';
        $process = new Process([$opener, $url], null, CredentialEnvironment::scrubbed());
        $process->run();

        return $process->isSuccessful();
    }
}

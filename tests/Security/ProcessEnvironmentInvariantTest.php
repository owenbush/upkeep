<?php

declare(strict_types=1);

namespace Upkeep\Tests\Security;

use PHPUnit\Framework\TestCase;

/**
 * Structural guard for the first layer of the credential fix.
 *
 * Symfony's Process inherits the entire parent environment whenever its $env
 * argument is omitted, which is how the PAT reached every child process — and
 * from there the logs, the failure excerpts, and the cached result files. The
 * defence is only complete if it holds at *every* construction site, so this
 * asserts the property over the source rather than over one code path.
 */
final class ProcessEnvironmentInvariantTest extends TestCase
{
    public function testEveryProcessConstructionScrubsTheCredentialEnvironment(): void
    {
        $sites = 0;
        foreach ($this->sourceFiles() as $file) {
            $source = (string) file_get_contents($file);
            foreach (self::constructions($source) as $construction) {
                ++$sites;
                $this->assertStringContainsString(
                    'CredentialEnvironment::scrubbed()',
                    $construction,
                    sprintf('new Process(...) in %s must not inherit the credential environment.', $file),
                );
            }
        }

        // A floor, not an exact count: it only has to prove the scanner really
        // found the construction sites rather than silently matching nothing.
        // Four remain — ProcessRunner, DiskUsage, BrowserOpener, ExecCommand —
        // since the base-artifact build moved onto the adapter's shell-out
        // seam and stopped spawning children of its own.
        $this->assertGreaterThanOrEqual(4, $sites, 'Expected to find the known Process construction sites.');
    }

    /**
     * @return list<string> the text of each `new Process(` argument list
     */
    private static function constructions(string $source): array
    {
        $found = [];
        $offset = 0;
        while (($start = strpos($source, 'new Process(', $offset)) !== false) {
            $depth = 0;
            $i = $start + \strlen('new Process(') - 1;
            $length = \strlen($source);
            for (; $i < $length; ++$i) {
                if ($source[$i] === '(') {
                    ++$depth;
                } elseif ($source[$i] === ')') {
                    --$depth;
                    if ($depth === 0) {
                        break;
                    }
                }
            }
            $found[] = substr($source, $start, $i - $start + 1);
            $offset = $i;
        }

        return $found;
    }

    /**
     * @return list<string>
     */
    private static function sourceFiles(): array
    {
        $files = [];
        $iterator = new \RecursiveIteratorIterator(
            new \RecursiveDirectoryIterator(\dirname(__DIR__, 2) . '/src', \FilesystemIterator::SKIP_DOTS),
        );
        foreach ($iterator as $file) {
            if ($file instanceof \SplFileInfo && $file->getExtension() === 'php') {
                $files[] = $file->getPathname();
            }
        }
        sort($files);

        return $files;
    }
}

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
    /**
     * The one deliberate exception, and why it is one.
     *
     * `Ui\UiServer` starts the browser UI's own server, whose jobs are the
     * `upkeep` binary itself — a child that needs the credential to do the
     * work being asked of it and never prints it. Every other child this tool
     * spawns is arbitrary, could echo its environment into output that gets
     * logged, and gets nothing.
     *
     * It is exempt from *scrubbing*, not from being explicit: the assertion
     * still requires it to pass an environment it constructed itself, so it
     * can never regress to the inherit-everything default that this whole
     * guard exists to prevent. What that environment contains is asserted in
     * UiSurfaceTest, and the captured output is redacted again on its way to
     * the browser rather than trusted.
     *
     * @var array<string, string> file suffix => what its $env argument must name
     */
    private const DELIBERATE_FORWARDERS = ['src/Ui/UiServer.php' => '$environment'];

    public function testEveryProcessConstructionScrubsTheCredentialEnvironment(): void
    {
        $sites = 0;
        foreach ($this->sourceFiles() as $file) {
            $source = (string) file_get_contents($file);
            foreach (self::constructions($source) as $construction) {
                ++$sites;
                $this->assertStringContainsString(
                    self::expectationFor($file),
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
     * A forwarder must still be listed here to be allowed one, so adding an
     * unscrubbed construction anywhere else fails exactly as before.
     */
    private static function expectationFor(string $file): string
    {
        foreach (self::DELIBERATE_FORWARDERS as $suffix => $expected) {
            if (str_ends_with(str_replace('\\', '/', $file), $suffix)) {
                return $expected;
            }
        }

        return 'CredentialEnvironment::scrubbed()';
    }

    /**
     * The exemption list is short and is meant to stay that way: every entry
     * is a place the credential deliberately reaches a child, so growing it
     * silently is the failure this asserts against.
     */
    public function testExactlyOneConstructionSiteIsAllowedToForwardTheCredential(): void
    {
        self::assertSame(['src/Ui/UiServer.php'], array_keys(self::DELIBERATE_FORWARDERS));
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

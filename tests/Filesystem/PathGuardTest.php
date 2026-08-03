<?php

declare(strict_types=1);

namespace Upkeep\Tests\Filesystem;

use PHPUnit\Framework\TestCase;
use Upkeep\Filesystem\FilesystemException;
use Upkeep\Filesystem\PathGuard;

final class PathGuardTest extends TestCase
{
    private string $world;

    protected function setUp(): void
    {
        $this->world = (string) realpath(sys_get_temp_dir()) . '/upkeep-pathguard-' . bin2hex(random_bytes(4));
        mkdir($this->world . '/root/inside', 0o700, true);
        mkdir($this->world . '/outside', 0o700, true);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->world));
    }

    public function testCanonicaliseResolvesTraversalSegmentsOfAnExistingPath(): void
    {
        self::assertSame(
            $this->world . '/outside',
            PathGuard::canonicalize($this->world . '/root/inside/../../outside'),
        );
    }

    public function testCanonicaliseResolvesSymlinksRatherThanTrustingTheSpelling(): void
    {
        symlink($this->world . '/outside', $this->world . '/root/link');

        self::assertSame($this->world . '/outside', PathGuard::canonicalize($this->world . '/root/link'));
    }

    public function testCanonicaliseCanonicalisesTheDeepestExistingAncestorOfAPathThatDoesNotExistYet(): void
    {
        // realpath() returns false for a path that does not exist, so a
        // creation path must be canonicalised through its parent.
        self::assertSame(
            $this->world . '/root/inside/new/deeper',
            PathGuard::canonicalize($this->world . '/root/inside/new/deeper'),
        );
    }

    public function testCanonicaliseRejectsATraversalSegmentThatCannotBeResolved(): void
    {
        $this->expectException(FilesystemException::class);
        $this->expectExceptionMessageMatches('/traversal/i');

        PathGuard::canonicalize($this->world . '/root/does-not-exist/../escape');
    }

    public function testAPathWithNoExistingAncestorAtAllStillCanonicalisesAgainstTheRoot(): void
    {
        // Nothing below "/" resolves here, so the walk runs all the way to the
        // root — which is its own canonical spelling. Redundant separators are
        // still collapsed on the way.
        self::assertSame('/no-such-top-level/child', PathGuard::canonicalize('/no-such-top-level/child'));
        self::assertSame('/no-such-top-level/child', PathGuard::canonicalize('//no-such-top-level//child'));
        self::assertSame(
            $this->world . '/root/inside',
            PathGuard::canonicalize($this->world . '//root///inside'),
        );
    }

    public function testARelativePathIsResolvedAgainstTheWorkingDirectory(): void
    {
        $original = getcwd();
        self::assertNotFalse($original);
        chdir($this->world . '/root');

        try {
            self::assertSame($this->world . '/root/inside', PathGuard::canonicalize('inside'));
            self::assertSame($this->world . '/root/not-yet', PathGuard::canonicalize('not-yet'));
        } finally {
            chdir($original);
        }
    }

    public function testARelativePathIsRefusedWhenTheWorkingDirectoryHasBeenDeleted(): void
    {
        // A shell left sitting in a directory that has since been removed has
        // no working directory to resolve against. Refusing explains that;
        // resolving against nothing would silently root the path at "/".
        $original = getcwd();
        self::assertNotFalse($original);
        $doomed = $this->world . '/doomed';
        mkdir($doomed, 0o700);
        chdir($doomed);
        rmdir($doomed);

        try {
            $this->expectException(FilesystemException::class);
            $this->expectExceptionMessageMatches('/working directory is unavailable/');

            PathGuard::canonicalize('relative/target');
        } finally {
            chdir($original);
        }
    }

    public function testCanonicaliseRejectsAnEmptyPath(): void
    {
        $this->expectException(FilesystemException::class);

        PathGuard::canonicalize('');
    }

    public function testWithinAcceptsTheRootItselfAndAnythingUnderIt(): void
    {
        self::assertTrue(PathGuard::isWithin($this->world . '/root', $this->world . '/root'));
        self::assertTrue(PathGuard::isWithin($this->world . '/root', $this->world . '/root/inside'));
    }

    public function testWithinRejectsATraversalEscapeAndASiblingWithASharedPrefix(): void
    {
        self::assertFalse(PathGuard::isWithin($this->world . '/root', $this->world . '/root/../outside'));
        // "…/root-sibling" shares the string prefix "…/root" but is not under it.
        mkdir($this->world . '/root-sibling', 0o700);
        self::assertFalse(PathGuard::isWithin($this->world . '/root', $this->world . '/root-sibling'));
    }

    public function testWithinRejectsASymlinkEscapeThatNoPatternMatchOnDotDotWouldCatch(): void
    {
        symlink($this->world . '/outside', $this->world . '/root/link');

        self::assertFalse(PathGuard::isWithin($this->world . '/root', $this->world . '/root/link'));
    }
}

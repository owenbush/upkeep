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

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Filesystem;

use PHPUnit\Framework\TestCase;
use Upkeep\Filesystem\FilesystemException;
use Upkeep\Filesystem\FileWriter;

final class FileWriterTest extends TestCase
{
    private string $world;

    protected function setUp(): void
    {
        $this->world = (string) realpath(sys_get_temp_dir()) . '/upkeep-filewriter-' . bin2hex(random_bytes(4));
        mkdir($this->world, 0o700, true);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->world));
    }

    public function testWriteCreatesTheFileWithTheRequestedMode(): void
    {
        $path = $this->world . '/private.json';
        FileWriter::write($path, '{"a":1}', FileWriter::MODE_PRIVATE);

        self::assertSame('{"a":1}', file_get_contents($path));
        self::assertSame(0o600, fileperms($path) & 0o777);
    }

    public function testWriteLeavesNoTemporarySiblingBehind(): void
    {
        $path = $this->world . '/clean.txt';
        FileWriter::write($path, 'content', FileWriter::MODE_SHARED);

        self::assertSame(['clean.txt'], array_values(array_diff((array) scandir($this->world), ['.', '..'])));
    }

    public function testWriteIsAtomicSoAReaderNeverObservesAPartialFile(): void
    {
        $path = $this->world . '/atomic.txt';
        FileWriter::write($path, str_repeat('old', 1000), FileWriter::MODE_SHARED);

        // Observe the directory while a large replacement is written: the
        // target inode is only ever the complete old or the complete new
        // content, because the new bytes land in a sibling temp file that is
        // rename()d into place.
        $new = str_repeat('new', 100000);
        $observed = [];
        $watcher = static function () use ($path, &$observed): void {
            $observed[] = (string) file_get_contents($path);
        };

        $watcher();
        FileWriter::write($path, $new, FileWriter::MODE_SHARED);
        $watcher();

        foreach ($observed as $content) {
            self::assertContains(
                $content,
                [str_repeat('old', 1000), $new],
                'A reader observed neither the complete old nor the complete new content.',
            );
        }
        self::assertSame($new, file_get_contents($path));
    }

    public function testWriteReplacesTheTargetInPlaceRatherThanUnlinkingItFirst(): void
    {
        $path = $this->world . '/inplace.txt';
        FileWriter::write($path, 'first', FileWriter::MODE_SHARED);

        // Hold an open handle across the replacement. rename() over the target
        // keeps the old inode alive for existing readers; an unlink-then-write
        // sequence would leave a window with no file at the path at all.
        $handle = fopen($path, 'r');
        self::assertNotFalse($handle);
        FileWriter::write($path, 'second', FileWriter::MODE_SHARED);
        self::assertTrue(is_file($path), 'The path must never stop existing during a replacement.');
        self::assertSame('first', stream_get_contents($handle));
        fclose($handle);

        self::assertSame('second', file_get_contents($path));
    }

    public function testWritePreservesTheExistingModeWhenNoModeIsRequested(): void
    {
        $path = $this->world . '/keepmode.txt';
        FileWriter::write($path, 'a', 0o640);
        FileWriter::write($path, 'b', null);

        self::assertSame(0o640, fileperms($path) & 0o777);
    }

    public function testWriteFailsLoudlyWhenTheTargetDirectoryDoesNotExist(): void
    {
        $this->expectException(FilesystemException::class);
        $this->expectExceptionMessageMatches('#' . preg_quote($this->world . '/nope', '#') . '#');

        FileWriter::write($this->world . '/nope/file.txt', 'x', FileWriter::MODE_SHARED);
    }

    public function testWriteFailsLoudlyWhenTheTargetPathIsADirectory(): void
    {
        mkdir($this->world . '/adir', 0o700);

        $this->expectException(FilesystemException::class);

        FileWriter::write($this->world . '/adir', 'x', FileWriter::MODE_SHARED);
    }

    public function testWriteFailsLoudlyWhenTheDirectoryIsNotWritable(): void
    {
        $dir = $this->world . '/readonly';
        mkdir($dir, 0o500);

        try {
            $this->expectException(FilesystemException::class);
            FileWriter::write($dir . '/file.txt', 'x', FileWriter::MODE_SHARED);
        } finally {
            chmod($dir, 0o700);
        }
    }

    public function testAShortWriteIsReportedAndLeavesNeitherTargetNorTempFileBehind(): void
    {
        // The whole point of this class: `file_put_contents` returning fewer
        // bytes than were handed to it used to flow into a discarded return
        // value while the caller reported success.
        $path = $this->world . '/short.json';

        try {
            ShortWritingFileWriter::write($path, '{"a":1}', FileWriter::MODE_PRIVATE);
            self::fail('Expected a FilesystemException.');
        } catch (FilesystemException $e) {
            self::assertStringContainsString('7 byte(s)', $e->getMessage());
            self::assertStringContainsString($path, $e->getMessage());
        }

        self::assertFileDoesNotExist($path);
        self::assertSame([], $this->siblings(), 'A failed write must not leave a temp file behind.');
    }

    public function testAWriteThatProducesNoBytesAtAllIsReportedAsNothingWritten(): void
    {
        $path = $this->world . '/nothing.json';

        try {
            RefusingFileWriter::write($path, 'payload', FileWriter::MODE_PRIVATE);
            self::fail('Expected a FilesystemException.');
        } catch (FilesystemException $e) {
            self::assertStringContainsString('wrote nothing', $e->getMessage());
        }

        self::assertFileDoesNotExist($path);
        self::assertSame([], $this->siblings());
    }

    public function testAModeThatCannotBeAppliedFailsTheWriteRatherThanPublishingAWorldReadableFile(): void
    {
        // These files carry token-scoped remote data and raw check output. A
        // write whose 0600 did not take must not be published at whatever mode
        // the temp file happened to have.
        $path = $this->world . '/mode.json';

        try {
            UnchmodableFileWriter::write($path, 'secret', FileWriter::MODE_PRIVATE);
            self::fail('Expected a FilesystemException.');
        } catch (FilesystemException $e) {
            self::assertStringContainsString('Cannot set mode 600', $e->getMessage());
        }

        self::assertFileDoesNotExist($path);
        self::assertSame([], $this->siblings());
    }

    public function testCommitFailsLoudlyAndDiscardsTheTempFileWhenTheRenameCannotHappen(): void
    {
        // commit() is public so callers can validate the exact final bytes
        // first. A rename it cannot perform must not leave the caller believing
        // the file was published, nor the temp file sitting next to the target.
        $temp = FileWriter::writeTemporary($this->world . '/target.txt', 'bytes', FileWriter::MODE_SHARED);
        mkdir($this->world . '/target.txt');

        try {
            FileWriter::commit($temp, $this->world . '/target.txt');
            self::fail('Expected a FilesystemException.');
        } catch (FilesystemException $e) {
            self::assertStringContainsString('into place at', $e->getMessage());
        }

        self::assertFileDoesNotExist($temp);
        self::assertSame(['target.txt'], $this->siblings());
    }

    /**
     * @return list<string>
     */
    private function siblings(): array
    {
        $entries = scandir($this->world);
        self::assertNotFalse($entries);

        return array_values(array_diff($entries, ['.', '..']));
    }

    public function testEnsureDirectoryCreatesTheTreeWithTheRequestedMode(): void
    {
        $dir = $this->world . '/a/b/c';
        FileWriter::ensureDirectory($dir, FileWriter::MODE_PRIVATE_DIR);

        self::assertDirectoryExists($dir);
        self::assertSame(0o700, fileperms($dir) & 0o777);
    }

    public function testEnsureDirectoryIsIdempotent(): void
    {
        $dir = $this->world . '/idempotent';
        FileWriter::ensureDirectory($dir, FileWriter::MODE_PRIVATE_DIR);
        FileWriter::ensureDirectory($dir, FileWriter::MODE_PRIVATE_DIR);

        self::assertDirectoryExists($dir);
    }

    public function testEnsureDirectoryFailsLoudlyWhenThePathIsAFile(): void
    {
        FileWriter::write($this->world . '/afile', 'x', FileWriter::MODE_SHARED);

        $this->expectException(FilesystemException::class);

        FileWriter::ensureDirectory($this->world . '/afile', FileWriter::MODE_PRIVATE_DIR);
    }
}

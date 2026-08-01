<?php

declare(strict_types=1);

namespace Upkeep\Tests\Drupal;

use PHPUnit\Framework\TestCase;
use Upkeep\Drupal\IssueFile;

final class IssueFileTest extends TestCase
{
    public function testIsPatchRecognisesPatchExtension(): void
    {
        $file = new IssueFile('3467675-42.patch', 'https://example.com/3467675-42.patch', 1024, 1705313456);
        self::assertTrue($file->isPatch());
    }

    public function testIsPatchRecognisesDiffExtension(): void
    {
        $file = new IssueFile('fix.diff', 'https://example.com/fix.diff', 512, 1705313456);
        self::assertTrue($file->isPatch());
    }

    public function testIsPatchIsCaseInsensitive(): void
    {
        $file = new IssueFile('FIX.PATCH', 'https://example.com/FIX.PATCH', 512, 1705313456);
        self::assertTrue($file->isPatch());
    }

    public function testIsPatchReturnsFalseForOtherFiles(): void
    {
        $file = new IssueFile('screenshot.png', 'https://example.com/screenshot.png', 2048, 1705313456);
        self::assertFalse($file->isPatch());
    }

    public function testCommentNumberFromStandardNaming(): void
    {
        $file = new IssueFile('3467675-42.patch', 'https://example.com/3467675-42.patch', 1024, 0);
        self::assertSame(42, $file->commentNumber());
    }

    public function testCommentNumberFromDescriptiveNaming(): void
    {
        $file = new IssueFile('3467675-12-config-schema.patch', 'https://example.com/f.patch', 1024, 0);
        self::assertSame(12, $file->commentNumber());
    }

    public function testCommentNumberReturnsNullForNonStandardNaming(): void
    {
        $file = new IssueFile('my-fix.patch', 'https://example.com/my-fix.patch', 1024, 0);
        self::assertNull($file->commentNumber());
    }

    public function testFromApiWithNestedFileFormat(): void
    {
        $data = [
            'file' => [
                'filename' => '3467675-42.patch',
                'url' => 'https://www.drupal.org/files/issues/3467675-42.patch',
                'filesize' => '1024',
                'timestamp' => '1705313456',
            ],
        ];

        $file = IssueFile::fromApi($data);
        self::assertNotNull($file);
        self::assertSame('3467675-42.patch', $file->name);
        self::assertSame('https://www.drupal.org/files/issues/3467675-42.patch', $file->url);
        self::assertSame(1024, $file->size);
        self::assertSame(1705313456, $file->timestamp);
    }

    public function testFromApiFlatFormat(): void
    {
        $data = [
            'filename' => 'fix.patch',
            'url' => 'https://example.com/fix.patch',
            'filesize' => '512',
            'timestamp' => '1705400000',
        ];

        $file = IssueFile::fromApi($data);
        self::assertNotNull($file);
        self::assertSame('fix.patch', $file->name);
    }

    public function testFromApiReturnsNullForMissingFields(): void
    {
        self::assertNull(IssueFile::fromApi([]));
        self::assertNull(IssueFile::fromApi(['filename' => 'test.patch']));
    }

    public function testRoundTrip(): void
    {
        $original = new IssueFile('3467675-42.patch', 'https://example.com/3467675-42.patch', 1024, 1705313456);
        $restored = IssueFile::fromApi($original->toApiArray());

        self::assertNotNull($restored);
        self::assertSame($original->name, $restored->name);
        self::assertSame($original->url, $restored->url);
        self::assertSame($original->size, $restored->size);
        self::assertSame($original->timestamp, $restored->timestamp);
    }
}

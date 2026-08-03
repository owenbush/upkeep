<?php

declare(strict_types=1);

namespace Upkeep\Tests\Drupal;

use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\TestCase;
use Upkeep\Drupal\ApiPayload;

#[CoversClass(ApiPayload::class)]
final class ApiPayloadTest extends TestCase
{
    public function testReadsScalarFieldsAndFallsBackToTheDefault(): void
    {
        $payload = new ApiPayload(['title' => 'Fix the thing']);

        self::assertSame('Fix the thing', $payload->string('title'));
        self::assertSame('', $payload->string('absent'));
        self::assertSame('-', $payload->string('absent', '-'));
        self::assertNull($payload->stringOrNull('absent'));
    }

    /**
     * The API renders the same field as a JSON number on one endpoint and a
     * quoted string on another, so both read as the same value.
     */
    public function testQuotedAndUnquotedNumbersReadAlike(): void
    {
        $payload = new ApiPayload(['nid' => 3456789, 'field_issue_status' => '8', 'filesize' => '1024']);

        self::assertSame(3456789, $payload->int('nid'));
        self::assertSame('3456789', $payload->string('nid'));
        self::assertSame(8, $payload->int('field_issue_status'));
        self::assertSame(1024, $payload->int('filesize'));
    }

    /**
     * intOrNull() exists so a caller can reject an absent id rather than read
     * it as the perfectly plausible node 0.
     */
    public function testAbsentIntegersAreDistinguishableFromZero(): void
    {
        $payload = new ApiPayload(['nid' => '0']);

        self::assertSame(0, $payload->intOrNull('nid'));
        self::assertNull($payload->intOrNull('absent'));
        self::assertSame(0, $payload->int('absent'));
    }

    /**
     * A wrongly shaped field is the default, never a coerced value: (string)
     * on an array is the word "Array" and (int) on one is 1, both of which
     * would read downstream as real answers.
     */
    public function testWronglyShapedFieldsDoNotCoerce(): void
    {
        $payload = new ApiPayload(['title' => ['a', 'b'], 'nid' => 'not-a-number', 'nothing' => null]);

        self::assertSame('', $payload->string('title'));
        self::assertNull($payload->stringOrNull('title'));
        self::assertNull($payload->intOrNull('title'));
        self::assertNull($payload->intOrNull('nid'));
        self::assertNull($payload->stringOrNull('nothing'));
    }

    public function testChildReadsNestedObjectsAndNullOtherwise(): void
    {
        $payload = new ApiPayload([
            'field_project' => ['machine_name' => 'token'],
            'field_issue_files' => ['und' => [['file' => ['filename' => 'a.patch']]]],
            'scalar' => 7,
        ]);

        self::assertSame('token', $payload->child('field_project')?->stringOrNull('machine_name'));
        self::assertNull($payload->child('scalar'));
        self::assertNull($payload->child('absent'));
        self::assertSame(
            [['file' => ['filename' => 'a.patch']]],
            $payload->child('field_issue_files')?->childArray('und'),
        );
        self::assertNull($payload->childArray('scalar'));
        self::assertNull($payload->childArray('absent'));
    }
}

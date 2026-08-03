<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\EngineDescription;

#[CoversClass(EngineDescription::class)]
final class EngineDescriptionTest extends TestCase
{
    public function testReadsTopLevelAndNestedStringFields(): void
    {
        $description = EngineDescription::fromJson(json_encode([
            'raw' => [
                'status' => 'running',
                'primary_url' => 'https://upkeep-token-d11.ddev.site',
                'dbinfo' => ['database_type' => 'mariadb', 'database_version' => '10.11'],
            ],
        ], \JSON_THROW_ON_ERROR));

        self::assertNotNull($description);
        self::assertSame('running', $description->stringOrNull('status'));
        self::assertSame('https://upkeep-token-d11.ddev.site', $description->stringOrNull('primary_url'));
        self::assertSame('mariadb', $description->stringOrNull('dbinfo', 'database_type'));
        self::assertSame('10.11', $description->stringOrNull('dbinfo', 'database_version'));
    }

    public function testAbsentPathStepsReadAsNull(): void
    {
        $description = EngineDescription::fromJson('{"raw":{"dbinfo":{}}}');

        self::assertNotNull($description);
        self::assertNull($description->stringOrNull('status'));
        self::assertNull($description->stringOrNull('dbinfo', 'database_type'));
        self::assertNull($description->stringOrNull('nope', 'database_type'));
    }

    /**
     * A structured field is "not known", not a coerced string: `(string)`
     * on an array is the word "Array", which would read downstream as a real
     * value. A scalar is rendered, matching the API-payload readers.
     */
    public function testStructuredFieldsReadAsNullAndScalarsRender(): void
    {
        $description = EngineDescription::fromJson('{"raw":{"status":42,"primary_url":["a"],"dbinfo":"nope"}}');

        self::assertNotNull($description);
        self::assertSame('42', $description->stringOrNull('status'));
        self::assertNull($description->stringOrNull('primary_url'));
        self::assertNull($description->stringOrNull('dbinfo', 'database_type'));
    }

    public function testUnusableOutputIsNoDescriptionAtAll(): void
    {
        self::assertNull(EngineDescription::fromJson(null));
        self::assertNull(EngineDescription::fromJson(''));
        self::assertNull(EngineDescription::fromJson('not json'));
        self::assertNull(EngineDescription::fromJson('"a string"'));
        self::assertNull(EngineDescription::fromJson('{"error":"no such project"}'));
        self::assertNull(EngineDescription::fromJson('{"raw":null}'));
        self::assertNull(EngineDescription::fromJson('{"raw":"not an object"}'));
    }
}

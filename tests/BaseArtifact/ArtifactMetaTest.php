<?php

declare(strict_types=1);

namespace Upkeep\Tests\BaseArtifact;

use PHPUnit\Framework\TestCase;
use Upkeep\BaseArtifact\ArtifactMeta;
use Upkeep\BaseArtifact\MetaException;

final class ArtifactMetaTest extends TestCase
{
    private const VALID_YAML = <<<'YAML'
        core_version: 11.4.4
        core_major: '11'
        php_version: 8.3.30
        db_engine: 'mariadb:10.11'
        built_at: '2026-07-29T14:02:11+00:00'

        YAML;

    public function testParsesValidYaml(): void
    {
        $meta = ArtifactMeta::fromYaml(self::VALID_YAML);

        self::assertSame('11.4.4', $meta->coreVersion);
        self::assertSame('11', $meta->coreMajor);
        self::assertSame('8.3.30', $meta->phpVersion);
        self::assertSame('mariadb:10.11', $meta->dbEngine);
        self::assertSame('2026-07-29T14:02:11+00:00', $meta->builtAt->format(\DateTimeInterface::ATOM));
    }

    public function testYamlRoundTrips(): void
    {
        $meta = ArtifactMeta::fromYaml(self::VALID_YAML);
        $again = ArtifactMeta::fromYaml($meta->toYaml());

        self::assertEquals($meta, $again);
    }

    public function testRejectsMissingKey(): void
    {
        $this->expectException(MetaException::class);
        $this->expectExceptionMessage('php_version');

        ArtifactMeta::fromYaml("core_version: 11.4.4\ncore_major: '11'\ndb_engine: mariadb\nbuilt_at: '2026-07-29T14:02:11+00:00'\n");
    }

    public function testRejectsMalformedYaml(): void
    {
        $this->expectException(MetaException::class);

        ArtifactMeta::fromYaml("{{{ not yaml");
    }

    public function testRejectsNonMappingYaml(): void
    {
        $this->expectException(MetaException::class);

        ArtifactMeta::fromYaml("- just\n- a list\n");
    }

    public function testRejectsUnparseableTimestamp(): void
    {
        $this->expectException(MetaException::class);
        $this->expectExceptionMessage('built_at');

        ArtifactMeta::fromYaml("core_version: 11.4.4\ncore_major: '11'\nphp_version: 8.3.30\ndb_engine: mariadb\nbuilt_at: 'not a date'\n");
    }
}

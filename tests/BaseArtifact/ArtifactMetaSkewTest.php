<?php

declare(strict_types=1);

namespace Upkeep\Tests\BaseArtifact;

use PHPUnit\Framework\TestCase;
use Upkeep\BaseArtifact\ArtifactMeta;

final class ArtifactMetaSkewTest extends TestCase
{
    private function meta(): ArtifactMeta
    {
        return new ArtifactMeta('11.4.4', '11', '8.3.30', 'mariadb:10.11', new \DateTimeImmutable('2026-07-29T14:02:11+00:00'));
    }

    public function testNoSkewForMatchingEnvironment(): void
    {
        self::assertSame([], $this->meta()->skewAgainst('8.3.30', 'mariadb:10.11'));
    }

    public function testPhpPatchDifferenceIsNotSkew(): void
    {
        self::assertSame([], $this->meta()->skewAgainst('8.3.99', 'mariadb:10.11'));
    }

    public function testPhpMinorDifferenceIsSkew(): void
    {
        $reasons = $this->meta()->skewAgainst('8.4.2', 'mariadb:10.11');

        self::assertCount(1, $reasons);
        self::assertStringContainsString('PHP', $reasons[0]);
        self::assertStringContainsString('8.3', $reasons[0]);
        self::assertStringContainsString('8.4', $reasons[0]);
    }

    public function testDbEngineDifferenceIsSkew(): void
    {
        $reasons = $this->meta()->skewAgainst('8.3.30', 'mysql:8.0');

        self::assertCount(1, $reasons);
        self::assertStringContainsString('DB engine', $reasons[0]);
        self::assertStringContainsString('mariadb:10.11', $reasons[0]);
        self::assertStringContainsString('mysql:8.0', $reasons[0]);
    }

    public function testBothDifferencesReportBothReasons(): void
    {
        self::assertCount(2, $this->meta()->skewAgainst('8.4.2', 'mysql:8.0'));
    }
}

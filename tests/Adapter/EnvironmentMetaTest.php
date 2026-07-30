<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\EnvironmentMeta;

final class EnvironmentMetaTest extends TestCase
{
    private function meta(): EnvironmentMeta
    {
        return new EnvironmentMeta(
            moduleName: 'conditions_helper',
            coreMajor: '11',
            seedCoreVersion: '11.4.4',
            addOnVersion: '1.1.5',
            createdAt: new \DateTimeImmutable('2026-07-29T12:00:00+00:00'),
        );
    }

    public function testYamlRoundTripPreservesEveryField(): void
    {
        $restored = EnvironmentMeta::fromYaml($this->meta()->toYaml());

        self::assertSame('conditions_helper', $restored->moduleName);
        self::assertSame('11', $restored->coreMajor);
        self::assertSame('11.4.4', $restored->seedCoreVersion);
        self::assertSame('1.1.5', $restored->addOnVersion);
        self::assertSame('2026-07-29T12:00:00+00:00', $restored->createdAt->format(\DateTimeInterface::ATOM));
    }

    public function testRejectsYamlMissingARequiredKey(): void
    {
        $this->expectException(AdapterException::class);
        EnvironmentMeta::fromYaml("module: conditions_helper\ncore_major: '11'\n");
    }

    public function testRejectsMalformedYaml(): void
    {
        $this->expectException(AdapterException::class);
        EnvironmentMeta::fromYaml("{ not: yaml: at all");
    }

    public function testMatchingEnvironmentHasNoStaleReasons(): void
    {
        self::assertSame([], $this->meta()->staleReasons('conditions_helper', '11', '11.4.4', '1.1.5'));
    }

    public function testEveryMismatchProducesItsOwnReason(): void
    {
        $reasons = $this->meta()->staleReasons('token_or', '10', '11.5.0', '1.2.0');

        self::assertCount(4, $reasons);
        self::assertStringContainsString('token_or', $reasons[0]);
        self::assertStringContainsString('10', $reasons[1]);
        self::assertStringContainsString('11.5.0', $reasons[2]);
        self::assertStringContainsString('1.2.0', $reasons[3]);
    }

    public function testSeedSkewAloneMarksTheEnvironmentStale(): void
    {
        $reasons = $this->meta()->staleReasons('conditions_helper', '11', '11.5.1', '1.1.5');

        self::assertCount(1, $reasons);
        self::assertStringContainsString('11.4.4', $reasons[0]);
        self::assertStringContainsString('11.5.1', $reasons[0]);
    }
}

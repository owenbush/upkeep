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

    /**
     * `prune --older-than` filters on last use. Until the field is written,
     * age silently falls back to creation time and prune can delete an
     * environment that was reused yesterday.
     */
    public function testLastUsedAtRoundTripsSoAgeFilteringHasSomethingToRead(): void
    {
        $stamped = $this->meta()->withLastUsedAt(new \DateTimeImmutable('2026-08-01T09:30:00+00:00'));

        self::assertStringContainsString('last_used_at', $stamped->toYaml());
        $restored = EnvironmentMeta::fromYaml($stamped->toYaml());
        self::assertNotNull($restored->lastUsedAt);
        self::assertSame('2026-08-01T09:30:00+00:00', $restored->lastUsedAt->format(\DateTimeInterface::ATOM));
    }

    public function testAMetaWithoutLastUsedAtStillParses(): void
    {
        $restored = EnvironmentMeta::fromYaml(
            "module: conditions_helper\ncore_major: '11'\nseed_core_version: 11.4.4\n"
            . "addon_version: 1.1.5\ncreated_at: '2026-07-29T12:00:00+00:00'\n",
        );

        self::assertNull($restored->lastUsedAt);
    }

    public function testAnUnparseableLastUsedAtIsRefusedRatherThanSilentlyIgnored(): void
    {
        $this->expectException(AdapterException::class);
        EnvironmentMeta::fromYaml(
            "module: conditions_helper\ncore_major: '11'\nseed_core_version: 11.4.4\n"
            . "addon_version: 1.1.5\ncreated_at: '2026-07-29T12:00:00+00:00'\nlast_used_at: 'not a date'\n",
        );
    }

    public function testStampingLastUseRewritesTheDotfileWithoutEverRemovingIt(): void
    {
        $dir = (string) realpath(sys_get_temp_dir()) . '/upkeep-envmeta-' . bin2hex(random_bytes(4));
        mkdir($dir, 0o700, true);

        try {
            $this->meta()->writeTo($dir);
            $path = $dir . '/' . EnvironmentMeta::FILENAME;

            // The dotfile doubles as the provisioning completion marker: if it
            // ever disappears mid-stamp, the next run tears down and rebuilds
            // a multi-gigabyte environment. rename() is why it cannot.
            $handle = fopen($path, 'r');
            self::assertNotFalse($handle);
            EnvironmentMeta::stampLastUsed($dir, new \DateTimeImmutable('2026-08-02T10:00:00+00:00'));
            self::assertTrue(is_file($path));
            self::assertStringNotContainsString('last_used_at', (string) stream_get_contents($handle));
            fclose($handle);

            $restored = EnvironmentMeta::fromYaml((string) file_get_contents($path));
            self::assertNotNull($restored->lastUsedAt);
            self::assertSame('2026-08-02T10:00:00+00:00', $restored->lastUsedAt->format(\DateTimeInterface::ATOM));
            self::assertSame('conditions_helper', $restored->moduleName);
            self::assertSame(
                '2026-07-29T12:00:00+00:00',
                $restored->createdAt->format(\DateTimeInterface::ATOM),
                'Stamping last use must not disturb the creation time.',
            );
            self::assertSame(
                [EnvironmentMeta::FILENAME],
                array_values(array_diff((array) scandir($dir), ['.', '..'])),
            );
        } finally {
            exec('rm -rf ' . escapeshellarg($dir));
        }
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

    /**
     * The dotfile doubles as the provisioning completion marker, so anything
     * that is not a well-formed meta mapping must be an error rather than a
     * partially populated meta the reuse decision would then trust.
     */
    public function testAMalformedMetaDocumentIsRefusedWithWhatIsWrongWithIt(): void
    {
        $cases = [
            "- module: widget\n" => 'must be a mapping',
            "module: widget\ncore_major: 11\nseed_core_version: 11.4.4\naddon_version: 1.1.5\n"
                . "created_at: '2026-01-01T00:00:00+00:00'\nlast_used_at: { not: a timestamp }\n"
                => '"last_used_at" must be a timestamp',
        ];

        foreach ($cases as $yaml => $expected) {
            try {
                EnvironmentMeta::fromYaml($yaml);
                self::fail(sprintf('Expected "%s" to be refused.', $expected));
            } catch (AdapterException $e) {
                self::assertStringContainsString($expected, $e->getMessage());
            }
        }
    }

    /**
     * Stamping a reuse reads the existing dotfile first. A missing one means
     * the environment was never fully provisioned, which is not something to
     * paper over by writing a fresh marker.
     */
    public function testStampingAnEnvironmentWithNoMarkerIsRefused(): void
    {
        $dir = (string) realpath(sys_get_temp_dir()) . '/upkeep-meta-' . bin2hex(random_bytes(4));
        mkdir($dir, 0o700, true);

        try {
            EnvironmentMeta::stampLastUsed($dir);
            self::fail('Expected the missing marker to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('Cannot read the environment meta at', $e->getMessage());
            self::assertFileDoesNotExist($dir . '/' . EnvironmentMeta::FILENAME);
        } finally {
            exec('rm -rf ' . escapeshellarg($dir));
        }
    }
}

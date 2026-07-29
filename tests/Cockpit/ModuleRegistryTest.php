<?php

declare(strict_types=1);

namespace Upkeep\Tests\Cockpit;

use PHPUnit\Framework\TestCase;
use Upkeep\Cockpit\ModuleRegistry;
use Upkeep\Cockpit\RegistryException;

final class ModuleRegistryTest extends TestCase
{
    public function testLoadsModulesFromAValidRegistryFile(): void
    {
        $registry = ModuleRegistry::fromFile(__DIR__ . '/fixtures/valid-registry.yml');

        $modules = $registry->modules();

        self::assertCount(2, $modules);

        $module = $modules['entity_reference_tweaks'];
        self::assertSame('entity_reference_tweaks', $module->name);
        self::assertSame('project/entity_reference_tweaks', $module->project);
        self::assertSame(['10', '11'], $module->coreVersions);

        self::assertSame(['11'], $modules['token_or']->coreVersions);
    }

    public function testRejectsAModuleEntryMissingRequiredFields(): void
    {
        $this->expectException(RegistryException::class);
        $this->expectExceptionMessageMatches('/broken_module.*project/');

        ModuleRegistry::fromFile(__DIR__ . '/fixtures/malformed-registry.yml');
    }

    public function testRejectsAFileThatIsNotARegistryMapping(): void
    {
        $this->expectException(RegistryException::class);
        $this->expectExceptionMessageMatches('/modules/');

        ModuleRegistry::fromFile(__DIR__ . '/fixtures/not-a-registry.yml');
    }

    public function testRejectsAMissingRegistryFile(): void
    {
        $this->expectException(RegistryException::class);
        $this->expectExceptionMessageMatches('/no-such-registry\.yml/');

        ModuleRegistry::fromFile(__DIR__ . '/fixtures/no-such-registry.yml');
    }
}

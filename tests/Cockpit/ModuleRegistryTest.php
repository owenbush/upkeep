<?php

declare(strict_types=1);

namespace Upkeep\Tests\Cockpit;

use PHPUnit\Framework\Attributes\DataProvider;
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

    /**
     * The machine name becomes a path segment under <cockpit>/results/ and
     * <cockpit>/cache/dashboard/, and an engine project name deep inside
     * prune. It is validated once here, at load, so neither of those ever
     * receives a traversal sequence and prune never aborts mid-teardown on an
     * uncaught InvalidArgumentException.
     */
    #[DataProvider('rejectedModuleNames')]
    public function testRejectsAModuleNameThatIsNotADrupalMachineName(string $name): void
    {
        $path = self::writeRegistry(sprintf(
            "modules:\n  %s:\n    project: project/thing\n    core_versions: [\"11\"]\n",
            $name,
        ));

        try {
            $this->expectException(RegistryException::class);
            $this->expectExceptionMessageMatches('/machine name/');
            ModuleRegistry::fromFile($path);
        } finally {
            @unlink($path);
        }
    }

    /**
     * @return iterable<string, array{string}>
     */
    public static function rejectedModuleNames(): iterable
    {
        yield 'traversal out of the results tree' => ['"../../../etc"'];
        yield 'a single traversal segment' => ['".."'];
        yield 'a path separator' => ['"a/b"'];
        yield 'an absolute path' => ['"/etc/passwd"'];
        yield 'a leading digit' => ['"9lives"'];
        yield 'uppercase' => ['"NotAMachineName"'];
        yield 'a hyphen' => ['"not-a-machine-name"'];
        yield 'empty' => ['""'];
    }

    public function testRejectsACoreVersionThatIsNotAWholeMajorVersion(): void
    {
        $path = self::writeRegistry(
            "modules:\n  token_or:\n    project: project/token_or\n    core_versions: [\"../11\"]\n",
        );

        try {
            $this->expectException(RegistryException::class);
            $this->expectExceptionMessageMatches('/core_versions/');
            ModuleRegistry::fromFile($path);
        } finally {
            @unlink($path);
        }
    }

    private static function writeRegistry(string $yaml): string
    {
        $path = sys_get_temp_dir() . '/upkeep-registry-validation-' . bin2hex(random_bytes(4)) . '.yml';
        file_put_contents($path, $yaml);

        return $path;
    }
}

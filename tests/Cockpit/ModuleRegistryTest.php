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

    /**
     * The registry is the tool's single source of truth and the file operators
     * hand-edit most often, so every malformed shape has to name what is wrong
     * with it rather than surfacing later as a type error inside a command.
     *
     * @param non-empty-string $expected
     */
    #[DataProvider('malformedRegistries')]
    public function testEveryMalformedRegistryShapeIsRejectedWithAnExplanation(string $yaml, string $expected): void
    {
        $path = self::writeRegistry($yaml);

        try {
            $this->expectException(RegistryException::class);
            $this->expectExceptionMessageMatches($expected);
            ModuleRegistry::fromFile($path);
        } finally {
            @unlink($path);
        }
    }

    /**
     * @return iterable<string, array{string, non-empty-string}>
     */
    public static function malformedRegistries(): iterable
    {
        yield 'not YAML at all' => ["modules:\n  token_or:\n   - [unclosed\n", '/not valid YAML/'];
        yield 'modules is a scalar' => ["modules: everything\n", '/must be a mapping of machine name/'];
        // A YAML sequence parses as an array, so it is caught one step later:
        // its keys are integers, which are not machine names.
        yield 'modules is a list of names' => ["modules:\n  - token_or\n", '/Module key "0".*machine name/s'];
        yield 'a module definition is a scalar' => [
            "modules:\n  token_or: project/token_or\n",
            '/token_or.*must be a mapping/s',
        ];
        yield 'project is missing' => ["modules:\n  token_or:\n    core_versions: [\"11\"]\n", '/non-empty "project"/'];
        yield 'project is empty' => [
            "modules:\n  token_or:\n    project: ''\n    core_versions: [\"11\"]\n",
            '/non-empty "project"/',
        ];
        yield 'core_versions is missing' => [
            "modules:\n  token_or:\n    project: project/token_or\n",
            '/core_versions.*non-empty list/s',
        ];
        yield 'core_versions is empty' => [
            "modules:\n  token_or:\n    project: project/token_or\n    core_versions: []\n",
            '/core_versions.*non-empty list/s',
        ];
        yield 'core_versions is a mapping, not a list' => [
            "modules:\n  token_or:\n    project: project/token_or\n    core_versions:\n      first: '11'\n",
            '/core_versions.*non-empty list/s',
        ];
    }

    public function testAnEmptyRegistryLoadsAsNoModulesRatherThanFailing(): void
    {
        // `upkeep init` scaffolds a registry with no modules yet; every command
        // that parses it must survive that state and report "none registered".
        $path = self::writeRegistry("modules:\n");

        try {
            self::assertSame([], ModuleRegistry::fromFile($path)->modules());
        } finally {
            @unlink($path);
        }
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

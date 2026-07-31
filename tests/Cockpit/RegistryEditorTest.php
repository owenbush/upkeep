<?php

declare(strict_types=1);

namespace Upkeep\Tests\Cockpit;

use PHPUnit\Framework\TestCase;
use Upkeep\Cockpit\Module;
use Upkeep\Cockpit\ModuleRegistry;
use Upkeep\Cockpit\RegistryEditor;

final class RegistryEditorTest extends TestCase
{
    private string $path;

    protected function setUp(): void
    {
        $this->path = sys_get_temp_dir() . '/upkeep-registry-' . bin2hex(random_bytes(4)) . '.yml';
        file_put_contents($this->path, <<<YAML
        modules:
          conditions_helper:
            project: project/conditions_helper
            core_versions: ["11"]
        YAML);
    }

    protected function tearDown(): void
    {
        @unlink($this->path);
    }

    public function testAddAppendsNewModulesAndPreservesExistingEntries(): void
    {
        $added = new RegistryEditor($this->path)->add([
            new Module('token_or', 'project/token_or', ['10', '11']),
        ]);

        self::assertSame(['token_or'], $added);
        $modules = ModuleRegistry::fromFile($this->path)->modules();
        self::assertSame(['conditions_helper', 'token_or'], array_keys($modules));
        self::assertSame('project/conditions_helper', $modules['conditions_helper']->project);
        self::assertSame(['10', '11'], $modules['token_or']->coreVersions);
    }

    public function testAddingAnAlreadyRegisteredModuleIsANoOp(): void
    {
        $editor = new RegistryEditor($this->path);
        $added = $editor->add([
            new Module('conditions_helper', 'project/conditions_helper', ['10']),
            new Module('fresh_module', 'project/fresh_module', ['11']),
        ]);

        self::assertSame(['fresh_module'], $added);
        $modules = ModuleRegistry::fromFile($this->path)->modules();
        // The existing entry keeps its original definition.
        self::assertSame(['11'], $modules['conditions_helper']->coreVersions);
        self::assertSame(['conditions_helper', 'fresh_module'], array_keys($modules));
    }

    public function testAddValidatesThroughTheRegistryParserBeforeWriting(): void
    {
        $editor = new RegistryEditor($this->path);
        $before = file_get_contents($this->path);

        $this->expectException(\Upkeep\Cockpit\RegistryException::class);
        try {
            $editor->add([new Module('bad', '', ['11'])]);
        } finally {
            self::assertSame($before, file_get_contents($this->path), 'A rejected add must not touch the file.');
        }
    }
}

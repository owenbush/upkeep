<?php

declare(strict_types=1);

namespace Upkeep\Tests\Cockpit;

use PHPUnit\Framework\TestCase;
use Upkeep\Cockpit\Module;
use Upkeep\Cockpit\ModuleRegistry;
use Upkeep\Cockpit\RegistryEditor;
use Upkeep\Filesystem\FilesystemException;

final class RegistryEditorTest extends TestCase
{
    private string $dir;

    private string $path;

    protected function setUp(): void
    {
        $this->dir = (string) realpath(sys_get_temp_dir()) . '/upkeep-registry-' . bin2hex(random_bytes(4));
        mkdir($this->dir, 0o700, true);
        $this->path = $this->dir . '/registry.yml';
        file_put_contents($this->path, <<<YAML
        modules:
          conditions_helper:
            project: project/conditions_helper
            core_versions: ["11"]
        YAML);
    }

    protected function tearDown(): void
    {
        @chmod($this->dir, 0o700);
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    /**
     * @return list<string>
     */
    private function siblings(): array
    {
        $entries = scandir($this->dir);
        self::assertNotFalse($entries, 'The registry directory must be readable.');

        return array_values(array_diff($entries, ['.', '..']));
    }

    public function testAddAppendsNewModulesAndPreservesExistingEntries(): void
    {
        $added = (new RegistryEditor($this->path))->add([
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

    public function testAnAddOfNothingNewDoesNotRewriteTheRegistryAtAll(): void
    {
        // Dumping regenerates the YAML and loses hand-written comments, so an
        // add that changes nothing must not reach the writer at all — re-running
        // `modules:add` for an already-registered module has to be inert.
        $before = (string) file_get_contents($this->path);

        $added = (new RegistryEditor($this->path))->add([
            new Module('conditions_helper', 'project/conditions_helper', ['10', '11']),
        ]);

        self::assertSame([], $added);
        self::assertSame($before, file_get_contents($this->path));
        self::assertSame(['registry.yml'], $this->siblings());
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
            self::assertSame(['registry.yml'], $this->siblings(), 'A rejected add must not leave a temp file.');
        }
    }

    /**
     * The registry is the tool's single source of truth and this is its only
     * writer. The validated bytes must be renamed into place, not deleted and
     * written a second time: a crash in that second write would leave a
     * truncated or empty registry.
     */
    public function testTheValidatedBytesAreRenamedIntoPlaceSoACrashCannotDestroyTheRegistry(): void
    {
        $before = (string) file_get_contents($this->path);

        // Hold the pre-add inode open. A rename() over the path leaves this
        // reader on the intact old file and the path itself never vanishes;
        // an in-place rewrite would show the new bytes through this handle.
        $handle = fopen($this->path, 'r');
        self::assertNotFalse($handle);

        (new RegistryEditor($this->path))->add([new Module('token_or', 'project/token_or', ['11'])]);

        self::assertTrue(is_file($this->path), 'The registry path must never stop existing during an add.');
        self::assertSame($before, stream_get_contents($handle), 'The pre-add content must survive on its own inode.');
        fclose($handle);

        self::assertStringContainsString('token_or', (string) file_get_contents($this->path));
        self::assertSame(['registry.yml'], $this->siblings(), 'A successful add must leave no temp file behind.');
    }

    public function testAFailedWriteIsReportedRatherThanReturningTheNamesAsAdded(): void
    {
        $editor = new RegistryEditor($this->path);
        $before = (string) file_get_contents($this->path);
        chmod($this->dir, 0o500);

        try {
            $this->expectException(FilesystemException::class);
            $editor->add([new Module('token_or', 'project/token_or', ['11'])]);
        } finally {
            chmod($this->dir, 0o700);
            self::assertSame($before, file_get_contents($this->path));
        }
    }

    public function testAValidationFailureNamesTheRealRegistryNotTheTemporaryFile(): void
    {
        $editor = new RegistryEditor($this->path);

        try {
            $editor->add([new Module('bad', '', ['11'])]);
            self::fail('Expected a RegistryException.');
        } catch (\Upkeep\Cockpit\RegistryException $e) {
            self::assertStringContainsString($this->path, $e->getMessage());
        }
    }
}

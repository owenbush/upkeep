<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Workflow\ExitCode;

/**
 * `upkeep modules` is the answer to "what does this cockpit know about", and
 * every other command's "not registered" error points at it. Both of its
 * outcomes are successes: a table, or the sentence that says where to add
 * entries.
 */
final class ModulesCommandTest extends TestCase
{
    private ?CliHarness $cli = null;

    protected function tearDown(): void
    {
        $this->cli?->destroy();
    }

    private function cli(): CliHarness
    {
        return $this->cli ??= CliHarness::create('modules');
    }

    public function testItListsEveryRegisteredModuleWithItsProjectAndTrackedCores(): void
    {
        $cli = $this->cli();
        $cli->registerModule('widget', 'project/widget_ui', ['10', '11']);
        $cli->registerModule('gadget');

        self::assertSame(ExitCode::OK, $cli->run('modules'), $cli->display());

        $display = $cli->display();
        self::assertStringContainsString('widget', $display);
        self::assertStringContainsString('project/widget_ui', $display);
        self::assertStringContainsString('10, 11', $display);
        self::assertStringContainsString('gadget', $display);
    }

    /**
     * A parseable registry with no entries is a fresh cockpit, not a broken
     * one: it exits 0 and names the file to edit, because this is the command
     * every "not registered" error sends the operator to.
     */
    public function testAnEmptyRegistryNamesTheFileToAddEntriesToAndStillExitsOk(): void
    {
        $cli = $this->cli();
        $cli->writeRegistry("modules: {}\n");

        self::assertSame(ExitCode::OK, $cli->run('modules'), $cli->display());
        self::assertStringContainsString('No modules registered yet.', $cli->display());
        self::assertStringContainsString($cli->cockpit . '/registry.yml', $cli->display());
    }
}

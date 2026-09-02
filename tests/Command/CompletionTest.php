<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandCompletionTester;
use Upkeep\Command\CheckCommand;
use Upkeep\Command\DevCommand;
use Upkeep\Command\PatchPromoteCommand;
use Upkeep\Adapter\Environment;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Tests\Support\StubEngineAdapterFactory;

/**
 * Shell completion for the values, which is the tedious half.
 *
 * Command *names* complete for free — Symfony Console registers that itself.
 * What it cannot know is that `<module>` means "one of the machine names in
 * this operator's registry", and that is the token every invocation starts
 * with and the one that is long and easy to mistype.
 *
 * The property that matters most is the last one here: completion must never
 * throw. It runs on every press of TAB, and an exception would spill a stack
 * trace across the prompt of somebody who only wanted a module name.
 */
final class CompletionTest extends TestCase
{
    private string $cockpit;

    private string|false $previousCockpit = false;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-completion-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit, 0o755, true);
        // The cockpit comes from the environment rather than --cockpit=, so
        // the token being completed is the one under test. It is also how a
        // cockpit is actually configured for daily use.
        $this->previousCockpit = getenv('UPKEEP_COCKPIT');
        putenv('UPKEEP_COCKPIT=' . $this->cockpit);
        $this->writeRegistry(
            "modules:\n"
            . "  field_inheritance:\n    project: project/field_inheritance\n    core_versions: ['10', '11']\n"
            . "  pathauto:\n    project: project/pathauto\n    core_versions: ['11']\n",
        );
    }

    protected function tearDown(): void
    {
        putenv('UPKEEP_COCKPIT' . ($this->previousCockpit === false ? '' : '=' . $this->previousCockpit));
        exec('rm -rf ' . escapeshellarg($this->cockpit));
    }

    private function writeRegistry(string $yaml): void
    {
        file_put_contents($this->cockpit . '/registry.yml', $yaml);
    }

    /** Completion touches no engine; the commands only need one to exist. */
    private static function engines(): StubEngineAdapterFactory
    {
        return new StubEngineAdapterFactory(FakeEngineAdapter::withEnvironment(new Environment(
            'x',
            '11',
            'x-11',
            '/tmp/x-11',
            'https://x-11.test',
            false,
        )));
    }

    private static function check(): CheckCommand
    {
        return new CheckCommand(self::engines());
    }

    public function testTheModuleArgumentSuggestsTheRegistryModules(): void
    {
        $tester = new CommandCompletionTester(self::check());

        $suggestions = $tester->complete(['']);

        self::assertSame(['field_inheritance', 'pathauto'], $suggestions);
    }

    /** Every command that takes a module gets it, because the base does. */
    public function testEveryModuleTakingCommandCompletesIt(): void
    {
        $commands = [self::check(), new DevCommand(self::engines())];

        foreach ($commands as $command) {
            $suggestions = (new CommandCompletionTester($command))->complete(['']);

            self::assertContains('pathauto', $suggestions, $command->getName() ?? '');
        }
    }

    /**
     * `--version` is the target-core selector everywhere, so it offers the
     * cores the named module actually tracks — not every core any module uses,
     * which would suggest a version that command would then refuse.
     */
    public function testVersionSuggestsOnlyTheCoresTheNamedModuleTracks(): void
    {
        $tester = new CommandCompletionTester(self::check());

        $suggestions = $tester->complete(['pathauto', '12', '--version=']);

        self::assertSame(['11'], $suggestions);
    }

    public function testVersionWithNoModuleNamedYetOffersEveryTrackedCore(): void
    {
        $tester = new CommandCompletionTester(self::check());

        $suggestions = $tester->complete(['--version=']);

        self::assertSame(['10', '11'], $suggestions);
    }

    public function testAnUnknownModuleFallsBackToEveryTrackedCore(): void
    {
        $tester = new CommandCompletionTester(self::check());

        $suggestions = $tester->complete(['not_a_module', '12', '--version=']);

        self::assertSame(['10', '11'], $suggestions);
    }

    /** A patch command completes the same way; the surface is shared. */
    public function testThePatchCommandsCompleteModulesToo(): void
    {
        $tester = new CommandCompletionTester(new PatchPromoteCommand(self::engines()));

        self::assertContains('field_inheritance', $tester->complete(['']));
    }

    // --------------------------------------------------- it must never throw

    /**
     * The load-bearing one. TAB is pressed constantly and in every state a
     * cockpit can be in; a stack trace across the prompt would be worse than
     * no completion at all. Every one of these suggests nothing and raises
     * nothing — the ordinary command run a moment later reports the problem
     * properly.
     */
    public function testABrokenCockpitSuggestsNothingRatherThanThrowing(): void
    {
        $tester = new CommandCompletionTester(self::check());

        $this->writeRegistry("modules:\n  broken: [this is not a mapping\n");
        self::assertSame([], $tester->complete(['']));

        unlink($this->cockpit . '/registry.yml');
        self::assertSame([], $tester->complete(['']));

        putenv('UPKEEP_COCKPIT=' . $this->cockpit . '/nope');
        self::assertSame([], $tester->complete(['']));
    }

    public function testAnUnrelatedArgumentSuggestsNothing(): void
    {
        $tester = new CommandCompletionTester(self::check());

        // The MR IID: a number off GitLab, which nothing local can enumerate.
        self::assertSame([], $tester->complete(['pathauto', '']));
    }
}

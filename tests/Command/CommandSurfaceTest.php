<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\Environment;
use Symfony\Component\Console\Command\Command;
use Upkeep\Adapter\VolumeProbe;
use Upkeep\Command\ApiProbeCommand;
use Upkeep\Command\BaseArtifactsBuildCommand;
use Upkeep\Command\BaseArtifactsStatusCommand;
use Upkeep\Command\CheckCommand;
use Upkeep\Command\DashboardCommand;
use Upkeep\Command\DevCommand;
use Upkeep\Command\EnvPathCommand;
use Upkeep\Command\ExecCommand;
use Upkeep\Command\InitCommand;
use Upkeep\Command\IssueCommand;
use Upkeep\Command\MergeCommand;
use Upkeep\Command\ModulesAddCommand;
use Upkeep\Command\ModulesCommand;
use Upkeep\Command\NeedsWorkCommand;
use Upkeep\Command\NotesCommand;
use Upkeep\Command\PatchesCommand;
use Upkeep\Command\PruneCommand;
use Upkeep\Command\ReviewCommand;
use Upkeep\Command\StatusCommand;
use Upkeep\Command\UpkeepCommand;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Tests\Support\StubEngineAdapterFactory;

/**
 * Drift guards for the shared CLI surface.
 *
 * The option descriptions used to be copy-pasted — sixteen definitions of
 * `--cockpit`, six of `--projects-root`, and four different wordings of
 * `--version` — and had already begun to diverge. These assertions fail if a
 * command starts defining its own again instead of using the shared seam.
 */
final class CommandSurfaceTest extends TestCase
{
    /** @return list<Command> every registered command, wired as bin/upkeep wires it */
    private static function commands(): array
    {
        $engines = new StubEngineAdapterFactory(FakeEngineAdapter::withEnvPath(null));
        $probe = new VolumeProbe(static fn (array $command): ?string => null);

        return [
            new ApiProbeCommand(),
            new BaseArtifactsBuildCommand(),
            new BaseArtifactsStatusCommand(),
            new CheckCommand($engines),
            new DashboardCommand(),
            new DevCommand($engines),
            new EnvPathCommand($engines),
            new ExecCommand($engines),
            new InitCommand(),
            new IssueCommand(),
            new MergeCommand(),
            new ModulesAddCommand(),
            new ModulesCommand(),
            new NeedsWorkCommand(),
            new NotesCommand(),
            new PatchesCommand(),
            new PruneCommand($engines, $probe),
            new ReviewCommand($engines),
            new StatusCommand($probe),
        ];
    }

    /**
     * The end-to-end tests are only worth their name if the application they
     * drive is the application `bin/upkeep` builds. Nothing can make a test
     * harness and a composition root share code — one is a script, the other
     * a class — so this asserts they registered the same commands. A command
     * added to `bin/upkeep` and not to the harness would otherwise be a
     * command no end-to-end test could ever reach.
     */
    /**
     * Which merge-request commands may read without a credential.
     *
     * A policy, pinned as one. drupalcode serves a public project's merge
     * requests and refs anonymously, so `check` and `review` — which fetch a
     * branch and check it out, and change nothing — no longer demand a token.
     * Anything that writes must not join them by inheriting a default, so the
     * default is strict and each read-only command says so for itself.
     *
     * If a future command extends AbstractMrCommand to write, this test is
     * where the decision gets made rather than discovered.
     */
    public function testOnlyTheReadOnlyMergeRequestCommandsSkipTheCredential(): void
    {
        $engines = new StubEngineAdapterFactory(FakeEngineAdapter::withEnvironment(new Environment(
            'widget',
            '11',
            'upkeep-widget-d11',
            sys_get_temp_dir() . '/upkeep-surface-unused',
            'https://localhost',
            true,
        )));

        $readsOnly = [];
        foreach ([new CheckCommand($engines), new ReviewCommand($engines)] as $command) {
            $method = new \ReflectionMethod($command, 'readsOnly');
            $readsOnly[(string) $command->getName()] = (bool) $method->invoke($command);
        }

        self::assertSame(['check' => true, 'review' => true], $readsOnly);
    }

    /**
     * The dependency check runs before anything is constructed.
     *
     * Structural, because it cannot be reached any other way: the suite runs
     * where every dependency is installed, so the guard's own branch never
     * fires here. What can be asserted is that it is still there and still
     * first — a checkout whose vendor/ predates a newly declared dependency
     * must meet one sentence and exit 2, not an uncaught "Class ... not found"
     * from wherever that package happened to be reached.
     */
    public function testBinUpkeepChecksItsDependenciesBeforeComposingAnything(): void
    {
        $source = file_get_contents(__DIR__ . '/../../bin/upkeep');
        self::assertIsString($source);

        $guard = strpos($source, 'RuntimeRequirements::missing(');
        $composition = strpos($source, 'new DdevContribAdapterFactory(');

        self::assertIsInt($guard, 'bin/upkeep no longer checks its runtime dependencies');
        self::assertIsInt($composition);
        self::assertLessThan($composition, $guard, 'the check must precede the composition root');
        self::assertStringContainsString('ExitCode::INFRASTRUCTURE', $source, 'and refuse with a 2');
    }

    public function testTheHarnessRegistersExactlyTheCommandsBinUpkeepDoes(): void
    {
        $source = file_get_contents(__DIR__ . '/../../bin/upkeep');
        self::assertIsString($source);
        self::assertGreaterThan(0, preg_match_all('/new (\w+Command)\(/', $source, $matches));

        $composed = array_values(array_unique($matches[1]));
        sort($composed);

        $harness = CliHarness::create('surface');
        try {
            $registered = [];
            foreach ($harness->application()->all() as $command) {
                $class = new \ReflectionClass($command);
                if (str_starts_with($class->getName(), 'Upkeep\\')) {
                    $registered[$class->getShortName()] = true;
                }
            }
        } finally {
            $harness->destroy();
        }

        $wired = array_keys($registered);
        sort($wired);
        self::assertSame($composed, $wired);
    }

    /**
     * The contract is only "one place" if every command actually goes through
     * it — a command extending Symfony's Command directly would keep its own
     * exit codes and its own error handling.
     */
    public function testEveryCommandExtendsTheSharedBase(): void
    {
        foreach (self::commands() as $command) {
            self::assertInstanceOf(UpkeepCommand::class, $command, $command::class);
        }
    }

    /**
     * @return iterable<string, array{string}>
     */
    public static function sharedOptions(): iterable
    {
        yield '--cockpit' => ['cockpit'];
        yield '--projects-root' => ['projects-root'];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('sharedOptions')]
    public function testSharedOptionsHaveOneDescriptionEverywhere(string $name): void
    {
        $descriptions = [];
        foreach (self::commands() as $command) {
            $definition = $command->getDefinition();
            if ($definition->hasOption($name)) {
                $descriptions[$command->getName() ?? $command::class] = $definition->getOption($name)
                    ->getDescription();
            }
        }

        self::assertGreaterThan(1, \count($descriptions), sprintf('--%s should be shared by several commands', $name));
        self::assertCount(1, array_unique($descriptions), sprintf('--%s descriptions have drifted', $name));
    }

    /**
     * `--version` had four wordings across six commands. It now has exactly
     * three, each a distinct documented meaning: the target-core selector
     * shared by every module-scoped command, the dashboard's row filter, and
     * base-artifacts:build's "which core to build for" (no module, so no
     * registry entry to be tracked by).
     */
    public function testTheVersionOptionsWordingsGroupByMeaningNotByCommand(): void
    {
        $byDescription = [];
        foreach (self::commands() as $command) {
            $definition = $command->getDefinition();
            if ($definition->hasOption('version')) {
                $byDescription[$definition->getOption('version')->getDescription()][] = (string) $command->getName();
            }
        }

        $groups = array_values($byDescription);
        foreach ($groups as $commands) {
            sort($commands);
        }
        sort($groups);

        self::assertSame(
            [
                ['base-artifacts:build'],
                ['dashboard'],
                ['check', 'dev', 'env:path', 'exec', 'needs-work', 'review'],
            ],
            $groups,
        );
    }

    /** No command may reintroduce an application-version flag under this name. */
    public function testNoCommandTreatsVersionAsAnApplicationVersionFlag(): void
    {
        foreach (self::commands() as $command) {
            $definition = $command->getDefinition();
            if (!$definition->hasOption('version')) {
                continue;
            }
            $option = $definition->getOption('version');
            $name = (string) $command->getName();
            self::assertTrue($option->acceptValue(), $name . ': --version must take a core major');
            self::assertNull($option->getShortcut(), $name . ': --version must not claim -V');
        }
    }

    /**
     * BP-CMD-16: the base-artifacts build used --core on the strength of a
     * Symfony constraint that VersionOptionInput removed.
     */
    public function testBaseArtifactsBuildSelectsItsCoreVersionLikeEveryOtherCommand(): void
    {
        $definition = (new BaseArtifactsBuildCommand())->getDefinition();

        self::assertTrue($definition->hasOption('version'));
        self::assertFalse($definition->hasOption('core'));
    }

    /** The module argument is one wording, not four. */
    public function testTheModuleArgumentHasOneDescriptionWhereItNamesARegisteredModule(): void
    {
        $engines = new StubEngineAdapterFactory(FakeEngineAdapter::withEnvPath(null));
        $descriptions = [];
        foreach (
            [
                new CheckCommand($engines),
                new ReviewCommand($engines),
                new DevCommand($engines),
                new EnvPathCommand($engines),
                new ExecCommand($engines),
                new IssueCommand(),
                new NeedsWorkCommand(),
            ] as $command
        ) {
            $descriptions[] = $command->getDefinition()->getArgument('module')->getDescription();
        }

        self::assertCount(1, array_unique($descriptions));
    }

    /** The policy stance, asserted structurally: no unattended merge path. */
    public function testMergeExposesNoOptionThatCouldSkipTheHumanApproval(): void
    {
        $definition = (new MergeCommand())->getDefinition();

        $options = array_keys($definition->getOptions());
        sort($options);
        self::assertSame(['cockpit', 'fast-lane'], $options);
        self::assertFalse(
            $definition->getOption('fast-lane')->acceptValue(),
            '--fast-lane names the workflow; it must never carry a value that could mean "and do not prompt"',
        );
    }
}

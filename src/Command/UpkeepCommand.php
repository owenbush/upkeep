<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\ProjectsRoot;
use Upkeep\BaseArtifact\BuildException;
use Upkeep\BaseArtifact\MetaException;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\Module;
use Upkeep\Cockpit\RegistryException;
use Upkeep\Filesystem\FilesystemException;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\MrContextResolver;
use Upkeep\Workflow\WorkflowException;

/**
 * The base every upkeep command extends. It exists so three things are
 * decided once instead of twenty-one times:
 *
 *  1. **The exit-code contract.** `execute()` is final: it runs the command
 *     and turns any domain failure into `ExitCode::INFRASTRUCTURE` with the
 *     exception's own message. Commands return `ExitCode::*` and nothing
 *     else — never `Command::FAILURE`/`SUCCESS`/`INVALID`, whose numbers
 *     collide with the contract while meaning something different.
 *  2. **The shared CLI surface.** `--cockpit`, `--projects-root`,
 *     `--version`, the `module`/`mr` arguments and `--no-open` are defined
 *     here, so a description cannot drift between commands.
 *  3. **The shared resolution seam.** Cockpit, registry, module lookup,
 *     core-version selection and MR-IID validation all happen through one
 *     path with one message and one failure mode.
 *
 * Commands stay thin: everything below delegates to Cockpit, ModuleRegistry
 * and Workflow\MrContextResolver, which own those rules and are unit-tested.
 */
abstract class UpkeepCommand extends Command
{
    final protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = $this->style($input, $output);

        try {
            return $this->perform($input, $output, $io);
        } catch (
            WorkflowException
            | AdapterException
            | RegistryException
            | FilesystemException
            | BuildException
            | MetaException $e
        ) {
            $io->error($e->getMessage());

            return ExitCode::INFRASTRUCTURE;
        }
    }

    /**
     * The command's work. Throwing any domain exception is the documented way
     * to report an infrastructure failure; the base reports it and maps it.
     *
     * @return int one of the ExitCode constants
     */
    abstract protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int;

    /**
     * Whether diagnostics belong on stderr. True for commands whose stdout
     * carries machine-readable output (a table, Markdown, a path) that must
     * stay pipeable.
     */
    protected function diagnosticsOnStderr(): bool
    {
        return false;
    }

    // ---------------------------------------------------------------- surface

    protected function addCockpitOption(): static
    {
        $this->addOption(
            'cockpit',
            null,
            InputOption::VALUE_REQUIRED,
            sprintf('Path to the cockpit directory (defaults to $%s, then the current directory)', Cockpit::ENV_VAR),
        );

        return $this;
    }

    protected function addProjectsRootOption(): static
    {
        $this->addOption(
            'projects-root',
            null,
            InputOption::VALUE_REQUIRED,
            sprintf(
                'Directory holding the engine environments (defaults to $%s, then <cockpit>/projects/ if it exists, '
                . 'then ~/.upkeep/projects)',
                ProjectsRoot::ENV_VAR,
            ),
        );

        return $this;
    }

    protected function addModuleArgument(): static
    {
        $this->addArgument(
            'module',
            InputArgument::REQUIRED,
            'Registered module machine name (see `upkeep modules`)',
        );

        return $this;
    }

    protected function addMrArgument(): static
    {
        $this->addArgument(
            'mr',
            InputArgument::REQUIRED,
            'Merge request IID on the module\'s drupalcode project',
        );

        return $this;
    }

    /**
     * `--version` as the *target core selector*: which tracked core major the
     * command acts against. Never an app-version flag (see VersionOptionInput).
     */
    protected function addTargetCoreOption(): static
    {
        $this->addOption(
            'version',
            null,
            InputOption::VALUE_REQUIRED,
            'Target core major version; must be tracked by the module\'s registry entry. Defaults to the first core '
            . 'version listed there.',
        );

        return $this;
    }

    /**
     * `--version` as a *filter* over already-assembled rows, for the reporting
     * commands. Deliberately a separate seam from the selector above: same
     * flag name, different meaning, each with one canonical wording.
     */
    protected function addCoreFilterOption(): static
    {
        $this->addOption(
            'version',
            null,
            InputOption::VALUE_REQUIRED,
            'Only show rows targeting this core major version (e.g. 11)',
        );

        return $this;
    }

    protected function addNoOpenOption(): static
    {
        $this->addOption(
            'no-open',
            null,
            InputOption::VALUE_NONE,
            'Do not open the drupal.org issue in the browser',
        );

        return $this;
    }

    // ------------------------------------------------------------- resolution

    /**
     * Cockpit resolution: `--cockpit` > $UPKEEP_COCKPIT > cwd.
     *
     * @throws FilesystemException when the path is empty or unresolvable
     */
    protected function cockpit(InputInterface $input): Cockpit
    {
        // Deliberately NOT collapsing '' to null: `--cockpit=` is a mistake
        // worth naming, and Cockpit::resolve() names it.
        return Cockpit::resolve(self::stringOption($input, 'cockpit'));
    }

    /**
     * The registry's modules, loaded and validated once, up front — including
     * on the destructive commands, where a parse failure discovered halfway
     * through would leave the operator staring at a plan that will not run.
     *
     * @return array<string, Module> keyed by machine name
     *
     * @throws RegistryException when the registry is missing or invalid
     */
    protected function modules(Cockpit $cockpit): array
    {
        return $cockpit->loadRegistry()->modules();
    }

    /**
     * The cockpit, proven usable: resolved and its registry parsed, so a
     * command that needs a cockpit but not its contents still reports a
     * missing or malformed one the same way every other command does.
     *
     * @throws FilesystemException|RegistryException
     */
    protected function requireCockpit(InputInterface $input): Cockpit
    {
        $cockpit = $this->cockpit($input);
        $this->modules($cockpit);

        return $cockpit;
    }

    /**
     * @param array<string, Module> $modules
     *
     * @throws WorkflowException when the module is not registered
     */
    protected static function requireModule(array $modules, string $name): Module
    {
        return MrContextResolver::requireModule($modules, $name);
    }

    /**
     * The target core major for this invocation: `--version` when given (and
     * tracked), otherwise the first core version in the module's registry
     * entry.
     *
     * @throws WorkflowException when the requested version is not tracked
     */
    protected static function targetCore(InputInterface $input, Module $module): string
    {
        return MrContextResolver::selectCoreVersion($module, self::stringOption($input, 'version'));
    }

    /**
     * The one MR-IID rule: a positive integer. `!0` is not a merge request,
     * so it is rejected here rather than turned into a confusing 404.
     *
     * @throws WorkflowException when the argument is not a positive integer
     */
    protected static function mrIid(InputInterface $input, string $argument = 'mr'): int
    {
        $raw = self::stringArgument($input, $argument);
        if (preg_match('/^\d+$/', $raw) !== 1 || (int) $raw < 1) {
            throw new WorkflowException(sprintf(
                'The <%s> argument must be a merge request IID (a positive integer), got "%s".',
                $argument,
                $raw,
            ));
        }

        return (int) $raw;
    }

    /**
     * A value-taking option as a string, or null when it was not given.
     *
     * Console input is `mixed`; narrowing it once here keeps every command
     * free of its own cast, and keeps the narrowing rule identical
     * everywhere.
     */
    protected static function stringOption(InputInterface $input, string $name): ?string
    {
        $value = $input->getOption($name);

        return \is_scalar($value) && $value !== false ? (string) $value : null;
    }

    /** A required argument as a string. */
    protected static function stringArgument(InputInterface $input, string $name): string
    {
        $value = $input->getArgument($name);

        return \is_scalar($value) ? (string) $value : '';
    }

    private function style(InputInterface $input, OutputInterface $output): SymfonyStyle
    {
        return new SymfonyStyle(
            $input,
            $this->diagnosticsOnStderr() && $output instanceof ConsoleOutputInterface
                ? $output->getErrorOutput()
                : $output,
        );
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Completion\CompletionInput;
use Symfony\Component\Console\Completion\CompletionSuggestions;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\BaseRefresh;
use Upkeep\Adapter\ProjectsRoot;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\BaseArtifact\BuildException;
use Upkeep\BaseArtifact\MetaException;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\Module;
use Upkeep\Cockpit\ModuleResolution;
use Upkeep\Cockpit\RegistryException;
use Upkeep\Drupal\DrupalOrgClient;
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

    /**
     * @param bool $required false where the command has a second, MR-less mode
     *                       to offer (check --working-copy), in which case the
     *                       command is responsible for refusing the empty case
     *                       itself — Console cannot express "one of these two"
     */
    protected function addMrArgument(bool $required = true): static
    {
        $this->addArgument(
            'mr',
            $required ? InputArgument::REQUIRED : InputArgument::OPTIONAL,
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

    /**
     * Opt out of bringing the base branch up to date before cutting from it.
     *
     * The default is to update, because not updating is what produced a green
     * local check and a red pipeline with no visible difference between them:
     * drupal.org's CI does not test your branch, it tests your branch merged
     * into the *current* tip of the target, and a working copy is cloned once
     * and then never fetched again. The flag is for working offline, and for
     * reproducing a verdict against the tree as it was.
     */
    protected function addNoUpdateOption(): static
    {
        $this->addOption(
            'no-update',
            null,
            InputOption::VALUE_NONE,
            'Do not fetch the base branch first; check against the working copy\'s base as it stands',
        );

        return $this;
    }

    /** The base-refresh mode this run asked for. */
    protected static function baseRefresh(InputInterface $input): BaseRefresh
    {
        return BaseRefresh::fromNoUpdateFlag($input->getOption('no-update') === true);
    }

    // ------------------------------------------------------------- resolution

    /**
     * Cockpit resolution: `--cockpit` > $UPKEEP_COCKPIT > cwd.
     *
     * @throws FilesystemException when the path is empty or unresolvable
     */
    /**
     * Shell completion for the two values every command shares: the module
     * machine name, and the core version behind `--version`.
     *
     * Command *names* complete already — Symfony Console registers that for
     * free. Argument values do not, and they are the tedious half: a machine
     * name is long, easy to mistype, and the thing every invocation starts
     * with. The suggestions come from the operator's own registry, so they are
     * exactly the modules they can act on.
     *
     * **Nothing here may throw.** This runs on every press of TAB, and an
     * exception would spill a stack trace across the prompt of somebody who
     * only wanted a module name. So a cockpit that cannot be resolved, or a
     * registry that will not parse, silently suggests nothing — the ordinary
     * command run a moment later reports it properly.
     */
    public function complete(CompletionInput $input, CompletionSuggestions $suggestions): void
    {
        parent::complete($input, $suggestions);

        $modules = $this->completableModules($input);

        if ($input->mustSuggestArgumentValuesFor('module')) {
            $suggestions->suggestValues(array_keys($modules));

            return;
        }

        if ($input->mustSuggestOptionValuesFor('version')) {
            $suggestions->suggestValues(self::completableCores($input, $modules));
        }
    }

    /**
     * @return array<string, Module> empty whenever anything at all is wrong
     */
    private function completableModules(CompletionInput $input): array
    {
        try {
            return $this->modules($this->cockpit($input));
        } catch (\Throwable) {
            return [];
        }
    }

    /**
     * The cores the module already on the command line tracks, so `--version=`
     * offers 10 and 11 rather than every version any module uses. With no
     * module named yet, every tracked core is fair game.
     *
     * @param array<string, Module> $modules
     *
     * @return list<string>
     */
    private static function completableCores(CompletionInput $input, array $modules): array
    {
        $named = $input->getArgument('module');
        if (\is_string($named) && isset($modules[$named])) {
            return $modules[$named]->coreVersions;
        }

        $cores = [];
        foreach ($modules as $module) {
            foreach ($module->coreVersions as $core) {
                $cores[$core] = $core;
            }
        }
        sort($cores);

        return $cores;
    }

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
     * A module the cockpit *watches*, for the survey commands that narrow
     * their own output to one of them.
     *
     * Still strict, and rightly: `dashboard widget` is a request to drill into
     * something the dashboard is showing, so a name it is not showing is a
     * mistake rather than a wider question.
     *
     * @param array<string, Module> $modules
     *
     * @throws WorkflowException when the module is not registered
     */
    protected static function requireModule(array $modules, string $name): Module
    {
        return MrContextResolver::requireModule($modules, $name);
    }

    /**
     * The module a *subject* command acts on, registered or not.
     *
     * The registry is a watchlist, not a gate: `project/<name>` is drupal.org's
     * convention and the cores a run can use are the ones base artifacts exist
     * for, so a module nobody has registered is workable. A registry entry
     * still wins where there is one — a maintainer's `core_versions` is a
     * deliberate statement and outranks anything inferred. See
     * `docs/any-module.md`.
     *
     * @param array<string, Module> $modules
     *
     * @throws RegistryException when the name cannot be a module, or nothing
     *                           has been built for it to run against
     */
    protected function resolveModule(Cockpit $cockpit, array $modules, string $name): Module
    {
        return ModuleResolution::resolve(
            $modules,
            $name,
            (new ArtifactLayout($cockpit->baseArtifactsPath()))->versionsOnDisk(),
        );
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

    /**
     * A value-taking option as an integer, or $default when it was not given.
     *
     * Reads the value exactly as an `(int)` cast does, which is what the
     * numeric flags have always done — the point here is that console input
     * is narrowed in one place, not that the parsing rule changes.
     */
    protected static function intOption(InputInterface $input, string $name, int $default = 0): int
    {
        $value = self::stringOption($input, $name);

        return $value !== null ? (int) $value : $default;
    }

    /** A required argument as a string. */
    protected static function stringArgument(InputInterface $input, string $name): string
    {
        $value = $input->getArgument($name);

        return \is_scalar($value) ? (string) $value : '';
    }

    /**
     * Surface what a drupal.org scan could not read.
     *
     * This client degrades by returning *less data*, which at the call site is
     * indistinguishable from there being less data — a dropped attachment
     * looks like an issue with fewer patches, a truncated page like a project
     * with fewer issues. So the shortfall is stated, and the counts printed
     * alongside it are named as the lower bounds they are.
     */
    protected static function reportScanWarnings(SymfonyStyle $io, DrupalOrgClient $drupal): void
    {
        $warnings = $drupal->warnings();
        if ($warnings === []) {
            return;
        }

        $shown = \array_slice($warnings, 0, 5);
        if (\count($warnings) > \count($shown)) {
            $shown[] = sprintf('... and %d more.', \count($warnings) - \count($shown));
        }
        $shown[] = 'Counts below are lower bounds: re-run to pick up what was missed.';

        $io->warning(array_merge(
            [sprintf('%d drupal.org request(s) did not answer.', \count($warnings))],
            $shown,
        ));
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

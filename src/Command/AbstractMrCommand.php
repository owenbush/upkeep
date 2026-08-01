<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Adapter\DdevContribAdapter;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\ProcessRunner;
use Upkeep\Adapter\ProjectsRoot;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Workflow\MrContext;
use Upkeep\Workflow\MrContextResolver;
use Upkeep\Workflow\WorkflowException;

/**
 * Shared wiring for the single-MR commands (check, review): argument/option
 * surface, cockpit + registry + GitLab client construction, MrContext
 * resolution, and engine-adapter construction.
 *
 * Deliberately untested orchestration — the meaningful logic (context
 * resolution outcomes, core-version defaulting, exit-code mapping) lives in
 * Upkeep\Workflow and is unit-tested there; the adapter operations are the
 * task-11 surface. These commands are the live verification layer.
 */
abstract class AbstractMrCommand extends Command
{
    public function __construct(private readonly ?EngineAdapterInterface $adapter = null)
    {
        parent::__construct();
    }

    /**
     * NOTE for application wiring: the --version option collides with
     * Symfony Console's built-in application-level --version/-V. The hosting
     * Application must drop that default option before running these
     * commands:
     *
     *   $definition = $application->getDefinition();
     *   $definition->setOptions(array_values(array_filter(
     *       $definition->getOptions(),
     *       static fn ($option) => $option->getName() !== 'version',
     *   )));
     */
    protected function configureMrSurface(): void
    {
        $this
            ->addArgument('module', InputArgument::REQUIRED, 'Registered module machine name (see `upkeep modules`)')
            ->addArgument('mr', InputArgument::REQUIRED, 'Merge request IID on the module\'s drupalcode project')
            ->addOption('version', null, InputOption::VALUE_REQUIRED, 'Target core major version; must be tracked by the module\'s registry entry. Defaults to the first core version listed there.')
            ->addOption('cockpit', null, InputOption::VALUE_REQUIRED, sprintf('Path to the cockpit directory (defaults to $%s, then the current directory)', Cockpit::ENV_VAR))
            ->addOption('projects-root', null, InputOption::VALUE_REQUIRED, sprintf('Directory holding the engine environments (defaults to $%s, then ~/.upkeep/projects)', ProjectsRoot::ENV_VAR));
    }

    /**
     * Resolves cockpit, registry, token, and MR context.
     *
     * @throws WorkflowException when any part of the context cannot be resolved
     */
    protected function resolveContext(InputInterface $input, SymfonyStyle $io): MrContext
    {
        $cockpit = Cockpit::resolve($input->getOption('cockpit'));
        $registry = $cockpit->loadRegistry();

        $tokens = new TokenResolver();
        $token = $tokens->resolve();
        if ($token === null) {
            throw new WorkflowException(sprintf(
                'No GitLab token found; MR resolution needs one. Configure one of: %s. (The token is never printed or logged.)',
                $tokens->describeSources(),
            ));
        }

        $iidRaw = (string) $input->getArgument('mr');
        if (preg_match('/^\d+$/', $iidRaw) !== 1) {
            throw new WorkflowException(sprintf('The <mr> argument must be a merge request IID (a positive integer), got "%s".', $iidRaw));
        }

        $io->writeln(sprintf('Resolving MR !%s of %s via GitLab ...', $iidRaw, $input->getArgument('module')));

        $version = $input->getOption('version');

        return (new MrContextResolver($registry->modules(), new GitlabClient(HttpClient::create(), $token)))
            ->resolve((string) $input->getArgument('module'), (int) $iidRaw, $version !== null ? (string) $version : null);
    }

    protected function cockpit(InputInterface $input): Cockpit
    {
        return Cockpit::resolve($input->getOption('cockpit'));
    }

    /**
     * The engine adapter, logging stage lines always and streamed process
     * output only in verbose mode (long steps still show one line per stage).
     */
    protected function adapter(InputInterface $input, OutputInterface $output): EngineAdapterInterface
    {
        if ($this->adapter !== null) {
            return $this->adapter;
        }

        $cockpit = $this->cockpit($input);
        $stageLog = static fn (string $line) => $output->writeln($line);
        $processLog = static fn (string $line) => $output->writeln($line, OutputInterface::VERBOSITY_VERBOSE);

        return new DdevContribAdapter(
            new ArtifactLayout($cockpit->baseArtifactsPath()),
            ProjectsRoot::resolve($input->getOption('projects-root')),
            new ProcessRunner($processLog),
            $stageLog,
        );
    }

    /**
     * One-line summary of the resolved context, printed before the long steps.
     */
    protected static function describeContext(SymfonyStyle $io, MrContext $context, ?string $requestedVersion): void
    {
        $mr = $context->mergeRequest;
        $io->writeln(sprintf(
            'MR !%d "%s" (%s -> %s) by %s, head %s',
            $mr->iid,
            $mr->title,
            $mr->sourceBranch,
            $mr->targetBranch,
            $mr->authorUsername,
            $mr->headSha ?? 'unknown',
        ));
        $io->writeln(sprintf(
            'Target: Drupal core %s%s, module %s',
            $context->coreMajor,
            $requestedVersion === null ? ' (default: first tracked core version in the registry)' : '',
            $context->module->name,
        ));
    }
}

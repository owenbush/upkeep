<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Adapter\EngineAdapterFactory;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Workflow\MrContext;
use Upkeep\Workflow\MrContextResolver;
use Upkeep\Workflow\WorkflowException;

/**
 * Shared wiring for the single-MR commands (check, review): argument/option
 * surface, cockpit + registry + GitLab client construction, and MrContext
 * resolution.
 *
 * Deliberately untested orchestration — the meaningful logic (context
 * resolution outcomes, core-version defaulting, exit-code mapping) lives in
 * Upkeep\Workflow and is unit-tested there; the adapter operations are the
 * task-11 surface. These commands are the live verification layer.
 */
abstract class AbstractMrCommand extends UpkeepCommand
{
    public function __construct(private readonly EngineAdapterFactory $engines)
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
        $this->addModuleArgument()
            ->addMrArgument()
            ->addTargetCoreOption()
            ->addCockpitOption()
            ->addProjectsRootOption();
    }

    /**
     * Resolves cockpit, registry, token, and MR context.
     *
     * @throws WorkflowException when any part of the context cannot be resolved
     */
    protected function resolveContext(InputInterface $input, SymfonyStyle $io): MrContext
    {
        $registry = $this->cockpit($input)->loadRegistry();

        $tokens = GitlabClientFactory::resolver($io);
        $token = $tokens->resolve();
        if ($token === null) {
            throw new WorkflowException(GitlabClientFactory::missingTokenMessage($tokens));
        }

        $iid = self::mrIid($input);
        $io->writeln(sprintf('Resolving MR !%d of %s via GitLab ...', $iid, self::stringArgument($input, 'module')));

        return (new MrContextResolver($registry->modules(), new GitlabClient(HttpClient::create(), $token)))
            ->resolve(
                self::stringArgument($input, 'module'),
                $iid,
                self::stringOption($input, 'version'),
            );
    }

    /**
     * The engine adapter for this invocation, logging stage lines always and
     * streamed process output only in verbose mode (long steps still show one
     * line per stage). The engine implementation comes from the injected
     * factory — no command names or chooses one.
     */
    protected function adapter(InputInterface $input, OutputInterface $output): EngineAdapterInterface
    {
        return $this->engines->create(
            $this->cockpit($input),
            self::stringOption($input, 'projects-root'),
            static fn (string $line) => $output->writeln($line),
            static fn (string $line) => $output->writeln($line, OutputInterface::VERBOSITY_VERBOSE),
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

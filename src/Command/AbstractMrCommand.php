<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\EngineAdapterFactory;
use Upkeep\BaseArtifact\ArtifactLayout;
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
 * The meaningful logic (context resolution outcomes, core-version defaulting,
 * exit-code mapping) lives in Upkeep\Workflow and is unit-tested there, and
 * the adapter operations are the task-11 surface; what is verified here is
 * the orchestration itself, end-to-end through the console against an
 * injected GitLab client and a fake engine.
 */
abstract class AbstractMrCommand extends UpkeepCommand
{
    /**
     * @param ?GitlabClient $gitlabClient injected in tests; built from the
     *                                    resolved token otherwise (the same
     *                                    seam every other GitLab-using
     *                                    command already exposes)
     */
    public function __construct(
        private readonly EngineAdapterFactory $engines,
        private readonly ?GitlabClient $gitlabClient = null,
    ) {
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
    protected function configureMrSurface(bool $mrRequired = true): void
    {
        $this->addModuleArgument()
            ->addMrArgument($mrRequired)
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
        $cockpit = $this->cockpit($input);
        $registry = $cockpit->loadRegistry();
        $client = $this->gitlabClient ?? $this->clientFromToken($io);

        $iid = self::mrIid($input);
        $io->writeln(sprintf('Resolving MR !%d of %s via GitLab ...', $iid, self::stringArgument($input, 'module')));

        // The disk answers for a module the registry does not carry — the
        // merge-request path is a subject command like any other, and gating
        // it on the watchlist was the half of the split that got missed.
        return (new MrContextResolver(
            $registry->modules(),
            $client,
            (new ArtifactLayout($cockpit->baseArtifactsPath()))->versionsOnDisk(),
        ))
            ->resolve(
                self::stringArgument($input, 'module'),
                $iid,
                self::stringOption($input, 'version'),
            );
    }

    /**
     * No credential means no verdict can be produced, which is the
     * infrastructure outcome — raised here, with the one shared wording, so
     * the base's exception mapping turns it into exit 2 like every other
     * infrastructure failure. The factory's own reporter is therefore a no-op:
     * the guidance travels in the exception rather than being printed twice.
     *
     * @throws WorkflowException when no token is configured
     */
    private function clientFromToken(SymfonyStyle $io): GitlabClient
    {
        $tokens = GitlabClientFactory::resolver($io);

        // A command that only looks does not need a credential: drupalcode
        // serves public projects' merge requests and refs anonymously, and
        // requiring a token to read was upkeep's restriction rather than
        // GitLab's. Writes stay credentialed, and the client refuses them
        // structurally when there is none.
        if ($this->readsOnly()) {
            return GitlabClientFactory::readOnly($tokens, static function (string $note) use ($io): void {
                $io->note($note);
            });
        }

        $client = GitlabClientFactory::authenticated($tokens, static function (): void {
        });

        return $client ?? throw new WorkflowException(GitlabClientFactory::missingTokenMessage($tokens));
    }

    /**
     * Whether this command only reads.
     *
     * Declared rather than inferred, and defaulting to false: a command that
     * writes and forgets to say so gets the strict path, which refuses early
     * with the token guidance. The other way round would let a write reach
     * GitLab with no credential and fail four frames down.
     */
    protected function readsOnly(): bool
    {
        return false;
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
            ...self::liveProcessOutput($output),
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

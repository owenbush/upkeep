<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Question\ChoiceQuestion;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Cockpit\Module;
use Upkeep\Cockpit\RegistryEditor;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\WorkflowException;

/**
 * Registers maintained modules into the cockpit registry from the token
 * holder's GitLab project memberships — list what you maintain, opt in,
 * done. Read-only against GitLab; writes only registry.yml.
 *
 * Thin wiring over tested parts (GitlabClient::membershipProjects,
 * RegistryEditor); covered by CommandTester scenarios, not unit tests.
 */
#[AsCommand(
    name: 'modules:add',
    description: 'Register maintained modules from your git.drupalcode.org project memberships (interactive opt-in).',
)]
final class ModulesAddCommand extends UpkeepCommand
{
    public function __construct(
        private readonly ?GitlabClient $client = null,
        private readonly ?TokenResolver $tokenResolver = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this
            ->addArgument(
                'modules',
                InputArgument::IS_ARRAY,
                'Module machine names to register without prompting (must be among your memberships)',
            )
            ->addOption(
                'core-versions',
                null,
                InputOption::VALUE_REQUIRED,
                'Comma-separated core majors the new entries track (e.g. "10,11")',
                '11',
            );
        $this->addCockpitOption();
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->cockpit($input);
        $registered = $this->modules($cockpit);

        // The factory reports the missing-token guidance itself; memberships
        // cannot be listed without a credential.
        $client = $this->buildClient($io);
        if ($client === null) {
            return ExitCode::INFRASTRUCTURE;
        }

        $projects = $client->membershipProjects();
        if ($projects instanceof ApiFailure) {
            throw new WorkflowException(sprintf(
                'Could not list your project memberships [%s]: %s',
                $projects->shortCode(),
                $projects->message,
            ));
        }

        // Contrib modules live under project/; the machine name is the path.
        $candidates = [];
        foreach ($projects as $project) {
            if (str_starts_with($project->pathWithNamespace, 'project/') && !isset($registered[$project->path])) {
                $candidates[$project->path] = $project;
            }
        }

        if ($candidates === []) {
            $io->success('Every project/ membership is already registered — nothing to add.');

            return ExitCode::OK;
        }

        $coreVersions = array_values(array_filter(array_map(
            trim(...),
            explode(',', self::stringOption($input, 'core-versions') ?? ''),
        )));
        if ($coreVersions === []) {
            throw new WorkflowException('--core-versions must name at least one core major, e.g. "11" or "10,11".');
        }

        /** @var list<string> $requested */
        $requested = $input->getArgument('modules');
        if ($requested !== []) {
            $unknown = array_diff($requested, array_keys($candidates), array_keys($registered));
            if ($unknown !== []) {
                throw new WorkflowException(sprintf(
                    'Not among your project/ memberships: %s. Run without arguments to pick interactively.',
                    implode(', ', $unknown),
                ));
            }
            $chosen = array_values(array_intersect($requested, array_keys($candidates)));
        } elseif (!$input->isInteractive()) {
            throw new WorkflowException(
                'Pass module machine names as arguments when running non-interactive (no prompt available). '
                . 'Example: upkeep modules:add token_or field_helper',
            );
        } else {
            $question = new ChoiceQuestion(
                sprintf(
                    'Which modules should be registered? (comma-separated; %d unregistered membership(s) found)',
                    \count($candidates),
                ),
                array_keys($candidates),
            );
            $question->setMultiselect(true);
            /** @var list<string> $chosen */
            $chosen = (array) $io->askQuestion($question);
        }

        if ($chosen === []) {
            $io->writeln('Nothing selected; registry unchanged.');

            return ExitCode::OK;
        }

        // A registry that was not written must never be reported as
        // "Registered N module(s)": both failure modes propagate to the base,
        // which reports them as an infrastructure failure.
        $added = (new RegistryEditor($cockpit->registryPath()))->add(array_map(
            static fn (string $name): Module => new Module(
                $name,
                $candidates[$name]->pathWithNamespace,
                $coreVersions,
            ),
            $chosen,
        ));

        $io->success(sprintf(
            'Registered %d module(s) tracking core %s: %s',
            \count($added),
            implode(', ', $coreVersions),
            implode(', ', $added),
        ));
        $io->writeln('Run `upkeep dashboard` to see their open merge requests.');

        return ExitCode::OK;
    }

    private function buildClient(SymfonyStyle $io): ?GitlabClient
    {
        if ($this->client !== null) {
            return $this->client;
        }

        return $this->tokenResolver === null
            ? GitlabClientFactory::forConsole($io)
            : GitlabClientFactory::fromResolvedToken($this->tokenResolver, $io);
    }
}

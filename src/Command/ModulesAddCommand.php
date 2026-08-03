<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Question\ChoiceQuestion;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\Module;
use Upkeep\Cockpit\RegistryEditor;
use Upkeep\Cockpit\RegistryException;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\Project;
use Upkeep\Gitlab\TokenResolver;

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
final class ModulesAddCommand extends Command
{
    public function __construct(private readonly ?GitlabClient $client = null)
    {
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
                'cockpit',
                null,
                InputOption::VALUE_REQUIRED,
                sprintf(
                    'Path to the cockpit directory (defaults to $%s, then the current directory)',
                    Cockpit::ENV_VAR,
                ),
            )
            ->addOption(
                'core-versions',
                null,
                InputOption::VALUE_REQUIRED,
                'Comma-separated core majors the new entries track (e.g. "10,11")',
                '11',
            );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        try {
            $cockpit = Cockpit::resolve($input->getOption('cockpit'));
            $registered = $cockpit->loadRegistry()->modules();
        } catch (RegistryException $e) {
            $io->error($e->getMessage());

            return Command::FAILURE;
        }

        $projects = $this->buildClient($io)?->membershipProjects();
        if ($projects === null) {
            return Command::FAILURE;
        }
        if ($projects instanceof ApiFailure) {
            $io->error('Could not list your project memberships: ' . $projects->message());

            return Command::FAILURE;
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

            return Command::SUCCESS;
        }

        $coreVersions = array_values(array_filter(array_map(
            trim(...),
            explode(',', (string) $input->getOption('core-versions')),
        )));
        if ($coreVersions === []) {
            $io->error('--core-versions must name at least one core major, e.g. "11" or "10,11".');

            return Command::FAILURE;
        }

        /** @var list<string> $requested */
        $requested = $input->getArgument('modules');
        if ($requested !== []) {
            $unknown = array_diff($requested, array_keys($candidates), array_keys($registered));
            if ($unknown !== []) {
                $io->error(sprintf(
                    'Not among your project/ memberships: %s. Run without arguments to pick interactively.',
                    implode(', ', $unknown),
                ));

                return Command::FAILURE;
            }
            $chosen = array_values(array_intersect($requested, array_keys($candidates)));
        } elseif (!$input->isInteractive()) {
            $io->error(
                'Pass module machine names as arguments when running non-interactive (no prompt available). '
                . 'Example: upkeep modules:add token_or field_helper',
            );

            return Command::FAILURE;
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

            return Command::SUCCESS;
        }

        try {
            $added = (new RegistryEditor($cockpit->registryPath()))->add(array_map(
                static fn (string $name): Module => new Module(
                    $name,
                    $candidates[$name]->pathWithNamespace,
                    $coreVersions,
                ),
                $chosen,
            ));
        } catch (RegistryException $e) {
            $io->error($e->getMessage());

            return Command::FAILURE;
        }

        $io->success(sprintf(
            'Registered %d module(s) tracking core %s: %s',
            \count($added),
            implode(', ', $coreVersions),
            implode(', ', $added),
        ));
        $io->writeln('Run `upkeep dashboard` to see their open merge requests.');

        return Command::SUCCESS;
    }

    private function buildClient(SymfonyStyle $io): ?GitlabClient
    {
        if ($this->client !== null) {
            return $this->client;
        }

        $token = (new TokenResolver())->resolve();
        if ($token === null) {
            $io->error(sprintf(
                'No GitLab token found. Configure one of: env var %s, config file %s.',
                TokenResolver::ENV_VAR,
                TokenResolver::CONFIG_PATH_HINT,
            ));

            return null;
        }

        return new GitlabClient(HttpClient::create(), $token);
    }
}

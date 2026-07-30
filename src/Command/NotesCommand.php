<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\RegistryException;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Notes\NotesGenerator;

/**
 * Draft release notes for a module: what merged since its last tag, as
 * paste-ready Markdown on stdout.
 *
 * Thin wiring, deliberately untested: all meaningful behavior — latest-tag
 * selection, bot/human grouping, Markdown formatting, and both edge cases —
 * lives in NotesGenerator (unit-tested with mocked client data); fetching and
 * typed failures live in GitlabClient (also unit-tested). This command only
 * resolves the module, fetches, and prints, and is verified live.
 *
 * Module resolution mirrors api:probe with one addition: when a cockpit
 * registry is available and knows the module, its project path wins;
 * otherwise the argument is treated as the project path directly.
 *
 * Strictly read-only: only GET-backed client methods (project/tags/
 * mergedSince) are used, and tagging/release cutting stays manual — this
 * command has no tag or release flags and never will.
 */
#[AsCommand(
    name: 'notes',
    description: 'Draft paste-ready Markdown release notes: merged MRs since the module\'s last tag.',
)]
final class NotesCommand extends Command
{
    protected function configure(): void
    {
        $this->addArgument(
            'module',
            InputArgument::REQUIRED,
            'Module machine name (resolved via the cockpit registry when available) or full project path (e.g. project/conditions_helper)',
        );
        $this->addOption(
            'cockpit',
            null,
            InputOption::VALUE_REQUIRED,
            sprintf('Path to the cockpit directory (defaults to $%s, then the current directory)', Cockpit::ENV_VAR),
        );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        // Route diagnostics to stderr so stdout carries only the paste-ready
        // Markdown (safe to pipe or redirect).
        $io = new SymfonyStyle(
            $input,
            $output instanceof ConsoleOutputInterface ? $output->getErrorOutput() : $output,
        );
        $module = (string) $input->getArgument('module');
        $projectPath = $this->resolveProjectPath($module, $input->getOption('cockpit'));

        $resolver = new TokenResolver();
        $token = $resolver->resolve();
        if ($token === null) {
            $io->error(sprintf(
                'No GitLab token found. Configure one of: %s. (The token is never printed or logged.)',
                $resolver->describeSources(),
            ));

            return Command::FAILURE;
        }

        $client = new GitlabClient(HttpClient::create(), $token);

        $project = $client->project($projectPath);
        if ($project instanceof ApiFailure) {
            $io->error(sprintf('Project lookup failed [%s]: %s', $this->failureName($project), $project->message));

            return Command::FAILURE;
        }

        $tags = $client->tags($project);
        if ($tags instanceof ApiFailure) {
            $io->error(sprintf('Tag list failed [%s]: %s', $this->failureName($tags), $tags->message));

            return Command::FAILURE;
        }

        $latestTag = NotesGenerator::latestTag($tags);
        // Tagless module: the full merged history is everything since epoch.
        $since = $latestTag?->createdAt ?? new \DateTimeImmutable('@0');

        $merged = $client->mergedSince($project, $since);
        if ($merged instanceof ApiFailure) {
            $io->error(sprintf('Merged-MR list failed [%s]: %s', $this->failureName($merged), $merged->message));

            return Command::FAILURE;
        }

        $markdown = (new NotesGenerator())->generate($module, $latestTag, $merged->all());
        // Raw write: the Markdown must reach stdout byte-for-byte paste-ready,
        // untouched by the console formatter.
        $output->writeln($markdown, OutputInterface::OUTPUT_RAW);

        return Command::SUCCESS;
    }

    /**
     * Prefer the cockpit registry's project path for the module; fall back to
     * treating the argument as the project path itself (api:probe behavior)
     * when no usable registry exists or the module is not registered.
     */
    private function resolveProjectPath(string $module, ?string $cockpitOption): string
    {
        try {
            $registry = Cockpit::resolve($cockpitOption)->loadRegistry();
        } catch (RegistryException) {
            return $module;
        }

        return ($registry->modules()[$module] ?? null)?->project ?? $module;
    }

    private function failureName(ApiFailure $failure): string
    {
        return (new \ReflectionClass($failure))->getShortName();
    }
}

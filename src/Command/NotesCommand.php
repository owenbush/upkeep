<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\RegistryException;
use Upkeep\Filesystem\FilesystemException;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Notes\NotesGenerator;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\WorkflowException;

/**
 * Draft release notes for a module: what merged since its last tag, as
 * paste-ready Markdown on stdout.
 *
 * Thin wiring: all meaningful behavior — latest-tag selection, bot/human
 * grouping, Markdown formatting, and both edge cases — lives in
 * NotesGenerator (unit-tested with mocked client data); fetching and typed
 * failures live in GitlabClient (also unit-tested). This command only
 * resolves the module, fetches, and prints, and is verified live. The wiring
 * itself is pinned by one hermetic no-token run (NotesCommandTest), because
 * "verified live" is not a thing CI does: this class once shipped with an
 * unimported GitlabClientFactory and fataled on every invocation.
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
final class NotesCommand extends UpkeepCommand
{
    protected function configure(): void
    {
        $this->addArgument(
            'module',
            InputArgument::REQUIRED,
            'Module machine name (resolved via the cockpit registry when available) or full project path (e.g. '
            . 'project/conditions_helper)',
        );
        $this->addCockpitOption();
    }

    /** stdout carries only the paste-ready Markdown. */
    protected function diagnosticsOnStderr(): bool
    {
        return true;
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $module = self::stringArgument($input, 'module');
        $projectPath = $this->resolveProjectPath($module, self::stringOption($input, 'cockpit'));

        // The factory reports the missing-token guidance itself (one wording
        // for the whole CLI); "no credential" is an infrastructure failure.
        $client = GitlabClientFactory::forConsole($io);
        if ($client === null) {
            return ExitCode::INFRASTRUCTURE;
        }

        $project = $client->project($projectPath);
        if ($project instanceof ApiFailure) {
            throw new WorkflowException(
                sprintf('Project lookup failed [%s]: %s', $project->shortCode(), $project->message),
            );
        }

        $tags = $client->tags($project);
        if ($tags instanceof ApiFailure) {
            throw new WorkflowException(sprintf('Tag list failed [%s]: %s', $tags->shortCode(), $tags->message));
        }

        $latestTag = NotesGenerator::latestTag($tags);
        // Tagless module — or a tag GitLab reports without a creation date:
        // the full merged history is everything since epoch. `??` reads with
        // isset() semantics, so a null $latestTag falls through here too.
        $since = $latestTag->createdAt ?? new \DateTimeImmutable('@0');

        $merged = $client->mergedSince($project, $since);
        if ($merged instanceof ApiFailure) {
            throw new WorkflowException(
                sprintf('Merged-MR list failed [%s]: %s', $merged->shortCode(), $merged->message),
            );
        }

        $markdown = (new NotesGenerator())->generate($module, $latestTag, $merged->all());
        // Raw write: the Markdown must reach stdout byte-for-byte paste-ready,
        // untouched by the console formatter.
        $output->writeln($markdown, OutputInterface::OUTPUT_RAW);

        return ExitCode::OK;
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
        } catch (RegistryException | FilesystemException) {
            // Deliberate fallback (the one place a missing registry is not an
            // error): `upkeep notes` accepts a bare project path, so with no
            // usable cockpit the argument simply IS the project path.
            return $module;
        }

        $registered = $registry->modules()[$module] ?? null;

        return $registered !== null ? $registered->project : $module;
    }
}

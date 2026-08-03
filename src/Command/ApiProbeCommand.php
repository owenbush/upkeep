<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\WorkflowException;

/**
 * Thin debug command: probe the git.drupalcode.org API for one module.
 *
 * Deliberately untested wiring — all meaningful behavior (parsing, typed
 * failures, memoization, rate-limit handling) lives in GitlabClient, which is
 * covered by unit tests. This command is the live verification surface.
 *
 * Takes the module name directly (or a full "namespace/path"); it does not
 * consult the cockpit registry — later orchestrator commands do that.
 *
 * The PAT comes from TokenResolver (UPKEEP_GITLAB_TOKEN env var, else
 * ~/.config/upkeep/drupal-pat) and is never printed.
 *
 * This command performs read-only GETs; it never calls the merge endpoint.
 */
#[AsCommand(
    name: 'api:probe',
    description: 'Probe the git.drupalcode.org GitLab API for a module: open MRs and head pipeline status.',
)]
final class ApiProbeCommand extends UpkeepCommand
{
    protected function configure(): void
    {
        $this->addArgument(
            'module',
            InputArgument::REQUIRED,
            'Module machine name (e.g. conditions_helper) or full project path (e.g. project/conditions_helper)',
        );
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $module = self::stringArgument($input, 'module');

        // The factory reports the missing-token guidance itself (one wording
        // for the whole CLI); "no credential" is an infrastructure failure.
        $client = GitlabClientFactory::forConsole($io);
        if ($client === null) {
            return ExitCode::INFRASTRUCTURE;
        }

        $project = $client->project($module);
        if ($project instanceof ApiFailure) {
            throw new WorkflowException(
                sprintf('Project lookup failed [%s]: %s', $project->shortCode(), $project->message),
            );
        }
        $io->title(sprintf('%s (project id %d)', $project->pathWithNamespace, $project->id));

        $open = $client->openMergeRequests($project);
        if ($open instanceof ApiFailure) {
            throw new WorkflowException(sprintf('MR list failed [%s]: %s', $open->shortCode(), $open->message));
        }
        $io->writeln(sprintf('Open merge requests: <info>%d</info>', \count($open)));

        $first = $open->first();
        if ($first === null) {
            $io->writeln('No open MR to inspect further.');

            return ExitCode::OK;
        }

        // Re-fetch the single MR: only that endpoint carries head_pipeline.
        $detailed = $client->mergeRequest($project, $first->iid);
        $mr = $detailed instanceof MergeRequest ? $detailed : $first;

        $io->section(sprintf('MR !%d', $mr->iid));
        $io->definitionList(
            ['IID' => (string) $mr->iid],
            ['Title' => $mr->title],
            ['Author' => $mr->authorUsername . ($mr->authorId !== null ? sprintf(' (id %d)', $mr->authorId) : '')],
            ['Source branch' => $mr->sourceBranch],
            ['Draft' => $mr->draft ? 'yes' : 'no'],
            ['Detailed merge status' => $mr->detailedMergeStatus ?? 'n/a'],
            ['Head SHA' => $mr->headSha ?? 'n/a'],
            ['URL' => $mr->webUrl],
        );

        if ($detailed instanceof ApiFailure) {
            $io->warning(sprintf(
                'Single-MR fetch failed [%s]: %s (pipeline status unavailable)',
                $detailed->shortCode(),
                $detailed->message,
            ));

            return ExitCode::OK;
        }

        if ($mr->headPipeline === null) {
            $io->writeln('Head pipeline: <comment>none</comment>');
        } else {
            $io->writeln(sprintf(
                'Head pipeline: <info>%s</info> (#%d) %s',
                $mr->headPipeline->status->value,
                $mr->headPipeline->id,
                $mr->headPipeline->webUrl,
            ));
        }

        return ExitCode::OK;
    }
}

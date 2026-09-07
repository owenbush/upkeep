<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\IssueReference;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\WorkflowException;

/**
 * Show the drupal.org issue linked to a merge request and open it in the
 * browser — the quickest path to changing the issue status (RTBC, needs
 * work, etc.) since the drupal.org API is read-only.
 */
#[AsCommand(
    name: 'issue',
    description: 'Show and open the drupal.org issue linked to a merge request.',
)]
final class IssueCommand extends UpkeepCommand
{
    public function __construct(
        private readonly ?GitlabClient $gitlabClient = null,
        private readonly ?DrupalOrgClient $drupalClient = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addModuleArgument()
            ->addMrArgument()
            ->addNoOpenOption()
            ->addCockpitOption();
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->cockpit($input);
        $modules = $this->modules($cockpit);
        $module = $this->resolveModule($cockpit, $modules, self::stringArgument($input, 'module'));
        $iid = self::mrIid($input);

        $gitlab = $this->gitlabClient ?? GitlabClientFactory::forConsole($io);
        if ($gitlab === null) {
            return ExitCode::INFRASTRUCTURE;
        }

        $project = $gitlab->project($module->project);
        if ($project instanceof ApiFailure) {
            throw new WorkflowException(
                sprintf('Could not resolve project [%s]: %s', $project->shortCode(), $project->message),
            );
        }

        $mr = $gitlab->mergeRequest($project, $iid);
        if ($mr instanceof ApiFailure) {
            throw new WorkflowException(
                sprintf('Could not fetch MR !%d [%s]: %s', $iid, $mr->shortCode(), $mr->message),
            );
        }

        $nid = IssueReference::extract($mr->title, $mr->sourceBranch, $mr->description);
        if ($nid === null) {
            throw new WorkflowException(sprintf(
                'No issue number found in MR !%d. Checked title ("%s") and branch ("%s") — neither contains an '
                . 'issue reference.',
                $iid,
                $mr->title,
                $mr->sourceBranch,
            ));
        }

        $issueUrl = IssueReference::issueUrl($nid);

        $drupal = $this->drupalClient ?? new DrupalOrgClient(HttpClient::create());
        $issue = $drupal->issue($nid);

        $io->section(sprintf('Issue #%d', $nid));

        if ($issue !== null) {
            $io->writeln(sprintf('  Title:      %s', $issue->title));
            $io->writeln(sprintf('  Status:     <options=bold>%s</>', $issue->status->label()));
            if ($issue->priorityLabel() !== null) {
                $io->writeln(sprintf('  Priority:   %s', $issue->priorityLabel()));
            }
            if ($issue->category !== null) {
                $io->writeln(sprintf('  Category:   %s', $issue->category));
            }
            if ($issue->version !== null) {
                $io->writeln(sprintf('  Version:    %s', $issue->version));
            }
            if ($issue->component !== null) {
                $io->writeln(sprintf('  Component:  %s', $issue->component));
            }
            $io->writeln(sprintf('  URL:        %s', $issue->url));
            $issueUrl = $issue->url;
        } else {
            $io->writeln(sprintf('  URL:        %s', $issueUrl));
            $io->writeln('  (Could not fetch issue details from drupal.org — the URL is still valid.)');
        }

        $io->writeln(sprintf('  MR:         !%d "%s"', $mr->iid, $mr->title));
        $io->newLine();

        if (!$input->getOption('no-open')) {
            if (BrowserOpener::open($issueUrl)) {
                $io->success('Opened in browser. Change the status on the drupal.org issue page.');
            } else {
                $io->writeln(sprintf('Could not open browser automatically. Visit: %s', $issueUrl));
            }
        }

        return ExitCode::OK;
    }
}

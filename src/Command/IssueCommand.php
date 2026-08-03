<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Symfony\Component\Process\Process;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\IssueReference;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\TokenResolver;

/**
 * Show the drupal.org issue linked to a merge request and open it in the
 * browser — the quickest path to changing the issue status (RTBC, needs
 * work, etc.) since the drupal.org API is read-only.
 */
#[AsCommand(
    name: 'issue',
    description: 'Show and open the drupal.org issue linked to a merge request.',
)]
final class IssueCommand extends Command
{
    public function __construct(
        private readonly ?GitlabClient $gitlabClient = null,
        private readonly ?DrupalOrgClient $drupalClient = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this
            ->addArgument('module', InputArgument::REQUIRED, 'Registered module machine name')
            ->addArgument('mr', InputArgument::REQUIRED, 'Merge request IID')
            ->addOption('no-open', null, InputOption::VALUE_NONE, 'Show issue details without opening the browser')
            ->addOption(
                'cockpit',
                null,
                InputOption::VALUE_REQUIRED,
                sprintf(
                    'Path to the cockpit directory (defaults to $%s, then the current directory)',
                    Cockpit::ENV_VAR,
                ),
            );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        $cockpit = Cockpit::resolve($input->getOption('cockpit'));
        $modules = $cockpit->loadRegistry()->modules();

        $name = (string) $input->getArgument('module');
        if (!isset($modules[$name])) {
            $io->error(sprintf('Module "%s" is not registered.', $name));

            return Command::FAILURE;
        }

        $gitlab = $this->gitlabClient ?? $this->buildGitlabClient($io);
        if ($gitlab === null) {
            return Command::FAILURE;
        }

        $module = $modules[$name];
        $project = $gitlab->project($module->project);
        if ($project instanceof ApiFailure) {
            $io->error('Could not resolve project: ' . $project->message);

            return Command::FAILURE;
        }

        $iid = (int) $input->getArgument('mr');
        $mr = $gitlab->mergeRequest($project, $iid);
        if ($mr instanceof ApiFailure) {
            $io->error(sprintf('Could not fetch MR !%d: %s', $iid, $mr->message));

            return Command::FAILURE;
        }

        $nid = IssueReference::extract($mr->title, $mr->sourceBranch, $mr->description);
        if ($nid === null) {
            $io->error(sprintf(
                'No issue number found in MR !%d. Checked title ("%s") and branch ("%s") — neither contains an '
                . 'issue reference.',
                $iid,
                $mr->title,
                $mr->sourceBranch,
            ));

            return Command::FAILURE;
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
            $opener = \PHP_OS_FAMILY === 'Darwin' ? 'open' : 'xdg-open';
            $process = new Process([$opener, $issueUrl]);
            $process->run();

            if ($process->isSuccessful()) {
                $io->success('Opened in browser. Change the status on the drupal.org issue page.');
            } else {
                $io->writeln(sprintf('Could not open browser automatically. Visit: %s', $issueUrl));
            }
        }

        return Command::SUCCESS;
    }

    private function buildGitlabClient(SymfonyStyle $io): ?GitlabClient
    {
        $token = (new TokenResolver())->resolve();
        if ($token === null) {
            $io->error(sprintf(
                'No GitLab token found. Configure one of: env var %s, config file %s.',
                TokenResolver::DEFAULT_ENV_VAR,
                TokenResolver::defaultConfigFile(),
            ));

            return null;
        }

        return new GitlabClient(HttpClient::create(), $token);
    }
}

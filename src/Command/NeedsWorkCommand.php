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
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Drupal\IssueReference;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Results\CachedResult;
use Upkeep\Results\ResultsCache;
use Upkeep\Workflow\MrContextResolver;
use Upkeep\Workflow\WorkflowException;

/**
 * Post a structured check-results comment on a GitLab MR and open the
 * linked drupal.org issue for a manual status change to "Needs work".
 */
#[AsCommand(
    name: 'needs-work',
    description: 'Post local check results as a comment on the merge request.',
)]
final class NeedsWorkCommand extends Command
{
    private const EXCERPT_BYTES = 2000;

    public function __construct(
        private readonly ?GitlabClient $gitlabClient = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this
            ->addArgument('module', InputArgument::REQUIRED, 'Registered module machine name')
            ->addArgument('mr', InputArgument::REQUIRED, 'Merge request IID')
            ->addOption(
                'version',
                null,
                InputOption::VALUE_REQUIRED,
                'Target core major version (defaults to first tracked version)',
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
            ->addOption('no-open', null, InputOption::VALUE_NONE, 'Do not open the drupal.org issue in the browser')
            ->addOption('dry-run', null, InputOption::VALUE_NONE, 'Print the comment without posting it');
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        $cockpit = Cockpit::resolve($input->getOption('cockpit'));
        $modules = $cockpit->loadRegistry()->modules();

        $gitlab = $this->gitlabClient ?? $this->buildGitlabClient($io);
        if ($gitlab === null) {
            return Command::FAILURE;
        }

        $iidRaw = (string) $input->getArgument('mr');
        if (!preg_match('/^\d+$/', $iidRaw) || (int) $iidRaw < 1) {
            $io->error(sprintf('"%s" is not a valid merge request IID.', $iidRaw));

            return Command::FAILURE;
        }

        try {
            $resolver = new MrContextResolver($modules, $gitlab);
            $context = $resolver->resolve(
                (string) $input->getArgument('module'),
                (int) $iidRaw,
                $input->getOption('version'),
            );
        } catch (WorkflowException $e) {
            $io->error($e->getMessage());

            return Command::FAILURE;
        }

        $mr = $context->mergeRequest;
        $module = $context->module;
        $coreMajor = $context->coreMajor;

        $cache = new ResultsCache($cockpit->root . '/results');
        $cached = $mr->headSha !== null
            ? $cache->find($module->name, $mr->iid, $coreMajor, $mr->headSha)
            : null;

        $stale = false;
        if ($cached === null) {
            $cached = $cache->latest($module->name, $mr->iid, $coreMajor);
            if ($cached !== null && $mr->headSha !== null && $cached->sha !== $mr->headSha) {
                $stale = true;
            }
        }

        if ($cached === null) {
            $io->error(sprintf(
                'No cached check results for %s !%d (core %s). Run `upkeep check %s %d --version=%s` first.',
                $module->name,
                $mr->iid,
                $coreMajor,
                $module->name,
                $mr->iid,
                $coreMajor,
            ));

            return Command::FAILURE;
        }

        if ($stale) {
            $io->warning(sprintf(
                'Results were recorded against SHA %s; the MR is now at %s. The comment will note the older SHA.',
                substr($cached->sha, 0, 8),
                substr((string) $mr->headSha, 0, 8),
            ));
        }

        $comment = $this->formatComment($cached, $coreMajor);

        if ($input->getOption('dry-run')) {
            $io->section('Comment preview');
            $io->writeln($comment);

            return Command::SUCCESS;
        }

        $result = $gitlab->postNote($context->project, $mr->iid, $comment);
        if ($result instanceof ApiFailure) {
            $io->error(sprintf('Could not post comment on !%d: %s', $mr->iid, $result->message));

            return Command::FAILURE;
        }

        $io->success(sprintf('Comment posted on !%d — %s', $mr->iid, $mr->webUrl));

        $nid = IssueReference::extract($mr->title, $mr->sourceBranch, $mr->description);
        if ($nid !== null && !$input->getOption('no-open')) {
            $issueUrl = IssueReference::issueUrl($nid);
            $opener = \PHP_OS_FAMILY === 'Darwin' ? 'open' : 'xdg-open';
            $process = new Process([$opener, $issueUrl]);
            $process->run();

            if ($process->isSuccessful()) {
                $io->writeln(sprintf('Opened drupal.org issue #%d — set the status to Needs work.', $nid));
            } else {
                $io->writeln(sprintf('Set the issue status at: %s', $issueUrl));
            }
        }

        return Command::SUCCESS;
    }

    private function formatComment(CachedResult $cached, string $coreMajor): string
    {
        $recorded = $cached->recordedAt->format('Y-m-d H:i') . ' UTC';
        $sha = substr($cached->sha, 0, 8);

        $lines = [];
        $lines[] = '### upkeep local check results';
        $lines[] = '';
        $lines[] = sprintf('Core: %s · SHA: `%s` · Recorded: %s', $coreMajor, $sha, $recorded);
        $lines[] = '';
        $lines[] = '| Check | Status | Duration |';
        $lines[] = '|-------|--------|----------|';

        $failedChecks = [];
        foreach ($cached->result->results as $check) {
            $status = match ($check->status) {
                CheckStatus::Failed => '**FAIL**',
                CheckStatus::Passed => 'pass',
                CheckStatus::NoTests => 'no tests',
                CheckStatus::Unavailable => 'unavailable',
            };
            $duration = $check->durationSeconds > 0
                ? sprintf('%.1fs', $check->durationSeconds)
                : '—';
            $lines[] = sprintf('| %s | %s | %s |', $check->type->value, $status, $duration);

            if (!$check->passed() && $check->status === CheckStatus::Failed) {
                $failedChecks[] = $check;
            }
        }

        foreach ($failedChecks as $check) {
            $excerpt = $this->excerptOutput($check->output);
            if ($excerpt !== '') {
                $lines[] = '';
                $lines[] = sprintf('<details><summary>%s output</summary>', $check->type->value);
                $lines[] = '';
                $lines[] = '```';
                $lines[] = $excerpt;
                $lines[] = '```';
                $lines[] = '';
                $lines[] = '</details>';
            }
        }

        $lines[] = '';
        $lines[] = '---';
        $lines[] = '*Posted via [upkeep](https://github.com/owenbush/upkeep)*';

        return implode("\n", $lines);
    }

    private function excerptOutput(string $output): string
    {
        $output = trim($output);
        if ($output === '') {
            return '';
        }

        if (\strlen($output) > self::EXCERPT_BYTES) {
            return substr($output, -self::EXCERPT_BYTES);
        }

        return $output;
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

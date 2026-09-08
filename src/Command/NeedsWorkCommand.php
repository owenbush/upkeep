<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\CheckStatus;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\Drupal\IssueReference;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Results\CachedResult;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;
use Upkeep\Workflow\ExitCode;
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
final class NeedsWorkCommand extends UpkeepCommand
{
    public function __construct(
        private readonly ?GitlabClient $gitlabClient = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addModuleArgument()
            ->addMrArgument()
            ->addTargetCoreOption()
            ->addCockpitOption()
            ->addNoOpenOption()
            ->addOption('dry-run', null, InputOption::VALUE_NONE, 'Print the comment without posting it');
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->cockpit($input);
        $modules = $this->modules($cockpit);
        $iid = self::mrIid($input);

        $gitlab = $this->gitlabClient ?? GitlabClientFactory::forConsole($io);
        if ($gitlab === null) {
            return ExitCode::INFRASTRUCTURE;
        }

        $context = (new MrContextResolver(
            $modules,
            $gitlab,
            (new ArtifactLayout($cockpit->baseArtifactsPath()))->versionsOnDisk(),
        ))->resolve(
            self::stringArgument($input, 'module'),
            $iid,
            self::stringOption($input, 'version'),
        );

        $mr = $context->mergeRequest;
        $module = $context->module;
        $coreMajor = $context->coreMajor;

        $cache = new ResultsCache($cockpit->resultsPath());
        $cached = $mr->headSha !== null
            ? $cache->find($module->name, ResultKey::mergeRequest($mr->iid), $coreMajor, $mr->headSha)
            : null;

        $stale = false;
        if ($cached === null) {
            $cached = $cache->latest($module->name, ResultKey::mergeRequest($mr->iid), $coreMajor);
            if ($cached !== null && $mr->headSha !== null && $cached->sha !== $mr->headSha) {
                $stale = true;
            }
        }

        if ($cached === null) {
            throw new WorkflowException(sprintf(
                'No cached check results for %s !%d (core %s). Run `upkeep check %s %d --version=%s` first.',
                $module->name,
                $mr->iid,
                $coreMajor,
                $module->name,
                $mr->iid,
                $coreMajor,
            ));
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

            return ExitCode::OK;
        }

        $result = $gitlab->postNote($context->project, $mr->iid, $comment);
        if ($result instanceof ApiFailure) {
            throw new WorkflowException(sprintf(
                'Could not post comment on !%d [%s]: %s',
                $mr->iid,
                $result->shortCode(),
                $result->message,
            ));
        }

        $io->success(sprintf('Comment posted on !%d — %s', $mr->iid, $mr->webUrl));

        $nid = IssueReference::extract($mr->title, $mr->sourceBranch, $mr->description);
        if ($nid !== null && !$input->getOption('no-open')) {
            $issueUrl = IssueReference::issueUrl($nid);
            if (BrowserOpener::open($issueUrl)) {
                $io->writeln(sprintf('Opened drupal.org issue #%d — set the status to Needs work.', $nid));
            } else {
                $io->writeln(sprintf('Set the issue status at: %s', $issueUrl));
            }
        }

        return ExitCode::OK;
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
            $excerpt = $check->outputExcerpt();
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
}

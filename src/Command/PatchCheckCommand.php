<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\PatchContext;

/**
 * The patch-side workhorse: run one patch file through the same isolated flow
 * a merge request gets — resolve the issue, choose the patch, download it,
 * ensure the (module x core) environment, apply it onto a branch off the base,
 * optionally load a fixture, run the checks — and report.
 *
 * Deliberately the mirror of `check`, because the point is that a patch
 * contribution should cost a maintainer no more than a branch does. Results
 * are cached exactly as an MR's are, under a ResultKey::patch() namespace and
 * keyed by the patch's content hash, so the dashboard can show a patch row's
 * LOCAL state and tell a fresh verdict from one about a superseded re-roll.
 * The namespace is what keeps that evidence away from the fast-lane gate,
 * which only ever reads ResultKey::mergeRequest() entries: a patch is not
 * something upkeep can merge, and its verdict must never look like grounds
 * for merging a branch.
 *
 * Exit codes (see ExitCode): 0 all-green, 1 at least one check failed,
 * 2 infrastructure error — which here includes a patch that does not apply,
 * because no verdict on the contribution was produced.
 */
#[AsCommand(
    name: 'patch:check',
    description: 'Run one drupal.org patch through the full isolated check flow.',
)]
final class PatchCheckCommand extends AbstractPatchCommand
{
    protected function configure(): void
    {
        $this->setHelp(<<<'HELP'
            The patch-side counterpart of <info>check</info>: downloads a patch from a drupal.org
            issue, applies it onto a branch off the base, and runs the full suite.

              <info>upkeep patch:check pathauto 3597857</info>
              <info>upkeep patch:check pathauto 3597857 --latest</info>   take the newest patch without asking
              <info>upkeep patch:check pathauto 3597857 --file=NAME</info>

            With several patches on the issue and no terminal to ask at, the newest is
            taken, and said so.
            HELP);

        $this->configurePatchSurface();
        $this->addOption(
            'fixture',
            null,
            InputOption::VALUE_REQUIRED,
            'Load this named fixture into the database before running checks (aborts before any check when the '
            . 'fixture is unknown)',
        );
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $fixture = self::stringOption($input, 'fixture');

        $context = $this->resolveContext($input, $io);
        self::describeContext($io, $context, self::stringOption($input, 'version'));

        $adapter = $this->adapter($input, $output);

        $io->section('Environment');
        $environment = $adapter->ensureEnv($context->module, $context->coreMajor);

        $io->section('Patch');
        $adapter->applyPatch($environment, $context->application());

        if ($fixture !== null) {
            $io->section('Fixture');
            $adapter->loadFixture($environment, $fixture);
        }

        $io->section('Checks');
        $run = $adapter->runChecks($environment);

        self::renderSummary($io, $run, $context->patch->name, $context->issue->nid);
        $this->cacheResults($input, $io, $context, $run);

        return ExitCode::forRun($run);
    }

    /**
     * Persists the run under the patch namespace, keyed by the patch's
     * revision, so the dashboard's LOCAL column can show it and tell a verdict
     * about *this* patch from one about a re-roll that replaced it.
     */
    private function cacheResults(
        InputInterface $input,
        SymfonyStyle $io,
        PatchContext $context,
        CheckRunResult $run,
    ): void {
        $key = ResultKey::patch($context->issue->nid);
        $revision = $context->revision();
        $resultsDir = $this->cockpit($input)->resultsPath();
        (new ResultsCache($resultsDir))->store(
            $context->module->name,
            $key,
            $context->coreMajor,
            $revision,
            $run,
        );
        $io->writeln(sprintf(
            'Results cached: %s/%s/%s/%s/%s.json',
            $resultsDir,
            $context->module->name,
            $key->segment,
            $context->coreMajor,
            $revision,
        ));
    }

    private static function renderSummary(SymfonyStyle $io, CheckRunResult $run, string $patch, int $nid): void
    {
        $io->section('Results');
        $io->table(
            ['Check', 'Status', 'Exit', 'Duration'],
            array_map(static fn (CheckResult $check): array => [
                $check->type->value,
                self::statusCell($check->status),
                $check->exitCode === null ? '-' : (string) $check->exitCode,
                sprintf('%.1fs', $check->durationSeconds),
            ], $run->results),
        );

        foreach ($run->failures() as $failure) {
            $io->section(sprintf('FAILED: %s', $failure->type->value));
            $excerpt = $failure->outputExcerpt();
            $io->writeln($excerpt === '' ? '(no output captured)' : $excerpt);
        }

        $io->newLine();
        if ($run->allPassed()) {
            $io->success(sprintf('All checks green for %s (issue #%d).', $patch, $nid));

            return;
        }

        $io->error(sprintf(
            '%d check(s) failed for %s (issue #%d).',
            \count($run->failures()),
            $patch,
            $nid,
        ));
        // The drupal.org API is read-only, so the status change is the
        // maintainer's to make; naming the issue is the most this can do.
        $io->writeln(sprintf(
            '<fg=gray>Report it at https://www.drupal.org/node/%d — the drupal.org API is read-only, so the '
            . 'status change is a browser action.</>',
            $nid,
        ));
    }

    private static function statusCell(CheckStatus $status): string
    {
        return match ($status) {
            CheckStatus::Passed => '<info>passed</info>',
            CheckStatus::Failed => '<error>FAILED</error>',
            CheckStatus::NoTests => '<comment>no tests</comment>',
            CheckStatus::Unavailable => '<comment>unavailable</comment>',
        };
    }
}

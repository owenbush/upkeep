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
use Upkeep\Results\ResultsCache;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\MrContext;

/**
 * The single-MR workhorse: run one merge request through the full isolated
 * flow — resolve context, ensure the (module x core) environment, apply the
 * MR, optionally load a fixture, run the checks — then report per-check
 * outcomes and persist them to the shared results cache the dashboard reads.
 *
 * Exit codes (see ExitCode): 0 all-green, 1 at least one check failed,
 * 2 infrastructure error (the run never produced a verdict).
 */
#[AsCommand(
    name: 'check',
    description: 'Run one merge request through the full isolated check flow and cache the per-check results.',
)]
final class CheckCommand extends AbstractMrCommand
{
    protected function configure(): void
    {
        $this->configureMrSurface();
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
        $fixture = $input->getOption('fixture');

        $context = $this->resolveContext($input, $io);
        self::describeContext($io, $context, self::stringOption($input, 'version'));

        $adapter = $this->adapter($input, $output);

        $io->section('Environment');
        $environment = $adapter->ensureEnv($context->module, $context->coreMajor);

        $io->section('Merge request');
        $adapter->applyMr($environment, $context->mergeRequest);

        if ($fixture !== null) {
            $io->section('Fixture');
            $adapter->loadFixture($environment, (string) $fixture);
        }

        $io->section('Checks');
        $run = $adapter->runChecks($environment);

        self::renderSummary($io, $run);
        $this->cacheResults($input, $io, $context, $run);

        return ExitCode::forRun($run);
    }

    private static function renderSummary(SymfonyStyle $io, CheckRunResult $run): void
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
            $io->success('All checks green.');
        } else {
            $io->error(sprintf('%d check(s) failed.', \count($run->failures())));
        }
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

    /**
     * Persists the run keyed by (module, MR iid, core, MR head SHA) so the
     * dashboard's LOCAL column and the fast-lane gate can consume it. A
     * missing head SHA (API anomaly) skips caching with a warning rather
     * than storing an entry staleness checks could never match.
     */
    private function cacheResults(
        InputInterface $input,
        SymfonyStyle $io,
        MrContext $context,
        CheckRunResult $run,
    ): void {
        $sha = $context->mergeRequest->headSha;
        if ($sha === null) {
            $io->warning(
                'The MR has no head SHA; results were NOT cached (the dashboard could never tell fresh from '
                . 'stale).',
            );

            return;
        }

        $resultsDir = $this->cockpit($input)->resultsPath();
        (new ResultsCache($resultsDir))->store(
            $context->module->name,
            $context->mergeRequest->iid,
            $context->coreMajor,
            $sha,
            $run,
        );
        $io->writeln(sprintf(
            'Results cached: %s/%s/%d/%s/%s.json',
            $resultsDir,
            $context->module->name,
            $context->mergeRequest->iid,
            $context->coreMajor,
            $sha,
        ));
    }
}

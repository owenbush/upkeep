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
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Cockpit\Module;
use Upkeep\Workflow\MrContextResolver;
use Upkeep\Workflow\WorkflowException;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\MrContext;

/**
 * The single-MR workhorse: run one merge request through the full isolated
 * flow — resolve context, ensure the (module x core) environment, apply the
 * MR, optionally load a fixture, run the checks — then report per-check
 * outcomes and persist them to the shared results cache the dashboard reads.
 *
 * Two modes. Given a merge request IID it does the above and caches the
 * verdict. Given `--working-copy` it runs the same suite against whatever the
 * module working copy is currently on and caches **nothing** — the mode that
 * did not exist, and whose absence left `start` telling people to run a flag
 * that was never built.
 *
 * The asymmetry is not an oversight. A cached verdict is keyed by a subject
 * and a revision (an MR and its head SHA, a patch and its source URL) so the
 * dashboard can tell a fresh pass from one about work that has since moved.
 * A working copy has neither: it is a mutable local state with no identity the
 * next command could match against, and an entry keyed on a guess would put
 * evidence in front of the fast-lane gate that nothing could ever invalidate.
 * So this mode reports and exits, which is all a "did I break it?" question
 * needs.
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
        $this->setHelp(<<<'HELP'
            Runs one merge request through the full isolated flow: provision the
            (module x core) environment, apply the MR, run every check, and cache the
            result where the dashboard and the fast-lane gate read it.

              <info>upkeep check pathauto 12</info>
              <info>upkeep check pathauto 12 --version=11</info>
              <info>upkeep check pathauto 12 --fixture=sample-content</info>

            Or check what you are working on right now, whatever branch that is — after
            <info>upkeep start</info>, or <info>upkeep patch:promote</info>, or your own edits:

              <info>upkeep check pathauto --working-copy</info>

            That mode needs no merge request and no GitLab token, and caches nothing: a
            working copy has no revision the dashboard could match a verdict against.

            Exits 0 all green, 1 a check failed, 2 it could not run at all. Needs base
            artifacts for that core: <info>upkeep base-artifacts:build --version=11</info>.
            HELP);

        $this->configureMrSurface(mrRequired: false);
        $this->addOption(
            'working-copy',
            null,
            InputOption::VALUE_NONE,
            'Check the module working copy as it stands instead of a merge request (caches nothing)',
        );
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
        $workingCopy = $input->getOption('working-copy') === true;
        $mr = self::stringArgument($input, 'mr');

        // Console can express "required" and "optional" but not "exactly one
        // of these two", so the refusals live here — both of them, because
        // guessing either way would run a different check than was asked for.
        if ($workingCopy && $mr !== '') {
            throw new WorkflowException(sprintf(
                'Give a merge request IID or --working-copy, not both: !%s names a specific branch to fetch, '
                . 'while --working-copy means whatever the working copy already holds.',
                $mr,
            ));
        }
        if (!$workingCopy && $mr === '') {
            throw new WorkflowException(
                "Nothing to check. Name a merge request (upkeep check <module> <mr>), or pass --working-copy to "
                . 'check what the module working copy is currently on.',
            );
        }

        if ($workingCopy) {
            return $this->checkWorkingCopy($input, $output, $io, $fixture);
        }

        $context = $this->resolveContext($input, $io);
        self::describeContext($io, $context, self::stringOption($input, 'version'));

        $adapter = $this->adapter($input, $output);

        $io->section('Environment');
        $environment = $adapter->ensureEnv($context->module, $context->coreMajor);

        $io->section('Merge request');
        $adapter->applyMr($environment, $context->mergeRequest);

        if ($fixture !== null) {
            $io->section('Fixture');
            $adapter->loadFixture($environment, $fixture);
        }

        $io->section('Checks');
        $run = $adapter->runChecks($environment);

        self::renderSummary($io, $run);
        $this->cacheResults($input, $io, $context, $run);

        return ExitCode::forRun($run);
    }

    /**
     * The same suite, against whatever the working copy is on.
     *
     * Deliberately touches neither GitLab nor drupal.org: the question this
     * answers — "is what I have in front of me green?" — is one a maintainer
     * has every right to ask offline, and on a branch no remote has heard of.
     */
    private function checkWorkingCopy(
        InputInterface $input,
        OutputInterface $output,
        SymfonyStyle $io,
        ?string $fixture,
    ): int {
        $cockpit = $this->cockpit($input);
        $module = MrContextResolver::requireModule(
            $this->modules($cockpit),
            self::stringArgument($input, 'module'),
        );
        $coreMajor = self::targetCore($input, $module);

        $adapter = $this->adapter($input, $output);

        $io->section('Environment');
        $environment = $adapter->ensureEnv($module, $coreMajor);

        $branch = self::describeWorkingCopy($io, $adapter, $module, $coreMajor);

        if ($fixture !== null) {
            $io->section('Fixture');
            $adapter->loadFixture($environment, $fixture);
        }

        $io->section('Checks');
        $run = $adapter->runChecks($environment);

        self::renderSummary($io, $run);
        $io->writeln(sprintf(
            '<fg=gray>Checked the working copy%s. Nothing was cached: a working copy has no revision the '
            . 'dashboard could tell fresh from stale.</>',
            $branch === null ? '' : ' on ' . $branch,
        ));

        return ExitCode::forRun($run);
    }

    /**
     * Names the branch under test, and refuses a dirty one.
     *
     * A tree with uncommitted changes is checkable but not *reportable*: the
     * run would describe a state that exists only in that moment, and the
     * first thing anybody does with a green result is act on it. Saying what
     * is uncommitted costs one line and prevents that.
     *
     * @throws WorkflowException when the working copy cannot be read
     */
    private static function describeWorkingCopy(
        SymfonyStyle $io,
        EngineAdapterInterface $adapter,
        Module $module,
        string $coreMajor,
    ): ?string {
        $status = $adapter->inspectWorkingCopy($module->name, $coreMajor);
        if ($status === null) {
            throw new WorkflowException(sprintf(
                'The working copy for %s on core %s could not be read, so there is nothing to check. Start '
                . 'something first: upkeep start %s <issue>.',
                $module->name,
                $coreMajor,
                $module->name,
            ));
        }

        $io->section('Working copy');
        $io->writeln(sprintf('Branch: %s', $status->currentBranch ?? '(detached HEAD)'));
        if ($status->isDirty()) {
            $io->warning(array_merge(
                ['Uncommitted changes are included in this run:'],
                $status->describe(),
            ));
        }

        return $status->currentBranch;
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
            ResultKey::mergeRequest($context->mergeRequest->iid),
            $context->coreMajor,
            $sha,
            $run,
        );
        $io->writeln(sprintf(
            'Results cached: %s/%s/%s/%s/%s.json',
            $resultsDir,
            $context->module->name,
            ResultKey::mergeRequest($context->mergeRequest->iid)->segment,
            $context->coreMajor,
            $sha,
        ));
    }
}

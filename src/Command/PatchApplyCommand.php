<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Workflow\ExitCode;

/**
 * Download a patch from a drupal.org issue and apply it in the module's
 * environment, then get out of the way.
 *
 * The manual half of the patch flow: for when you want to look at the change,
 * click through the site, or run something the check suite does not cover.
 * `patch:check` is the same resolution and apply followed by the full suite.
 *
 * Prints the environment path on stdout and nothing else, so it composes:
 * `cd $(upkeep patch:apply widget 3597808)`.
 */
#[AsCommand(
    name: 'patch:apply',
    description: 'Download a patch from a drupal.org issue and apply it in the module environment.',
)]
final class PatchApplyCommand extends AbstractPatchCommand
{
    protected function configure(): void
    {
        $this->configurePatchSurface();
    }

    /** The environment path is the payload; diagnostics stay off stdout. */
    protected function diagnosticsOnStderr(): bool
    {
        return true;
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $context = $this->resolveContext($input, $io);
        self::describeContext($io, $context, self::stringOption($input, 'version'));

        $adapter = $this->adapter($input, $output);

        $io->section('Environment');
        $environment = $adapter->ensureEnv($context->module, $context->coreMajor);

        $io->section('Patch');
        $adapter->applyPatch($environment, $context->application(), self::baseRefresh($input));

        $io->success(sprintf(
            'Applied %s on branch %s. Run checks with: upkeep patch:check %s %d --version=%s',
            $context->patch->name,
            $context->application()->branchName(),
            $context->module->name,
            $context->issue->nid,
            $context->coreMajor,
        ));

        $output->writeln($environment->projectPath);

        return ExitCode::OK;
    }
}

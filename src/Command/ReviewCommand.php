<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\AdapterException;
use Upkeep\Cockpit\RegistryException;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\WorkflowException;

/**
 * Put a merge request onto a browsable site: resolve context, ensure the
 * (module x core) environment, apply the MR, serve — then print the site URL
 * (and one-time login URL when the engine can mint one) prominently.
 *
 * Exit codes: 0 the site is up with the MR applied, 2 infrastructure error.
 * (1 is reserved for red checks and never returned here — review runs no
 * checks.)
 */
#[AsCommand(
    name: 'review',
    description: 'Apply a merge request to a running site and print its browsable URL.',
)]
final class ReviewCommand extends AbstractMrCommand
{
    protected function configure(): void
    {
        $this->configureMrSurface();
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        try {
            $context = $this->resolveContext($input, $io);
            self::describeContext($io, $context, $input->getOption('version'));

            $adapter = $this->adapter($input, $output);

            $io->section('Environment');
            $environment = $adapter->ensureEnv($context->module, $context->coreMajor);

            $io->section('Merge request');
            $adapter->applyMr($environment, $context->mergeRequest);

            $io->section('Serve');
            $serve = $adapter->serve($environment);
        } catch (WorkflowException|AdapterException|RegistryException $e) {
            $io->error($e->getMessage());

            return ExitCode::INFRASTRUCTURE;
        }

        $io->success(sprintf(
            'MR !%d ("%s") is live for review on Drupal core %s.',
            $context->mergeRequest->iid,
            $context->mergeRequest->title,
            $context->coreMajor,
        ));
        $io->writeln(sprintf('  Site URL:   <options=bold>%s</>', $serve->url));
        if ($serve->loginUrl !== null) {
            $io->writeln(sprintf('  Login URL:  %s  (one-time)', $serve->loginUrl));
        }
        $io->newLine();

        return ExitCode::OK;
    }
}

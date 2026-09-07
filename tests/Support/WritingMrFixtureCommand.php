<?php

declare(strict_types=1);

namespace Upkeep\Tests\Support;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Command\AbstractMrCommand;
use Upkeep\Workflow\ExitCode;

/**
 * A merge-request command that would write, declaring nothing.
 *
 * The default has to be strict: a command that writes and forgets to say so
 * must get the credential refusal, not an anonymous client it will fail with
 * four frames further down.
 */
#[AsCommand(name: 'fixture:write')]
final class WritingMrFixtureCommand extends AbstractMrCommand
{
    protected function configure(): void
    {
        $this->configureMrSurface();
    }

    /** Exposes the inherited default so a test can assert what it is. */
    public function declaresReadsOnly(): bool
    {
        return $this->readsOnly();
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $this->resolveContext($input, $io);

        return ExitCode::OK;
    }
}

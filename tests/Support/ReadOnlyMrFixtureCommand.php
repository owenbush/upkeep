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
 * A merge-request command that only looks.
 *
 * A fixture rather than one of the real ones, and the reason matters: a real
 * read-only command that no longer short-circuits on a missing token goes on
 * to make the request, which would put a live call to git.drupalcode.org in
 * the offline suite. This one stops as soon as the client has been built.
 */
#[AsCommand(name: 'fixture:read')]
final class ReadOnlyMrFixtureCommand extends AbstractMrCommand
{
    protected function configure(): void
    {
        $this->configureMrSurface();
    }

    protected function readsOnly(): bool
    {
        return true;
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $this->resolveContext($input, $io);

        return ExitCode::OK;
    }
}

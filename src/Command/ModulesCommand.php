<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Workflow\ExitCode;

#[AsCommand(
    name: 'modules',
    description: 'List the modules registered in the cockpit module registry.',
)]
final class ModulesCommand extends UpkeepCommand
{
    protected function configure(): void
    {
        $this->addCockpitOption();
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->cockpit($input);
        $modules = $this->modules($cockpit);

        if ($modules === []) {
            $io->writeln(sprintf('No modules registered yet. Add entries to %s.', $cockpit->registryPath()));

            return ExitCode::OK;
        }

        $io->table(
            ['Module', 'Project', 'Core versions'],
            array_map(
                static fn ($m) => [$m->name, $m->project, implode(', ', $m->coreVersions)],
                array_values($modules),
            ),
        );

        return ExitCode::OK;
    }
}

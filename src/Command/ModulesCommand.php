<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\RegistryException;

#[AsCommand(
    name: 'modules',
    description: 'List the modules registered in the cockpit module registry.',
)]
final class ModulesCommand extends Command
{
    protected function configure(): void
    {
        $this->addOption(
            'cockpit',
            null,
            InputOption::VALUE_REQUIRED,
            sprintf('Path to the cockpit directory (defaults to $%s, then the current directory)', Cockpit::ENV_VAR),
        );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);
        $cockpit = Cockpit::resolve($input->getOption('cockpit'));

        try {
            $registry = $cockpit->loadRegistry();
        } catch (RegistryException $e) {
            $io->error($e->getMessage());

            return Command::FAILURE;
        }

        $modules = $registry->modules();
        if ($modules === []) {
            $io->writeln(sprintf('No modules registered yet. Add entries to %s.', $cockpit->registryPath()));

            return Command::SUCCESS;
        }

        $io->table(
            ['Module', 'Project', 'Core versions'],
            array_map(
                static fn ($m) => [$m->name, $m->project, implode(', ', $m->coreVersions)],
                array_values($modules),
            ),
        );

        return Command::SUCCESS;
    }
}

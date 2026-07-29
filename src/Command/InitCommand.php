<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Cockpit\Cockpit;

#[AsCommand(
    name: 'init',
    description: 'Scaffold a new cockpit: module registry, base-artifacts/ and fixtures/ directories.',
)]
final class InitCommand extends Command
{
    private const string REGISTRY_TEMPLATE = <<<'YAML'
        # Upkeep cockpit module registry.
        #
        # Each key under "modules" is a module machine name. Required fields:
        #   project:       git.drupalcode.org project path, e.g. "project/token_or"
        #   core_versions: non-empty list of Drupal core versions the module is
        #                  maintained for, e.g. ["10", "11"]
        #
        # Example entry:
        #
        # modules:
        #   token_or:
        #     project: project/token_or
        #     core_versions: ["10", "11"]
        modules: {}

        YAML;

    protected function configure(): void
    {
        $this->addArgument('dir', InputArgument::OPTIONAL, 'Directory to create the cockpit in', '.');
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);
        $cockpit = new Cockpit(rtrim((string) $input->getArgument('dir'), '/'));

        if (file_exists($cockpit->registryPath())) {
            $io->error(sprintf('A cockpit already exists at "%s" (found %s).', $cockpit->root, Cockpit::REGISTRY_FILENAME));

            return Command::FAILURE;
        }

        foreach ([$cockpit->root, $cockpit->baseArtifactsPath(), $cockpit->fixturesPath()] as $dir) {
            if (!is_dir($dir) && !mkdir($dir, 0755, true) && !is_dir($dir)) {
                $io->error(sprintf('Could not create directory "%s".', $dir));

                return Command::FAILURE;
            }
        }

        file_put_contents($cockpit->registryPath(), self::REGISTRY_TEMPLATE);
        file_put_contents($cockpit->baseArtifactsPath() . '/.gitkeep', '');
        file_put_contents($cockpit->fixturesPath() . '/.gitkeep', '');

        $io->success(sprintf(
            'Cockpit created at "%s": %s, %s/, %s/.',
            $cockpit->root,
            Cockpit::REGISTRY_FILENAME,
            Cockpit::BASE_ARTIFACTS_DIR,
            Cockpit::FIXTURES_DIR,
        ));

        return Command::SUCCESS;
    }
}

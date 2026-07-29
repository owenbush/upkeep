<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\BaseArtifact\BaseArtifactBuilder;
use Upkeep\BaseArtifact\BuildException;
use Upkeep\BaseArtifact\MetaException;
use Upkeep\Cockpit\Cockpit;

#[AsCommand(
    name: 'base-artifacts:build',
    description: 'Build the canonical per-core-version base artifacts: resolved base tree + clean-install DB dump.',
)]
final class BaseArtifactsBuildCommand extends Command
{
    protected function configure(): void
    {
        $this
            ->addOption('version', null, InputOption::VALUE_REQUIRED, 'Drupal core major version to build artifacts for (e.g. 11)')
            ->addOption('force', null, InputOption::VALUE_NONE, 'Deliberately rebuild over an existing artifact set')
            ->addOption('scratch-dir', null, InputOption::VALUE_REQUIRED, 'Directory for the throwaway ddev site-install project', sys_get_temp_dir())
            ->addOption(
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

        if (!file_exists($cockpit->registryPath())) {
            $io->error(sprintf('No cockpit found at "%s" (missing %s). Run `upkeep init` first.', $cockpit->root, Cockpit::REGISTRY_FILENAME));

            return Command::FAILURE;
        }

        $version = (string) $input->getOption('version');
        if ($version === '') {
            $io->error('The --version option is required (e.g. --version=11).');

            return Command::FAILURE;
        }

        $layout = new ArtifactLayout($cockpit->baseArtifactsPath());
        $builder = new BaseArtifactBuilder(
            $layout,
            (string) $input->getOption('scratch-dir'),
            static fn (string $line) => $output->writeln($line),
        );

        try {
            $meta = $builder->build($version, (bool) $input->getOption('force'));
        } catch (\InvalidArgumentException | BuildException | MetaException $e) {
            $io->error($e->getMessage());

            return Command::FAILURE;
        }

        $io->success(sprintf(
            'Base artifacts for Drupal %s built: core %s, PHP %s, DB %s. Tree: %s, dump: %s.',
            $meta->coreMajor,
            $meta->coreVersion,
            $meta->phpVersion,
            $meta->dbEngine,
            $layout->treePath($version),
            $layout->dumpPath($version),
        ));

        return Command::SUCCESS;
    }
}

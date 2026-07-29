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
            // Named --core, not --version: Symfony Console reserves -V/--version
            // at the application level (it prints the app version before any
            // command runs), so a command-scoped --version can never be received.
            ->addOption('core', null, InputOption::VALUE_REQUIRED, 'Drupal core major version to build artifacts for (e.g. 11)')
            ->addOption('force', null, InputOption::VALUE_NONE, 'Deliberately rebuild over an existing artifact set')
            // Defaults under $HOME, not the system temp dir: the throwaway ddev
            // project is bind-mounted into the Docker VM, and macOS providers
            // (colima, Docker Desktop) only share the home directory by default
            // — /tmp and /private/tmp are not mounted and the install fails.
            ->addOption(
                'scratch-dir',
                null,
                InputOption::VALUE_REQUIRED,
                'Directory for the throwaway ddev site-install project (must be a path your Docker provider mounts, e.g. under your home directory)',
                self::defaultScratchDir(),
            )
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

        $version = (string) $input->getOption('core');
        if ($version === '') {
            $io->error('The --core option is required (e.g. --core=11).');

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

    private static function defaultScratchDir(): string
    {
        $home = getenv('HOME');

        return $home !== false && $home !== '' ? $home . '/.upkeep/scratch' : sys_get_temp_dir();
    }
}

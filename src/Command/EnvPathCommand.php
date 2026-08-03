<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Upkeep\Adapter\DdevContribAdapter;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\ProcessRunner;
use Upkeep\Adapter\ProjectsRoot;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\Cockpit\Cockpit;

#[AsCommand(
    name: 'env:path',
    description: 'Print the absolute path of a module\'s environment directory.',
)]
final class EnvPathCommand extends Command
{
    public function __construct(private readonly ?EngineAdapterInterface $adapter = null)
    {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this
            ->addArgument('module', InputArgument::REQUIRED, 'Registered module machine name')
            ->addOption('version', null, InputOption::VALUE_REQUIRED, 'Target core major version (defaults to the first tracked version in the registry)')
            ->addOption('cockpit', null, InputOption::VALUE_REQUIRED, sprintf('Path to the cockpit directory (defaults to $%s, then the current directory)', Cockpit::ENV_VAR))
            ->addOption('projects-root', null, InputOption::VALUE_REQUIRED, sprintf('Directory holding the engine environments (defaults to $%s, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects)', ProjectsRoot::ENV_VAR));
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $cockpit = Cockpit::resolve($input->getOption('cockpit'));
        $modules = $cockpit->loadRegistry()->modules();

        $name = (string) $input->getArgument('module');
        if (!isset($modules[$name])) {
            $output->writeln(sprintf('<error>Module "%s" is not registered. Run `upkeep modules` to see what is.</error>', $name));

            return Command::FAILURE;
        }

        $module = $modules[$name];
        $version = $input->getOption('version');
        $coreMajor = $version !== null ? (string) $version : $module->coreVersions[0] ?? null;

        if ($coreMajor === null || !\in_array($coreMajor, $module->coreVersions, true)) {
            $output->writeln(sprintf(
                '<error>Core version %s is not tracked for %s (tracked: %s).</error>',
                $coreMajor ?? '(none)',
                $name,
                implode(', ', $module->coreVersions),
            ));

            return Command::FAILURE;
        }

        $adapter = $this->adapter ?? $this->buildAdapter($cockpit, $input);
        $path = $adapter->resolveEnvPath($name, $coreMajor);

        if ($path === null) {
            $output->writeln(sprintf('<error>No provisioned environment for %s on Drupal %s. Run `upkeep check` or `upkeep review` to create one.</error>', $name, $coreMajor));

            return Command::FAILURE;
        }

        $output->writeln($path);

        return Command::SUCCESS;
    }

    private function buildAdapter(Cockpit $cockpit, InputInterface $input): EngineAdapterInterface
    {
        return new DdevContribAdapter(
            new ArtifactLayout($cockpit->baseArtifactsPath()),
            ProjectsRoot::resolve($input->getOption('projects-root'), $cockpit->root),
            new ProcessRunner(static function (): void {}),
            static function (): void {},
        );
    }
}

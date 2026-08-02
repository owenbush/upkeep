<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\DdevContribAdapter;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\ProcessRunner;
use Upkeep\Adapter\ProjectsRoot;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\RegistryException;

#[AsCommand(
    name: 'dev',
    description: 'Prepare an environment for active development: provision if needed, optionally check out a branch, and print the path.',
)]
final class DevCommand extends Command
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
            ->addOption('branch', null, InputOption::VALUE_REQUIRED, 'Branch to check out in the module working copy')
            ->addOption('cockpit', null, InputOption::VALUE_REQUIRED, sprintf('Path to the cockpit directory (defaults to $%s, then the current directory)', Cockpit::ENV_VAR))
            ->addOption('projects-root', null, InputOption::VALUE_REQUIRED, sprintf('Directory holding the engine environments (defaults to $%s, then ~/.upkeep/projects)', ProjectsRoot::ENV_VAR));
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle(
            $input,
            $output instanceof ConsoleOutputInterface ? $output->getErrorOutput() : $output,
        );

        $cockpit = Cockpit::resolve($input->getOption('cockpit'));
        try {
            $registry = $cockpit->loadRegistry();
        } catch (RegistryException $e) {
            $io->error($e->getMessage());

            return Command::FAILURE;
        }

        $modules = $registry->modules();
        $name = (string) $input->getArgument('module');

        if (!isset($modules[$name])) {
            $io->error(sprintf('Module "%s" is not registered. Run `upkeep modules` to see what is.', $name));

            return Command::FAILURE;
        }

        $module = $modules[$name];
        $version = $input->getOption('version');
        $coreMajor = $version !== null ? (string) $version : $module->coreVersions[0] ?? null;

        if ($coreMajor === null || !\in_array($coreMajor, $module->coreVersions, true)) {
            $io->error(sprintf(
                'Core version %s is not tracked for %s (tracked: %s).',
                $coreMajor ?? '(none)',
                $name,
                implode(', ', $module->coreVersions),
            ));

            return Command::FAILURE;
        }

        $adapter = $this->adapter ?? $this->buildAdapter($cockpit, $input, $io);
        if ($adapter === null) {
            return Command::FAILURE;
        }

        try {
            $environment = $adapter->ensureEnv($module, $coreMajor);
        } catch (AdapterException $e) {
            $io->error($e->getMessage());

            return Command::FAILURE;
        }

        $branch = $input->getOption('branch');
        if ($branch !== null) {
            try {
                $adapter->checkoutBranch($environment, (string) $branch);
            } catch (AdapterException $e) {
                $io->error($e->getMessage());

                return Command::FAILURE;
            }
        }

        $modulePath = $environment->projectPath . '/module';

        $output->writeln('');
        $output->writeln(sprintf('  <fg=green>Environment</>  %s', $environment->projectName));
        $output->writeln(sprintf('  <fg=green>Module path</>  %s', $modulePath));
        $output->writeln(sprintf('  <fg=green>Site URL</>     %s', $environment->primaryUrl));
        $output->writeln('');
        $output->writeln(sprintf('  cd %s', $modulePath));
        $output->writeln('');

        return Command::SUCCESS;
    }

    private function buildAdapter(Cockpit $cockpit, InputInterface $input, SymfonyStyle $io): ?EngineAdapterInterface
    {
        return new DdevContribAdapter(
            new ArtifactLayout($cockpit->baseArtifactsPath()),
            ProjectsRoot::resolve($input->getOption('projects-root')),
            new ProcessRunner(static function (string $line) use ($io): void {
                $io->text($line);
            }),
            static function (string $line) use ($io): void {
                $io->text($line);
            },
        );
    }
}

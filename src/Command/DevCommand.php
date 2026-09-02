<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\EngineAdapterFactory;
use Upkeep\Workflow\ExitCode;

#[AsCommand(
    name: 'dev',
    description: 'Prepare an environment for active development: provision if needed, optionally check out a '
    . 'branch, and print the path.',
)]
final class DevCommand extends UpkeepCommand
{
    public function __construct(private readonly EngineAdapterFactory $engines)
    {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addModuleArgument()
            ->addTargetCoreOption()
            ->addOption('branch', null, InputOption::VALUE_REQUIRED, 'Branch to check out in the module working copy')
            ->addCockpitOption()
            ->addProjectsRootOption();
    }

    /** The environment summary is the payload; diagnostics stay off stdout. */
    protected function diagnosticsOnStderr(): bool
    {
        return true;
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->cockpit($input);
        $module = self::requireModule($this->modules($cockpit), self::stringArgument($input, 'module'));
        $coreMajor = self::targetCore($input, $module);

        $adapter = $this->engines->create(
            $cockpit,
            self::stringOption($input, 'projects-root'),
            static fn (string $line) => $io->text($line),
            // The engine's raw process output, which is a wall of it — behind
            // -v like everywhere else. `dev`'s payload is four lines telling
            // you where the site is; burying them under the provisioning
            // transcript defeats the command.
            static fn (string $line) => $io->writeln($line, OutputInterface::VERBOSITY_VERBOSE),
        );

        $environment = $adapter->ensureEnv($module, $coreMajor);

        $branch = self::stringOption($input, 'branch');
        if ($branch !== null) {
            $adapter->checkoutBranch($environment, $branch);
        }

        $modulePath = $environment->projectPath . '/module';

        $output->writeln('');
        $output->writeln(sprintf('  <fg=green>Environment</>  %s', $environment->projectName));
        $output->writeln(sprintf('  <fg=green>Module path</>  %s', $modulePath));
        $output->writeln(sprintf('  <fg=green>Site URL</>     %s', $environment->primaryUrl));
        $output->writeln('');
        $output->writeln(sprintf('  cd %s', $modulePath));
        $output->writeln('');

        return ExitCode::OK;
    }
}

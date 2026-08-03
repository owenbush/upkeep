<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\EngineAdapterFactory;
use Upkeep\Workflow\ExitCode;

#[AsCommand(
    name: 'env:path',
    description: 'Print the absolute path of a module\'s environment directory.',
)]
final class EnvPathCommand extends UpkeepCommand
{
    public function __construct(private readonly EngineAdapterFactory $engines)
    {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addModuleArgument()
            ->addTargetCoreOption()
            ->addCockpitOption()
            ->addProjectsRootOption();
    }

    /** stdout carries the path and nothing else, so `cd $(upkeep env:path x)` works. */
    protected function diagnosticsOnStderr(): bool
    {
        return true;
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->cockpit($input);
        $name = self::stringArgument($input, 'module');
        $module = self::requireModule($this->modules($cockpit), $name);
        $coreMajor = self::targetCore($input, $module);

        $adapter = $this->engines->create(
            $cockpit,
            self::stringOption($input, 'projects-root'),
            static function (): void {
            },
            static function (): void {
            },
        );

        $path = $adapter->resolveEnvPath($name, $coreMajor);
        if ($path === null) {
            throw new AdapterException(sprintf(
                'No provisioned environment for %s on Drupal %s. Run `upkeep check` or `upkeep review` to create one.',
                $name,
                $coreMajor,
            ));
        }

        $output->writeln($path);

        return ExitCode::OK;
    }
}

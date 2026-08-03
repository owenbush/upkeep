<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\Process\Process;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\EngineAdapterFactory;
use Upkeep\Security\CredentialEnvironment;
use Upkeep\Workflow\ExitCode;

/**
 * Run an arbitrary command inside a module's environment directory.
 *
 * Exit codes follow the CLI-wide contract rather than the child's raw code:
 * 0 the command succeeded, 1 it exited non-zero, 2 upkeep could not run it
 * (unregistered module, untracked core, no provisioned environment, or a
 * child that never started). See Workflow\ExitCode.
 */
#[AsCommand(
    name: 'exec',
    description: 'Run a command in a module\'s environment directory.',
)]
final class ExecCommand extends UpkeepCommand
{
    public function __construct(private readonly EngineAdapterFactory $engines)
    {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addModuleArgument()
            ->addArgument(
                'cmd',
                InputArgument::IS_ARRAY | InputArgument::REQUIRED,
                'Command to run (use -- before the command to separate it from upkeep options)',
            );
        $this->addTargetCoreOption()
            ->addCockpitOption()
            ->addProjectsRootOption();
    }

    /** stdout belongs to the wrapped command; upkeep's own words go to stderr. */
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

        // Never empty: `cmd` is a REQUIRED array argument, so the console
        // refuses the invocation ("Not enough arguments") before perform() is
        // reached. A guard here would be a branch nothing can take.
        /** @var list<string> $cmd */
        $cmd = $input->getArgument('cmd');

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

        $process = new Process($cmd, $path, CredentialEnvironment::scrubbed(), timeout: null);
        $process->run(static function (string $type, string $buffer) use ($output): void {
            $output->write($buffer);
        });

        return ExitCode::forChildProcess($process->getExitCode());
    }
}

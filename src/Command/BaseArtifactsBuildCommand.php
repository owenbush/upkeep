<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\CommandRunner;
use Upkeep\Adapter\MountablePath;
use Upkeep\Adapter\ProcessRunner;
use Upkeep\Adapter\ThrowawaySite;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\BaseArtifact\BaseArtifactBuilder;
use Upkeep\BaseArtifact\CoreConstraint;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\WorkflowException;

#[AsCommand(
    name: 'base-artifacts:build',
    description: 'Build the canonical per-core-version base artifacts: resolved base tree + clean-install DB dump.',
)]
final class BaseArtifactsBuildCommand extends UpkeepCommand
{
    /**
     * @param ?CommandRunner $runner the shell-out seam every step of the build
     *                               goes through; null builds the live process
     *                               runner sharing this invocation's log
     */
    public function __construct(private readonly ?CommandRunner $runner = null)
    {
        parent::__construct();
    }

    protected function configure(): void
    {
        // --version, like every other core-version selector: the
        // application-level -V/--version is dropped in bin/upkeep (see
        // Command\VersionOptionInput), so command-scoped --version arrives
        // intact. This option was historically spelled --core on the strength
        // of a Symfony constraint that no longer applies.
        $this
            ->addOption(
                'version',
                null,
                InputOption::VALUE_REQUIRED,
                'Drupal core major version to build artifacts for (e.g. 11)',
            )
            ->addOption('force', null, InputOption::VALUE_NONE, 'Deliberately rebuild over an existing artifact set')
            // Deliberate, never inferred. Falling back to a pre-release when a
            // stable constraint finds nothing would quietly build something
            // other than what was asked for, and a base artifact set is what
            // every later verdict is measured against.
            ->addOption(
                'stability',
                null,
                InputOption::VALUE_REQUIRED,
                'Lowest release stability to accept (' . implode(', ', CoreConstraint::STABILITIES)
                . '). Needed while a core major is still in alpha or beta, which is when compatibility '
                . 'work happens',
            )
            // Defaults under $HOME, not the system temp dir: the throwaway
            // install project is bind-mounted into the Docker VM, and macOS
            // providers (colima, Docker Desktop) only share the home directory
            // by default — /tmp and /private/tmp are not mounted and the
            // install fails.
            ->addOption(
                'scratch-dir',
                null,
                InputOption::VALUE_REQUIRED,
                'Directory for the throwaway site-install project (must be a path your Docker provider mounts, e.g. '
                . 'under your home directory)',
                self::defaultScratchDir(),
            );
        $this->addCockpitOption();
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->requireCockpit($input);

        $version = self::stringOption($input, 'version') ?? '';
        if ($version === '') {
            throw new WorkflowException('The --version option is required (e.g. --version=11).');
        }

        $layout = new ArtifactLayout($cockpit->baseArtifactsPath());
        $log = static fn (string $line) => $output->writeln($line);

        // The throwaway install project is bind-mounted like any other
        // environment, so the scratch directory is held to the same $HOME
        // containment rule the option's help text promises.
        $scratchDir = MountablePath::requireUnderHome(
            self::stringOption($input, 'scratch-dir') ?? '',
            'scratch directory',
            '--scratch-dir',
        );

        $runner = $this->runner ?? new ProcessRunner($log);
        $builder = new BaseArtifactBuilder(
            $layout,
            new ThrowawaySite($runner, $log),
            $scratchDir,
            $log,
            $runner,
        );

        try {
            $meta = $builder->build(
                $version,
                (bool) $input->getOption('force'),
                self::stringOption($input, 'stability') ?: null,
            );
        } catch (\InvalidArgumentException $e) {
            throw new WorkflowException($e->getMessage(), 0, $e);
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

        return ExitCode::OK;
    }

    private static function defaultScratchDir(): string
    {
        $home = getenv('HOME');

        return $home !== false && $home !== '' ? $home . '/.upkeep/scratch' : sys_get_temp_dir();
    }
}

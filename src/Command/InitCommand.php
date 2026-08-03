<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Filesystem\FilesystemException;
use Upkeep\Filesystem\FileWriter;
use Upkeep\Workflow\ExitCode;

#[AsCommand(
    name: 'init',
    description: 'Scaffold a new cockpit: module registry, base-artifacts/, fixtures/, and projects/ directories.',
)]
final class InitCommand extends UpkeepCommand
{
    private const REGISTRY_TEMPLATE = <<<'YAML'
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

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = new Cockpit(self::stringArgument($input, 'dir'));

        // is_file, not file_exists: a *directory* named registry.yml is not a
        // cockpit, and claiming it is would send the user looking for one.
        // The write below is what reports that path as unusable.
        if (is_file($cockpit->registryPath())) {
            throw new FilesystemException(sprintf(
                'A cockpit already exists at "%s" (found %s).',
                $cockpit->root,
                Cockpit::REGISTRY_FILENAME,
            ));
        }

        $dirs = [
            $cockpit->root,
            $cockpit->baseArtifactsPath(),
            $cockpit->fixturesPath(),
            $cockpit->projectsPath(),
        ];

        // Every write is checked: a cockpit reported as created but missing
        // its registry.yml is the worst possible outcome here, because the
        // next command reports "Module registry not found" instead.
        try {
            foreach ($dirs as $dir) {
                FileWriter::ensureDirectory($dir, FileWriter::MODE_SHARED_DIR);
            }

            FileWriter::write($cockpit->registryPath(), self::REGISTRY_TEMPLATE, FileWriter::MODE_SHARED);
            FileWriter::write($cockpit->baseArtifactsPath() . '/.gitkeep', '', FileWriter::MODE_SHARED);
            FileWriter::write($cockpit->fixturesPath() . '/.gitkeep', '', FileWriter::MODE_SHARED);
            FileWriter::write($cockpit->projectsPath() . '/.gitkeep', '', FileWriter::MODE_SHARED);
        } catch (FilesystemException $e) {
            throw new FilesystemException(
                sprintf('Could not scaffold the cockpit at "%s": %s', $cockpit->root, $e->getMessage()),
                0,
                $e,
            );
        }

        $io->success(sprintf(
            'Cockpit created at "%s": %s, %s/, %s/, %s/.',
            $cockpit->root,
            Cockpit::REGISTRY_FILENAME,
            Cockpit::BASE_ARTIFACTS_DIR,
            Cockpit::FIXTURES_DIR,
            Cockpit::PROJECTS_DIR,
        ));

        return ExitCode::OK;
    }
}

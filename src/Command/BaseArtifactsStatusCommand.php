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
use Upkeep\BaseArtifact\ArtifactScanner;
use Upkeep\Cockpit\Cockpit;

#[AsCommand(
    name: 'base-artifacts:status',
    description: 'List which core versions have base artifacts, their build dates and sizes.',
)]
final class BaseArtifactsStatusCommand extends Command
{
    protected function configure(): void
    {
        $this->addOption(
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

        $records = new ArtifactScanner(new ArtifactLayout($cockpit->baseArtifactsPath()))->scan();

        if ($records === []) {
            $io->writeln(sprintf(
                'No base artifacts built yet under %s. Run `upkeep base-artifacts:build --core=N`.',
                $cockpit->baseArtifactsPath(),
            ));

            return Command::SUCCESS;
        }

        $io->table(
            ['Core', 'State', 'Exact core', 'Built', 'PHP', 'DB engine', 'Tree size', 'Dump size'],
            array_map(static fn ($r) => [
                $r->version,
                $r->complete ? 'complete (canonical)' : 'incomplete: missing ' . implode(', ', $r->missing),
                $r->meta?->coreVersion ?? '-',
                $r->meta?->builtAt->format('Y-m-d H:i:s T') ?? '-',
                $r->meta?->phpVersion ?? '-',
                $r->meta?->dbEngine ?? '-',
                self::formatBytes($r->treeSizeBytes),
                self::formatBytes($r->dumpSizeBytes),
            ], $records),
        );

        return Command::SUCCESS;
    }

    private static function formatBytes(int $bytes): string
    {
        if ($bytes >= 1024 ** 3) {
            return sprintf('%.1f GiB', $bytes / 1024 ** 3);
        }
        if ($bytes >= 1024 ** 2) {
            return sprintf('%.1f MiB', $bytes / 1024 ** 2);
        }
        if ($bytes >= 1024) {
            return sprintf('%.1f KiB', $bytes / 1024);
        }

        return $bytes . ' B';
    }
}

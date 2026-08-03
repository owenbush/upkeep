<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\BaseArtifact\ArtifactScanner;
use Upkeep\Maintenance\ByteFormat;
use Upkeep\Workflow\ExitCode;

#[AsCommand(
    name: 'base-artifacts:status',
    description: 'List which core versions have base artifacts, their build dates and sizes.',
)]
final class BaseArtifactsStatusCommand extends UpkeepCommand
{
    protected function configure(): void
    {
        $this->addCockpitOption();
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->requireCockpit($input);

        $records = (new ArtifactScanner(new ArtifactLayout($cockpit->baseArtifactsPath())))->scan();

        if ($records === []) {
            $io->writeln(sprintf(
                'No base artifacts built yet under %s. Run `upkeep base-artifacts:build --version=N`.',
                $cockpit->baseArtifactsPath(),
            ));

            return ExitCode::OK;
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
                ByteFormat::human($r->treeSizeBytes),
                ByteFormat::human($r->dumpSizeBytes),
            ], $records),
        );

        return ExitCode::OK;
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\ProjectsRoot;
use Upkeep\Adapter\VolumeProbe;
use Upkeep\Maintenance\ByteFormat;
use Upkeep\Maintenance\Category;
use Upkeep\Maintenance\InventoryItem;
use Upkeep\Maintenance\InventoryScanner;
use Upkeep\Workflow\ExitCode;

#[AsCommand(
    name: 'status',
    description: 'Report cockpit state; --disk itemizes real measured disk usage per module, core version, and '
    . 'category.',
)]
final class StatusCommand extends UpkeepCommand
{
    public function __construct(private readonly VolumeProbe $volumeProbe)
    {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addOption(
            'disk',
            null,
            InputOption::VALUE_NONE,
            'Itemize disk usage (project trees, materialized snapshots, docker volumes, base artifacts, fixture '
            . 'dumps) with totals',
        );
        $this->addCockpitOption()->addProjectsRootOption();
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->cockpit($input);
        // Loaded, not merely stat()ed: a registry that exists but does not
        // parse is reported here, with the reason, rather than further down.
        $modules = $this->modules($cockpit);

        $projectsRoot = ProjectsRoot::resolve(self::stringOption($input, 'projects-root'), $cockpit->root);
        $scanner = new InventoryScanner($cockpit, $projectsRoot);
        $items = $scanner->scan();
        // A directory that could not be read is reported as such, not folded
        // into the totals as if it were empty.
        foreach ($scanner->warnings() as $warning) {
            $io->warning($warning);
        }

        $trees = array_filter(
            $items,
            static fn (InventoryItem $i): bool => $i->category === Category::ProjectTree,
        );
        $items = [...$items, ...$this->volumeProbe->itemsForInventory($items)];

        if (!$input->getOption('disk')) {
            $io->writeln(sprintf('Cockpit: %s (%d registered module(s))', $cockpit->root, \count($modules)));
            $io->writeln(sprintf('Projects root: %s (%d environment(s))', $projectsRoot, \count($trees)));
            $io->writeln(sprintf(
                'Total tracked disk usage: %s across %d item(s). Use --disk for the breakdown.',
                ByteFormat::human(array_sum(array_map(static fn (InventoryItem $i) => $i->sizeBytes, $items))),
                \count($items),
            ));

            return ExitCode::OK;
        }

        self::renderDiskTable($io, $items);

        return ExitCode::OK;
    }

    /**
     * @param list<InventoryItem> $items
     */
    private static function renderDiskTable(SymfonyStyle $io, array $items): void
    {
        $now = new \DateTimeImmutable();

        $sorted = $items;
        usort($sorted, static fn (InventoryItem $a, InventoryItem $b): int =>
            [$a->module ?? "\xFF", $a->coreMajor ?? '', $a->category->value, $a->path]
            <=> [$b->module ?? "\xFF", $b->coreMajor ?? '', $b->category->value, $b->path]);

        $rows = array_map(static fn (InventoryItem $i) => [
            $i->module ?? '-',
            $i->coreMajor ?? '-',
            $i->category->label(),
            $i->path,
            self::age($i, $now),
            $i->keepMarked
                ? 'keep'
                : ($i->category === Category::BaseArtifact || $i->category === Category::FixtureDump
                    ? 'canonical'
                    : ''),
            ByteFormat::human($i->sizeBytes),
        ], $sorted);

        $io->table(['Module', 'Core', 'Category', 'Item', 'Age', 'Protection', 'Size'], $rows);

        $byCategory = [];
        foreach ($items as $item) {
            $byCategory[$item->category->value] = ($byCategory[$item->category->value] ?? 0) + $item->sizeBytes;
        }
        ksort($byCategory);
        foreach ($byCategory as $category => $bytes) {
            $io->writeln(sprintf('  %-22s %s', Category::from($category)->label() . 's:', ByteFormat::human($bytes)));
        }
        $io->writeln(sprintf('  %-22s %s', 'total:', ByteFormat::human(array_sum($byCategory))));
    }

    private static function age(InventoryItem $item, \DateTimeImmutable $now): string
    {
        $seconds = $item->ageSeconds($now);
        if ($seconds === null) {
            return '?';
        }
        if ($seconds >= 86400) {
            return sprintf('%dd', intdiv($seconds, 86400));
        }

        return sprintf('%dh', intdiv($seconds, 3600));
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\ProcessRunner;
use Upkeep\Adapter\ProjectsRoot;
use Upkeep\Adapter\VolumeProbe;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Maintenance\ByteFormat;
use Upkeep\Maintenance\Category;
use Upkeep\Maintenance\InventoryItem;
use Upkeep\Maintenance\InventoryScanner;

#[AsCommand(
    name: 'status',
    description: 'Report cockpit state; --disk itemizes real measured disk usage per module, core version, and '
    . 'category.',
)]
final class StatusCommand extends Command
{
    public function __construct(private readonly ?VolumeProbe $volumeProbe = null)
    {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this
            ->addOption(
                'disk',
                null,
                InputOption::VALUE_NONE,
                'Itemize disk usage (project trees, materialized snapshots, docker volumes, base artifacts, '
                . 'fixture dumps) with totals',
            )
            ->addOption(
                'cockpit',
                null,
                InputOption::VALUE_REQUIRED,
                sprintf(
                    'Path to the cockpit directory (defaults to $%s, then the current directory)',
                    Cockpit::ENV_VAR,
                ),
            )
            ->addOption(
                'projects-root',
                null,
                InputOption::VALUE_REQUIRED,
                sprintf(
                    'Directory holding the engine environments (defaults to $%s, then <cockpit>/projects/ if it '
                    . 'exists, then ~/.upkeep/projects)',
                    ProjectsRoot::ENV_VAR,
                ),
            );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);
        $cockpit = Cockpit::resolve($input->getOption('cockpit'));

        if (!file_exists($cockpit->registryPath())) {
            $io->error(sprintf(
                'No cockpit found at "%s" (missing %s). Run `upkeep init` first.',
                $cockpit->root,
                Cockpit::REGISTRY_FILENAME,
            ));

            return Command::FAILURE;
        }

        $projectsRoot = ProjectsRoot::resolve($input->getOption('projects-root'), $cockpit->root);
        $items = (new InventoryScanner($cockpit, $projectsRoot))->scan();

        $trees = [];
        foreach ($items as $item) {
            if ($item->category === Category::ProjectTree && $item->projectName !== null) {
                $trees[$item->projectName] = $item;
            }
        }
        $probe = $this->volumeProbe ?? self::defaultVolumeProbe();
        $items = [...$items, ...$probe->items($trees)];

        if (!$input->getOption('disk')) {
            $io->writeln(sprintf('Cockpit: %s', $cockpit->root));
            $io->writeln(sprintf('Projects root: %s (%d environment(s))', $projectsRoot, \count($trees)));
            $io->writeln(sprintf(
                'Total tracked disk usage: %s across %d item(s). Use --disk for the breakdown.',
                ByteFormat::human(array_sum(array_map(static fn (InventoryItem $i) => $i->sizeBytes, $items))),
                \count($items),
            ));

            return Command::SUCCESS;
        }

        self::renderDiskTable($io, $items);

        return Command::SUCCESS;
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

    private static function defaultVolumeProbe(): VolumeProbe
    {
        return VolumeProbe::withRunner(new ProcessRunner(static function (string $line): void {
        }));
    }
}

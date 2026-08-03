<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\EngineAdapterFactory;
use Upkeep\Adapter\ProjectsRoot;
use Upkeep\Adapter\VolumeProbe;
use Upkeep\Maintenance\ByteFormat;
use Upkeep\Maintenance\Category;
use Upkeep\Maintenance\Duration;
use Upkeep\Maintenance\InventoryItem;
use Upkeep\Maintenance\InventoryScanner;
use Upkeep\Maintenance\PruneExecutor;
use Upkeep\Maintenance\PruneScope;
use Upkeep\Maintenance\PruneSelector;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\WorkflowException;

#[AsCommand(
    name: 'prune',
    description: 'Reclaim disposable state (environment trees, engine projects, materialized snapshots). Dry-run '
    . 'by default; never touches base artifacts, keep-marked items, or committed fixture dumps.',
)]
final class PruneCommand extends UpkeepCommand
{
    public function __construct(
        private readonly EngineAdapterFactory $engines,
        private readonly VolumeProbe $volumeProbe,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this
            ->addOption(
                'trees',
                null,
                InputOption::VALUE_NONE,
                'Prune disposable environment trees (disposed via the adapter teardown, which also releases the '
                . 'engine project)',
            )
            ->addOption(
                'snapshots',
                null,
                InputOption::VALUE_NONE,
                'Prune materialized fixture snapshots (their committed .sql.gz dumps can rebuild them at any time)',
            )
            ->addOption(
                'projects',
                null,
                InputOption::VALUE_NONE,
                'Prune whole engine projects: trees plus their docker named volumes, via the adapter teardown',
            )
            ->addOption(
                'all',
                null,
                InputOption::VALUE_NONE,
                'Prune everything disposable: trees, volumes, and snapshots',
            )
            ->addOption(
                'older-than',
                null,
                InputOption::VALUE_REQUIRED,
                'Only prune items unused for at least this long (e.g. 30d, 12h); items of unknown age are then '
                . 'excluded',
            )
            ->addOption(
                'keep-latest',
                null,
                InputOption::VALUE_REQUIRED,
                'Snapshots: keep this many newest snapshots per project regardless of age',
                '0',
            )
            ->addOption(
                'yes',
                'y',
                InputOption::VALUE_NONE,
                'Actually delete. Without this flag the command is a dry run and deletes NOTHING',
            )
            ->setHelp(<<<'HELP'
                Dry-run by default: without <info>--yes</info> the command only lists deletion candidates
                with their reclaimable sizes. Protected regardless of any flag combination:

                  * base artifacts (<cockpit>/base-artifacts/) — canonical, expensive to rebuild
                  * committed fixture dumps (module tests/fixtures/*.sql.gz, <cockpit>/fixtures/*.sql.gz)
                  * keep-marked items: touch <comment><project>/.keep</comment> to keep a whole environment, or
                    <comment><artifact>.keep</comment> (e.g. materialized/<name>.sql.keep) to keep one snapshot.

                Environments are always disposed through the engine adapter (containers and
                named volumes released with the tree); pruned state regenerates on demand.
                HELP);

        $this->addCockpitOption()->addProjectsRootOption();
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $scope = self::scopeFromFlags($input);
        if ($scope === null) {
            throw new WorkflowException('Pick exactly one prune scope: --trees, --snapshots, --projects, or --all.');
        }

        try {
            $olderThan = $input->getOption('older-than') !== null
                ? Duration::parseToSeconds((string) $input->getOption('older-than'))
                : null;
        } catch (\InvalidArgumentException $e) {
            throw new WorkflowException($e->getMessage(), 0, $e);
        }
        $keepLatest = (int) $input->getOption('keep-latest');

        $cockpit = $this->cockpit($input);
        // Parsed up front, before anything is scanned or any reclaim plan is
        // shown: this is the only destructive command, and discovering a
        // malformed registry halfway through would abort a run the operator
        // has already been shown a plan for.
        $modules = $this->modules($cockpit);
        $projectsRoot = ProjectsRoot::resolve(self::stringOption($input, 'projects-root'), $cockpit->root);

        $scanner = new InventoryScanner($cockpit, $projectsRoot);
        $items = $scanner->scan();
        // An under-reported inventory can only under-delete, so the run
        // continues — but the operator is told what could not be looked at.
        foreach ($scanner->warnings() as $warning) {
            $io->warning($warning);
        }

        if (\in_array(Category::ProjectVolume, $scope->categories(), true)) {
            $items = [...$items, ...$this->volumeProbe->itemsForInventory($items)];
        }

        $selector = new PruneSelector($scanner->protectedRoots());
        $candidates = $selector->select($items, $scope, $olderThan, new \DateTimeImmutable(), $keepLatest);

        if ($candidates === []) {
            $io->writeln('Nothing to prune: no unprotected items match the given scope and filters.');

            return ExitCode::OK;
        }

        $now = new \DateTimeImmutable();
        $io->table(
            ['Category', 'Module', 'Core', 'Item', 'Age', 'Reclaimable'],
            array_map(static fn (InventoryItem $i) => [
                $i->category->label(),
                $i->module ?? '-',
                $i->coreMajor ?? '-',
                $i->path,
                $i->ageSeconds($now) === null ? '?' : sprintf('%dd', intdiv((int) $i->ageSeconds($now), 86400)),
                ByteFormat::human($i->sizeBytes),
            ], $candidates),
        );
        $total = array_sum(array_map(static fn (InventoryItem $i) => $i->sizeBytes, $candidates));
        $io->writeln(sprintf(
            'Total reclaimable: %s across %d item(s).',
            ByteFormat::human($total),
            \count($candidates),
        ));

        if (!$input->getOption('yes')) {
            $io->writeln('');
            $io->writeln('<comment>Dry run: nothing was deleted. Re-run with --yes to reclaim.</comment>');

            return ExitCode::OK;
        }

        $adapter = $this->engines->create(
            $cockpit,
            self::stringOption($input, 'projects-root'),
            static fn (string $line) => $io->writeln($line),
            static fn (string $line) => $io->writeln($line),
        );

        $outcome = (new PruneExecutor(
            $selector,
            $adapter,
            $modules,
            static fn (string $line) => $io->writeln($line),
        ))->execute($candidates);

        foreach ($outcome->skipped as [$item, $reason]) {
            $io->warning(sprintf('Skipped %s: %s', $item->path, $reason));
        }
        $io->success(sprintf(
            'Pruned %d item(s), reclaimed %s.',
            \count($outcome->deleted),
            ByteFormat::human($outcome->freedBytes),
        ));

        return ExitCode::OK;
    }

    private static function scopeFromFlags(InputInterface $input): ?PruneScope
    {
        $picked = array_values(array_filter(
            [PruneScope::Trees, PruneScope::Snapshots, PruneScope::Projects, PruneScope::All],
            static fn (PruneScope $scope): bool => (bool) $input->getOption($scope->value),
        ));

        return \count($picked) === 1 ? $picked[0] : null;
    }
}

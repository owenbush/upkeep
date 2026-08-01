<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\DdevContribAdapter;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\ProcessRunner;
use Upkeep\Adapter\ProjectsRoot;
use Upkeep\Adapter\VolumeProbe;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Maintenance\ByteFormat;
use Upkeep\Maintenance\Category;
use Upkeep\Maintenance\Duration;
use Upkeep\Maintenance\InventoryItem;
use Upkeep\Maintenance\InventoryScanner;
use Upkeep\Maintenance\PruneExecutor;
use Upkeep\Maintenance\PruneScope;
use Upkeep\Maintenance\PruneSelector;

#[AsCommand(
    name: 'prune',
    description: 'Reclaim disposable state (environment trees, engine projects, materialized snapshots). Dry-run by default; never touches base artifacts, keep-marked items, or committed fixture dumps.',
)]
final class PruneCommand extends Command
{
    public function __construct(
        private readonly ?EngineAdapterInterface $adapter = null,
        private readonly ?VolumeProbe $volumeProbe = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this
            ->addOption('trees', null, InputOption::VALUE_NONE, 'Prune disposable environment trees (disposed via the adapter teardown, which also releases the engine project)')
            ->addOption('snapshots', null, InputOption::VALUE_NONE, 'Prune materialized fixture snapshots (their committed .sql.gz dumps can rebuild them at any time)')
            ->addOption('projects', null, InputOption::VALUE_NONE, 'Prune whole engine projects: trees plus their docker named volumes, via the adapter teardown')
            ->addOption('all', null, InputOption::VALUE_NONE, 'Prune everything disposable: trees, volumes, and snapshots')
            ->addOption('older-than', null, InputOption::VALUE_REQUIRED, 'Only prune items unused for at least this long (e.g. 30d, 12h); items of unknown age are then excluded')
            ->addOption('keep-latest', null, InputOption::VALUE_REQUIRED, 'Snapshots: keep this many newest snapshots per project regardless of age', '0')
            ->addOption('yes', 'y', InputOption::VALUE_NONE, 'Actually delete. Without this flag the command is a dry run and deletes NOTHING')
            ->addOption('cockpit', null, InputOption::VALUE_REQUIRED, sprintf('Path to the cockpit directory (defaults to $%s, then the current directory)', Cockpit::ENV_VAR))
            ->addOption('projects-root', null, InputOption::VALUE_REQUIRED, sprintf('Directory holding the engine environments (defaults to $%s, then ~/.upkeep/projects)', ProjectsRoot::ENV_VAR))
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
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        $scope = self::scopeFromFlags($input);
        if ($scope === null) {
            $io->error('Pick exactly one prune scope: --trees, --snapshots, --projects, or --all.');

            return Command::FAILURE;
        }

        try {
            $olderThan = $input->getOption('older-than') !== null ? Duration::parseToSeconds((string) $input->getOption('older-than')) : null;
        } catch (\InvalidArgumentException $e) {
            $io->error($e->getMessage());

            return Command::FAILURE;
        }
        $keepLatest = (int) $input->getOption('keep-latest');

        $cockpit = Cockpit::resolve($input->getOption('cockpit'));
        if (!file_exists($cockpit->registryPath())) {
            $io->error(sprintf('No cockpit found at "%s" (missing %s). Run `upkeep init` first.', $cockpit->root, Cockpit::REGISTRY_FILENAME));

            return Command::FAILURE;
        }
        $projectsRoot = ProjectsRoot::resolve($input->getOption('projects-root'));

        $scanner = new InventoryScanner($cockpit, $projectsRoot);
        $items = $scanner->scan();

        if (\in_array(Category::ProjectVolume, $scope->categories(), true)) {
            $trees = [];
            foreach ($items as $item) {
                if ($item->category === Category::ProjectTree && $item->projectName !== null) {
                    $trees[$item->projectName] = $item;
                }
            }
            $probe = $this->volumeProbe ?? VolumeProbe::withRunner(new ProcessRunner(static function (string $line): void {
            }));
            $items = [...$items, ...$probe->items($trees)];
        }

        $selector = new PruneSelector($scanner->protectedRoots());
        $candidates = $selector->select($items, $scope, $olderThan, new \DateTimeImmutable(), $keepLatest);

        if ($candidates === []) {
            $io->writeln('Nothing to prune: no unprotected items match the given scope and filters.');

            return Command::SUCCESS;
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
        $io->writeln(sprintf('Total reclaimable: %s across %d item(s).', ByteFormat::human($total), \count($candidates)));

        if (!$input->getOption('yes')) {
            $io->writeln('');
            $io->writeln('<comment>Dry run: nothing was deleted. Re-run with --yes to reclaim.</comment>');

            return Command::SUCCESS;
        }

        $registry = $cockpit->loadRegistry();
        $adapter = $this->adapter ?? new DdevContribAdapter(
            new ArtifactLayout($cockpit->baseArtifactsPath()),
            $projectsRoot,
            new ProcessRunner(static fn (string $line) => $io->writeln($line)),
            static fn (string $line) => $io->writeln($line),
        );

        $outcome = (new PruneExecutor($selector, $adapter, $registry->modules(), static fn (string $line) => $io->writeln($line)))
            ->execute($candidates);

        foreach ($outcome->skipped as [$item, $reason]) {
            $io->warning(sprintf('Skipped %s: %s', $item->path, $reason));
        }
        $io->success(sprintf('Pruned %d item(s), reclaimed %s.', \count($outcome->deleted), ByteFormat::human($outcome->freedBytes)));

        return Command::SUCCESS;
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

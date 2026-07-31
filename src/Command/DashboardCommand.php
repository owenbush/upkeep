<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Helper\Table;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Cockpit\RegistryException;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Dashboard\RowAssembler;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Results\ResultsCache;

/**
 * One table of every open MR across all registered modules and tracked core
 * versions: MODULE, MR, CORE, TITLE, CI (live from GitLab), LOCAL (cached
 * check results), STATUS (fast-lane gate verdict).
 *
 * Strictly read-only aggregation: registry + GET-backed client calls + the
 * results cache. It never provisions environments, never runs checks, and
 * never mutates anything — the gate's verdicts are consumed by the human (and
 * by the merge command), not acted on here.
 *
 * Thin wiring by design: row assembly lives in Dashboard\RowAssembler
 * (shared with the fast-lane merge command), classification in FastLaneGate
 * (exhaustively unit-tested), staleness semantics in ResultsCache/
 * CachedResult, typed failures in GitlabClient. This command renders a table;
 * its tests cover row assembly, the --version filter, and failure cells.
 *
 * NOTE for application wiring: the --version option collides with Symfony
 * Console's built-in application-level --version/-V. The hosting Application
 * must drop that default option before running this command (same convention
 * as AbstractMrCommand — see its docblock for the snippet).
 */
#[AsCommand(
    name: 'dashboard',
    description: 'Show every open MR across registered modules and core versions with CI, local check, and fast-lane status.',
)]
final class DashboardCommand extends Command
{
    /** @param ?GitlabClient $client injected in tests; built from the resolved token otherwise */
    public function __construct(private readonly ?GitlabClient $client = null)
    {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addOption(
            'cockpit',
            null,
            InputOption::VALUE_REQUIRED,
            sprintf('Path to the cockpit directory (defaults to $%s, then the current directory)', Cockpit::ENV_VAR),
        );
        $this->addOption(
            'version',
            null,
            InputOption::VALUE_REQUIRED,
            'Only show rows targeting this core major version (e.g. 11)',
        );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        // Diagnostics go to stderr; stdout carries only the table.
        $io = new SymfonyStyle(
            $input,
            $output instanceof ConsoleOutputInterface ? $output->getErrorOutput() : $output,
        );

        $cockpit = Cockpit::resolve($input->getOption('cockpit'));
        try {
            $registry = $cockpit->loadRegistry();
        } catch (RegistryException $e) {
            $io->error($e->getMessage());

            return Command::FAILURE;
        }

        $client = $this->client ?? $this->buildClient($io);
        if ($client === null) {
            return Command::FAILURE;
        }

        $versionFilter = $input->getOption('version');
        $versionFilter = $versionFilter === null ? null : (string) $versionFilter;

        $assembler = new RowAssembler($client, new ResultsCache($cockpit->root . '/results'));
        $rows = $assembler->assemble($registry->modules(), $versionFilter);

        if ($rows === []) {
            $io->note(
                $versionFilter === null
                    ? 'No open merge requests across the registered modules.'
                    : sprintf('No open merge requests targeting core %s across the registered modules.', $versionFilter),
            );

            return Command::SUCCESS;
        }

        $table = new Table($output);
        $table->setHeaders(['MODULE', 'MR', 'CORE', 'TITLE', 'CI', 'LOCAL', 'STATUS']);
        $table->setRows(array_map(static fn (DashboardRow $row): array => $row->toTableCells(), $rows));
        $table->render();

        return Command::SUCCESS;
    }

    private function buildClient(SymfonyStyle $io): ?GitlabClient
    {
        $resolver = new TokenResolver();
        $token = $resolver->resolve();
        if ($token === null) {
            $io->error(sprintf(
                'No GitLab token found. Configure one of: %s. (The token is never printed or logged.)',
                $resolver->describeSources(),
            ));

            return null;
        }

        return new GitlabClient(HttpClient::create(), $token);
    }
}

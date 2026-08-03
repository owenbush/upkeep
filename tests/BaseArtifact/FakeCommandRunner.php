<?php

declare(strict_types=1);

namespace Upkeep\Tests\BaseArtifact;

use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\CapturedProcess;
use Upkeep\Adapter\CommandRunner;

/**
 * Stands in for the adapter's shell-out seam. It performs just enough real
 * filesystem work for the builder to observe the outcomes it checks — a
 * resolved tree with a lock file, an exported dump — and records every command
 * so the order can be asserted. No container runtime, no network, no composer.
 */
final class FakeCommandRunner implements CommandRunner
{
    /** @var list<list<string>> */
    private array $commands = [];

    private ?string $failCommand = null;

    private string $failMessage = '';

    private bool $exportDump = true;

    public function failOn(string $summary, string $message): void
    {
        $this->failCommand = $summary;
        $this->failMessage = $message;
    }

    public function skipDumpExport(): void
    {
        $this->exportDump = false;
    }

    /** @return list<list<string>> */
    public function commands(): array
    {
        return $this->commands;
    }

    /** @return list<string> the first two words of each command, in order */
    public function summaries(): array
    {
        return array_map(
            static fn (array $command): string => implode(' ', \array_slice($command, 0, 2)),
            $this->commands,
        );
    }

    public function run(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): string
    {
        $this->commands[] = $command;
        $summary = implode(' ', \array_slice($command, 0, 2));

        if ($summary === $this->failCommand) {
            throw new AdapterException(sprintf('Command failed (1): %s', $this->failMessage));
        }

        return match (true) {
            $summary === 'composer create-project' => $this->resolveTree($command[3]),
            $summary === 'cp -a' => $this->seedThrowaway($command[3]),
            $summary === 'rm -rf' => $this->remove($command[2]),
            $summary === 'ddev exec' => '8.3.30' . "\n",
            $summary === 'ddev describe' => (string) json_encode([
                'raw' => ['dbinfo' => ['database_type' => 'mariadb', 'database_version' => '10.11']],
            ]),
            $summary === 'ddev export-db' => $this->exportDb($command[2]),
            default => '',
        };
    }

    public function tryRun(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): ?string
    {
        try {
            return $this->run($command, $cwd, $timeout);
        } catch (AdapterException) {
            return null;
        }
    }

    public function capture(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): CapturedProcess
    {
        throw new \BadMethodCallException('The base-artifact build never captures.');
    }

    private function resolveTree(string $treePath): string
    {
        mkdir($treePath, 0o755, true);
        file_put_contents($treePath . '/composer.lock', (string) json_encode([
            'packages' => [['name' => 'drupal/core', 'version' => '11.4.4']],
        ]));

        return "Created project in " . $treePath . "\n";
    }

    private function seedThrowaway(string $throwawayPath): string
    {
        mkdir($throwawayPath, 0o755, true);

        return '';
    }

    private function remove(string $path): string
    {
        exec('rm -rf ' . escapeshellarg($path));

        return '';
    }

    private function exportDb(string $fileOption): string
    {
        if ($this->exportDump) {
            file_put_contents(substr($fileOption, \strlen('--file=')), 'gzipped dump bytes');
        }

        return '';
    }
}

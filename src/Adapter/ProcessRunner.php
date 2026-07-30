<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Symfony\Component\Process\Process;

/**
 * Shell-out helper shared by the adapter's engine interactions: streams
 * output lines to the log as they arrive (engine operations are long-running)
 * and turns failures into AdapterException.
 */
final readonly class ProcessRunner
{
    public const int DEFAULT_TIMEOUT = 3600;

    /**
     * @param \Closure(string): void $log
     */
    public function __construct(private \Closure $log)
    {
    }

    /**
     * Runs a command and returns its stdout; throws on failure.
     *
     * @param list<string> $command
     */
    public function run(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): string
    {
        $process = $this->start($command, $cwd, $timeout);

        if (!$process->isSuccessful()) {
            throw new AdapterException(sprintf(
                "Command failed (%s): %s\n%s",
                $process->getExitCode() ?? -1,
                $process->getCommandLine(),
                trim($process->getErrorOutput() . "\n" . $process->getOutput()),
            ));
        }

        return $process->getOutput();
    }

    /**
     * Runs a command tolerating failure: returns stdout on success, null on
     * any non-zero exit. For probes and best-effort cleanup steps.
     *
     * @param list<string> $command
     */
    public function tryRun(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): ?string
    {
        $process = $this->start($command, $cwd, $timeout);

        return $process->isSuccessful() ? $process->getOutput() : null;
    }

    /**
     * @param list<string> $command
     */
    private function start(array $command, ?string $cwd, int $timeout): Process
    {
        $process = new Process($command, $cwd, timeout: $timeout);
        $process->run(function (string $type, string $buffer): void {
            foreach (explode("\n", rtrim($buffer, "\n")) as $line) {
                ($this->log)('  ' . $line);
            }
        });

        return $process;
    }
}

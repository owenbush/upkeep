<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\CapturedProcess;
use Upkeep\Adapter\CommandRunner;

/**
 * A CommandRunner that answers from a script instead of spawning children.
 *
 * It reproduces ProcessRunner's contract exactly — that is the whole point of
 * the seam. The responder decides the outcome of one command:
 *
 *   - a string          the command succeeded, with that stdout;
 *   - null              the command failed, so run() throws and tryRun()
 *                       yields null;
 *   - a CapturedProcess the full outcome, for capture() (exit code, output,
 *                       timeout flag).
 *
 * A responder may also mutate the filesystem, which is how tests reproduce the
 * on-disk effects a real `git clone` or `ddev add-on get` would have had. Every
 * invocation is recorded so tests can assert what was issued, in what order,
 * and against which working directory.
 */
final class ScriptedCommandRunner implements CommandRunner
{
    /** @var list<array{command: list<string>, cwd: ?string, timeout: int}> */
    public array $invocations = [];

    /** @var \Closure(list<string>, ?string): (string|CapturedProcess|null) */
    private \Closure $responder;

    /**
     * @param ?\Closure(list<string>, ?string): (string|CapturedProcess|null) $responder
     *        null answers every command with empty, successful output
     */
    public function __construct(?\Closure $responder = null)
    {
        $this->responder = $responder ?? static fn (): string => '';
    }

    public function run(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): string
    {
        $result = $this->answer($command, $cwd, $timeout);
        if (!\is_string($result)) {
            throw new AdapterException(sprintf('Command failed: %s', implode(' ', $command)));
        }

        return $result;
    }

    public function tryRun(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): ?string
    {
        $result = $this->answer($command, $cwd, $timeout);

        return \is_string($result) ? $result : null;
    }

    public function capture(array $command, ?string $cwd = null, int $timeout = self::DEFAULT_TIMEOUT): CapturedProcess
    {
        $result = $this->answer($command, $cwd, $timeout);
        if ($result instanceof CapturedProcess) {
            return $result;
        }

        return new CapturedProcess(
            exitCode: \is_string($result) ? 0 : 1,
            output: $result ?? '',
            timedOut: false,
            durationSeconds: 0.0,
        );
    }

    /**
     * Every command issued, as a flat "argv joined by spaces" line — the shape
     * assertions read most easily.
     *
     * @return list<string>
     */
    public function commandLines(): array
    {
        return array_map(
            static fn (array $invocation): string => implode(' ', $invocation['command']),
            $this->invocations,
        );
    }

    /**
     * Whether any issued command line contains $needle.
     */
    public function issued(string $needle): bool
    {
        foreach ($this->commandLines() as $line) {
            if (str_contains($line, $needle)) {
                return true;
            }
        }

        return false;
    }

    /**
     * @param list<string> $command
     */
    private function answer(array $command, ?string $cwd, int $timeout): string|CapturedProcess|null
    {
        $this->invocations[] = ['command' => $command, 'cwd' => $cwd, 'timeout' => $timeout];

        return ($this->responder)($command, $cwd);
    }
}

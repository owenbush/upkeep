<?php

declare(strict_types=1);

namespace Upkeep\Tests\Support;

use Symfony\Component\Console\Output\BufferedOutput;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\ConsoleSectionOutput;
use Symfony\Component\Console\Output\OutputInterface;

/**
 * A ConsoleOutputInterface with both streams held in memory.
 *
 * Several commands promise that stdout carries only their machine-readable
 * payload — a path, a table, paste-ready Markdown — and that every diagnostic
 * goes to stderr, so `cd $(upkeep env:path widget)` cannot capture an error
 * message into the path. That promise is only observable against an output
 * that *has* two streams: with a plain BufferedOutput the two are the same
 * buffer and the split is invisible, which is why it went unasserted.
 *
 * Symfony's own ConsoleOutput writes to the real php://stdout, which a test
 * suite must not do, so this stands in for it.
 */
final class SplitConsoleOutput extends BufferedOutput implements ConsoleOutputInterface
{
    private OutputInterface $stderr;

    public function __construct()
    {
        parent::__construct(self::VERBOSITY_NORMAL, false);
        $this->stderr = new BufferedOutput(self::VERBOSITY_NORMAL, false);
    }

    public function getErrorOutput(): OutputInterface
    {
        return $this->stderr;
    }

    public function setErrorOutput(OutputInterface $error): void
    {
        $this->stderr = $error;
    }

    public function section(): ConsoleSectionOutput
    {
        throw new \BadMethodCallException('No upkeep command uses section output.');
    }

    /** Everything written to stderr, and empties the buffer. */
    public function fetchErrors(): string
    {
        return $this->stderr instanceof BufferedOutput ? $this->stderr->fetch() : '';
    }
}

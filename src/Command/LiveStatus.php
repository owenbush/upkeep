<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Terminal;

/**
 * What ddev, composer and git are doing right now, on one line that keeps
 * overwriting itself.
 *
 * Child-process output used to be all or nothing. All of it at normal
 * verbosity buried the few lines a command exists to print — a fresh provision
 * is hundreds of lines of clone, config, composer and site install, and `dev`'s
 * four-line answer scrolled off the top. None of it, the fix for that, made a
 * wedged `ddev start` indistinguishable from a slow one: after a reboot it
 * stalled on "Starting Mutagen sync process..." and upkeep said "starting it"
 * and then nothing at all.
 *
 * Both of those were the same mistake — treating *progress* like *payload*.
 * Progress wants to be seen while it is happening and not afterwards. So on a
 * terminal the latest line shows here, replacing the one before, and it is
 * cleared the moment the child process ends; under -v every line is kept, as
 * before; and anywhere that is not a terminal, nothing extra is written,
 * because overwriting a line in a CI log produces escape codes, not progress.
 *
 * The indicator advances only when output arrives. A frozen one is the point:
 * that is what waiting on Mutagen looks like, and a spinner on a timer would
 * have kept turning while nothing happened.
 */
final class LiveStatus
{
    private const FRAMES = ['⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'];

    /** Carriage return, then erase the whole line. */
    private const ERASE = "\r\033[2K";

    private bool $shown = false;

    private int $frame = 0;

    public function __construct(
        private readonly OutputInterface $output,
        private readonly ?int $width = null,
    ) {
    }

    /**
     * One line of child output.
     *
     * The line arrives redacted and indented by ProcessRunner. Under -v it is
     * written as it always was; on a terminal it becomes the status.
     */
    public function line(string $line): void
    {
        if ($this->output->isVerbose()) {
            $this->output->writeln($line);

            return;
        }

        if (!$this->live()) {
            return;
        }

        $text = self::displayable($line);
        if ($text === '') {
            return;
        }

        $prefix = '  ' . self::FRAMES[$this->frame] . ' ';
        $this->frame = ($this->frame + 1) % \count(self::FRAMES);

        // One column short of the width: a line that reaches the last column
        // makes most terminals wrap, and a carriage return then only erases
        // the second half, leaving the first half behind on every update.
        $room = max(1, $this->columns() - mb_strwidth($prefix) - 1);

        $this->output->write(
            self::ERASE . $prefix . mb_strimwidth($text, 0, $room, '…'),
            false,
            OutputInterface::OUTPUT_RAW,
        );
        $this->shown = true;
    }

    /**
     * Removes the status line, if one is showing.
     *
     * Called when each child process ends — the one moment it is certain
     * nothing else is mid-print. Every other write, stage narration included,
     * happens between processes, so nothing can land glued onto the end of it.
     */
    public function clear(): void
    {
        if (!$this->shown) {
            return;
        }

        $this->output->write(self::ERASE, false, OutputInterface::OUTPUT_RAW);
        $this->shown = false;
    }

    private function live(): bool
    {
        return $this->output->isDecorated() && !$this->output->isQuiet();
    }

    private function columns(): int
    {
        return $this->width ?? (new Terminal())->getWidth();
    }

    /**
     * The part of a line worth showing on one line.
     *
     * Docker and composer redraw progress with bare carriage returns inside a
     * single line, so only the last segment is current. Colour codes are
     * removed because they have no width and would throw the truncation off,
     * and any other control character because it would move the cursor.
     */
    private static function displayable(string $line): string
    {
        $segments = array_values(array_filter(
            explode("\r", $line),
            static fn (string $segment): bool => trim($segment) !== '',
        ));
        $current = $segments === [] ? '' : $segments[\count($segments) - 1];

        $withoutColour = preg_replace('/\e\[[0-9;?]*[A-Za-z]/', '', $current) ?? $current;
        $printable = preg_replace('/[\x00-\x1F\x7F]/', '', $withoutColour) ?? $withoutColour;

        return trim($printable);
    }
}

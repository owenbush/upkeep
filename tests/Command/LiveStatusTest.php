<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Output\BufferedOutput;
use Symfony\Component\Console\Output\OutputInterface;
use Upkeep\Command\LiveStatus;

/**
 * Child-process output as one live line on a terminal.
 *
 * All of it at normal verbosity buried the few lines a command exists to print;
 * none of it made a stalled `ddev start` look exactly like a slow one — after a
 * reboot it sat on "Starting Mutagen sync process..." and upkeep said nothing.
 * Progress wants to be visible while it happens and gone afterwards.
 */
final class LiveStatusTest extends TestCase
{
    private const ERASE = "\r\033[2K";

    private static function terminal(int $verbosity = OutputInterface::VERBOSITY_NORMAL): BufferedOutput
    {
        return new BufferedOutput($verbosity, true);
    }

    public function testOnATerminalTheLatestLineReplacesTheLastOne(): void
    {
        $out = self::terminal();
        $status = new LiveStatus($out, 80);

        $status->line('  Starting upkeep-widget-d11...');
        $status->line('  Starting Mutagen sync process...');

        $written = $out->fetch();
        self::assertStringContainsString(self::ERASE, $written);
        self::assertStringContainsString('Starting Mutagen sync process...', $written);
        self::assertStringNotContainsString("\n", $written, 'one line, overwritten in place');
    }

    /**
     * Cleared when the child exits, which is the only moment nothing else can
     * be mid-print — so the next stage line never lands glued onto it.
     */
    public function testClearingRemovesTheLineAndIsSafeWhenNothingIsShown(): void
    {
        $out = self::terminal();
        $status = new LiveStatus($out, 80);

        $status->clear();
        self::assertSame('', $out->fetch(), 'nothing to clear writes nothing');

        $status->line('  Updating dependencies');
        $out->fetch();

        $status->clear();
        self::assertSame(self::ERASE, $out->fetch());

        $status->clear();
        self::assertSame('', $out->fetch(), 'and only once');
    }

    /** Under -v every line is kept, exactly as before. */
    public function testVerboseKeepsTheWholeTranscript(): void
    {
        $out = self::terminal(OutputInterface::VERBOSITY_VERBOSE);
        $status = new LiveStatus($out, 80);

        $status->line('  one');
        $status->line('  two');
        $status->clear();

        self::assertSame("  one\n  two\n", $out->fetch());
    }

    /**
     * Not a terminal — a pipe or a CI log — means nothing extra. Overwriting a
     * line there produces escape codes, not progress.
     */
    public function testAnywhereThatIsNotATerminalGetsNothingExtra(): void
    {
        $piped = new BufferedOutput(OutputInterface::VERBOSITY_NORMAL, false);
        $quiet = new BufferedOutput(OutputInterface::VERBOSITY_QUIET, true);

        foreach ([$piped, $quiet] as $out) {
            $status = new LiveStatus($out, 80);
            $status->line('  Updating dependencies');
            $status->clear();

            self::assertSame('', $out->fetch());
        }
    }

    /**
     * Cut to fit, one column short. A line reaching the last column makes most
     * terminals wrap, and the carriage return then only erases the second
     * half, leaving the first behind on every update.
     */
    public function testALongLineIsCutToFitShortOfTheLastColumn(): void
    {
        $out = self::terminal();
        (new LiveStatus($out, 30))->line('  ' . str_repeat('x', 200));

        $visible = substr($out->fetch(), \strlen(self::ERASE));

        self::assertLessThan(30, mb_strwidth($visible));
        self::assertStringEndsWith('…', $visible);
    }

    /**
     * What a terminal would actually show from a line: only the last
     * carriage-return segment, which is how docker and composer redraw
     * progress, and no colour or control codes, which have no width and would
     * throw the fit off.
     */
    public function testOnlyTheCurrentPrintableTextOfALineIsShown(): void
    {
        $out = self::terminal();
        $status = new LiveStatus($out, 80);

        $status->line("  Pulling 10%\rPulling 55%\r\e[32mPulling 90%\e[0m\x07");

        $written = $out->fetch();
        self::assertStringContainsString('Pulling 90%', $written);
        self::assertStringNotContainsString('55%', $written);
        self::assertStringNotContainsString("\e[32m", $written);
        self::assertStringNotContainsString("\x07", $written);
    }

    /** A blank line leaves the current status alone rather than blanking it. */
    public function testABlankLineDoesNotReplaceTheStatus(): void
    {
        $out = self::terminal();
        $status = new LiveStatus($out, 80);

        $status->line('  Installing drupal/core');
        $out->fetch();
        $status->line('   ');
        $status->line("\r\r");

        self::assertSame('', $out->fetch());
    }

    /**
     * The indicator moves only when output arrives. A frozen one is the
     * point: that is what waiting on Mutagen looks like, and a spinner on a
     * timer would keep turning while nothing happened.
     */
    public function testTheIndicatorAdvancesPerLineNotOnATimer(): void
    {
        $out = self::terminal();
        $status = new LiveStatus($out, 80);

        $status->line('  a');
        $first = self::indicator($out->fetch());
        $status->line('  b');
        $second = self::indicator($out->fetch());

        self::assertNotSame('', $first);
        self::assertNotSame($first, $second);
    }

    /** The indicator glyph from one write: after the erase and the indent. */
    private static function indicator(string $written): string
    {
        return mb_substr(ltrim(substr($written, \strlen(self::ERASE))), 0, 1);
    }
}

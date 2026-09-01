<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Ui\LaunchToken;
use Upkeep\Ui\UiServer;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\WorkflowException;

/**
 * Serve the cockpit as a page in the operator's browser.
 *
 * A second renderer over the same core, not a second tool: what it shows comes
 * from `Dashboard\RowFactory` and what it *does* is run the `upkeep` binary as
 * a subprocess. The CLI remains the only implementation of every behaviour;
 * the UI is a way to look at it and to press its buttons.
 *
 * Runs in the foreground until interrupted, and binds to the loopback
 * interface only. There is no daemon, no pid file and no port left listening
 * after Ctrl-C — the design doc's "not a hosted service" line is where this
 * stops, and a local process the operator can see is on the right side of it.
 */
#[AsCommand(
    name: 'ui',
    description: 'Serve the cockpit dashboard in a browser (localhost only; runs until interrupted).',
)]
final class UiCommand extends UpkeepCommand
{
    /**
     * @param ?\Closure(string, list<string>, array<string, string>): int $serve
     *        injected in tests; runs the server process and returns its exit code
     * @param ?\Closure(string, int): bool $portInUse injected in tests, so no
     *        test has to bind a real port to exercise the collision
     */
    public function __construct(
        private readonly ?\Closure $serve = null,
        private readonly ?\Closure $portInUse = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addCockpitOption()
            ->addOption(
                'port',
                null,
                InputOption::VALUE_REQUIRED,
                'Port to listen on (default: ' . UiServer::DEFAULT_PORT . ')',
            )
            ->addNoOpenOption();
    }

    /** The URL is the payload; everything else is diagnostics. */
    protected function diagnosticsOnStderr(): bool
    {
        return true;
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $cockpit = $this->requireCockpit($input);
        $port = self::port($input);

        // Before anything is minted or printed. A busy port means the server
        // cannot bind, but the port still answers — from the previous run,
        // with the previous token. Printing a launch URL first would hand the
        // operator a link that was dead the moment it was written.
        $occupied = $this->portInUse ?? UiServer::isPortInUse(...);
        if ($occupied(UiServer::HOST, $port) === true) {
            throw new WorkflowException(sprintf(
                "Something is already listening on %s:%d.\n\n"
                . "If it is another `upkeep ui`, that one still owns the port and its link is the one that "
                . "works — its token is the only one it accepts. Either use the URL it printed, or stop it "
                . "(Ctrl-C in its terminal) and run this again to get a fresh one.\n\n"
                . 'If it is something else, pick another port: upkeep ui --port=%d',
                UiServer::HOST,
                $port,
                $port + 1,
            ));
        }

        $token = LaunchToken::mint();
        $url = $token->launchUrl(UiServer::HOST, $port);

        $io->writeln(sprintf('upkeep ui on <href=%s>%s</>', $url, $url));
        $io->writeln('');
        $io->writeln('<fg=gray>Bound to ' . UiServer::HOST . ' only. The link carries a one-time token for this</>');
        $io->writeln('<fg=gray>run — anyone without it gets a 404. Press Ctrl-C to stop.</>');

        if ($input->getOption('no-open') !== true) {
            BrowserOpener::open($url);
        }

        $server = new UiServer($token, $cockpit, $port);
        $serve = $this->serve ?? $server->runner();

        // Ctrl-C reaches the child too, so the command ends when the server
        // does. A non-zero exit here is the server failing to start — almost
        // always the port already being in use.
        $exit = $serve($server->documentRoot(), $server->arguments(), $server->environment());

        return $exit === 0 ? ExitCode::OK : ExitCode::INFRASTRUCTURE;
    }

    /**
     * @throws WorkflowException when the port is not one
     */
    private static function port(InputInterface $input): int
    {
        $raw = self::stringOption($input, 'port');
        if ($raw === null) {
            return UiServer::DEFAULT_PORT;
        }
        if (preg_match('/^\d+$/', $raw) !== 1 || (int) $raw < 1024 || (int) $raw > 65535) {
            throw new WorkflowException(sprintf(
                '--port must be a number between 1024 and 65535, got "%s".',
                $raw,
            ));
        }

        return (int) $raw;
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Command\NotesCommand;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Workflow\ExitCode;

/**
 * `notes` is wiring: its behaviour lives in NotesGenerator and GitlabClient,
 * both unit-tested. What is pinned here is the wiring itself — the command
 * shipped with an unimported GitlabClientFactory, so every invocation died
 * with a fatal "class not found" before it could reach the token check. One
 * hermetic run through perform() is enough to keep that from recurring.
 */
final class NotesCommandTest extends TestCase
{
    private string $home;
    private string|false $originalHome;
    private string|false $originalXdgConfigHome;
    private string|false $originalToken;

    protected function setUp(): void
    {
        $this->home = (string) realpath(sys_get_temp_dir()) . '/upkeep-notes-cmd-test-' . bin2hex(random_bytes(4));
        mkdir($this->home, 0700, true);

        // No token from any source, and no chance of reading the real one:
        // both the env var and every path the resolver derives from $HOME /
        // $XDG_CONFIG_HOME point into the temp world.
        $this->originalHome = getenv('HOME');
        $this->originalXdgConfigHome = getenv('XDG_CONFIG_HOME');
        $this->originalToken = getenv(TokenResolver::DEFAULT_ENV_VAR);
        putenv('HOME=' . $this->home);
        putenv('XDG_CONFIG_HOME=' . $this->home . '/.config');
        putenv(TokenResolver::DEFAULT_ENV_VAR);
    }

    protected function tearDown(): void
    {
        putenv($this->originalHome === false ? 'HOME' : 'HOME=' . $this->originalHome);
        putenv(
            $this->originalXdgConfigHome === false
                ? 'XDG_CONFIG_HOME'
                : 'XDG_CONFIG_HOME=' . $this->originalXdgConfigHome,
        );
        putenv(
            $this->originalToken === false
                ? TokenResolver::DEFAULT_ENV_VAR
                : TokenResolver::DEFAULT_ENV_VAR . '=' . $this->originalToken,
        );
        exec('rm -rf ' . escapeshellarg($this->home));
    }

    /**
     * Reaching the token check at all is the point: it is the first thing
     * past the module resolution the command previously never survived. No
     * token configured is an infrastructure failure, reported with the shared
     * CLI-wide wording and without any token material.
     */
    public function testWithoutATokenItReportsTheSharedGuidanceAndExitsInfrastructure(): void
    {
        $tester = new CommandTester(new NotesCommand());
        $exitCode = $tester->execute(['module' => 'conditions_helper', '--cockpit' => $this->home]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exitCode);
        self::assertStringContainsString('No GitLab token found', $tester->getDisplay());
    }
}

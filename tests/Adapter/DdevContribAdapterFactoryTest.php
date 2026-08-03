<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\DdevContribAdapterFactory;
use Upkeep\Adapter\Environment;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Security\SecretRedactor;

/**
 * The one place that assembles a concrete engine adapter. What matters is that
 * it resolves the invocation's projects root (enforcing the $HOME containment
 * rule) and threads the composition root's redactor into the runner every child
 * process line passes through — a factory that dropped the redactor would leak
 * a token into a log with nothing else to catch it.
 */
final class DdevContribAdapterFactoryTest extends TestCase
{
    private const SENTINEL = 'SENTINEL-TOKEN-VALUE-9f3a1c';

    private string|false $originalHome;

    private string|false $originalProjectsRoot;

    private string $home;

    protected function setUp(): void
    {
        $this->originalHome = getenv('HOME');
        $this->originalProjectsRoot = getenv('UPKEEP_PROJECTS_ROOT');

        $this->home = (string) realpath(sys_get_temp_dir()) . '/upkeep-factory-' . bin2hex(random_bytes(6));
        mkdir($this->home . '/cockpit', 0o700, true);
        putenv('HOME=' . $this->home);
        putenv('UPKEEP_PROJECTS_ROOT');
    }

    protected function tearDown(): void
    {
        putenv($this->originalHome === false ? 'HOME' : 'HOME=' . $this->originalHome);
        putenv(
            $this->originalProjectsRoot === false
                ? 'UPKEEP_PROJECTS_ROOT'
                : 'UPKEEP_PROJECTS_ROOT=' . $this->originalProjectsRoot,
        );
        exec('rm -rf ' . escapeshellarg($this->home));
    }

    public function testTheAdapterItBuildsUsesTheResolvedProjectsRoot(): void
    {
        $adapter = (new DdevContribAdapterFactory())->create(
            new Cockpit($this->home . '/cockpit'),
            $this->home . '/envs',
            static function (string $line): void {
            },
            static function (string $line): void {
            },
        );

        // The resolved projects root is where the adapter looks for
        // environments — observable through the path it reports.
        mkdir($this->home . '/envs/upkeep-widget-d11', 0o700, true);
        file_put_contents($this->home . '/envs/upkeep-widget-d11/.upkeep-env.yml', "module: widget\n");

        self::assertSame($this->home . '/envs/upkeep-widget-d11', $adapter->resolveEnvPath('widget', '11'));
        self::assertNull($adapter->resolveEnvPath('other', '11'));
    }

    /**
     * The factory owns the redactor decision for the whole invocation. If it
     * did not thread the composition root's redactor into the ProcessRunner,
     * a secret spliced into a command line would reach the exception message
     * verbatim, with nothing downstream to catch it.
     */
    public function testTheInjectedRedactorReachesTheRunnerEveryChildProcessGoesThrough(): void
    {
        // The environment path carries the secret, so it appears in the
        // command line of every git invocation made against the working copy.
        $environmentPath = $this->home . '/' . self::SENTINEL . '/upkeep-widget-d11';
        mkdir($environmentPath . '/module', 0o700, true);

        $adapter = (new DdevContribAdapterFactory(new SecretRedactor(self::SENTINEL)))->create(
            new Cockpit($this->home . '/cockpit'),
            $this->home . '/' . self::SENTINEL,
            static function (string $line): void {
            },
            static function (string $line): void {
            },
        );

        try {
            // Not a git repository, so the checkout fails, the fetch that
            // follows fails too, and the failure message carries the command
            // line the secret was part of.
            $adapter->checkoutBranch(new Environment(
                moduleName: 'widget',
                coreMajor: '11',
                projectName: 'upkeep-widget-d11',
                projectPath: $environmentPath,
                primaryUrl: 'https://upkeep-widget-d11.ddev.site',
                reused: true,
            ), '1.0.x');
            self::fail('Expected the branch checkout to fail outside a repository.');
        } catch (AdapterException $e) {
            self::assertStringContainsString(SecretRedactor::MASK, $e->getMessage());
            self::assertStringNotContainsString(self::SENTINEL, $e->getMessage());
        }
    }

    public function testAProjectsRootOutsideTheHomeDirectoryIsRefused(): void
    {
        $outside = (string) realpath(sys_get_temp_dir()) . '/upkeep-outside-' . bin2hex(random_bytes(6));

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('it is outside your home directory');

        (new DdevContribAdapterFactory())->create(
            new Cockpit($this->home . '/cockpit'),
            $outside,
            static function (string $line): void {
            },
            static function (string $line): void {
            },
        );
    }
}

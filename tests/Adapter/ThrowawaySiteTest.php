<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\ThrowawaySite;

/**
 * The engine mechanics behind a base-artifact build. Everything the throwaway
 * site does to the outside world goes through the CommandRunner seam, so the
 * build sequence and the install-environment identity it reports are verified
 * here without a container runtime.
 */
final class ThrowawaySiteTest extends TestCase
{
    private const PROJECT = 'upkeep-throwaway-11';

    private string $root;

    /** @var list<string> */
    private array $log = [];

    protected function setUp(): void
    {
        $this->root = (string) realpath(sys_get_temp_dir()) . '/upkeep-throwaway-' . bin2hex(random_bytes(6));
        mkdir($this->root, 0o700, true);
        $this->log = [];
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->root));
    }

    private function site(ScriptedCommandRunner $runner): ThrowawaySite
    {
        return new ThrowawaySite($runner, function (string $line): void {
            $this->log[] = $line;
        });
    }

    /**
     * @param array<string, ?string> $overrides
     */
    private static function runner(array $overrides = []): ScriptedCommandRunner
    {
        return new ScriptedCommandRunner(static function (array $command) use ($overrides): ?string {
            $line = implode(' ', $command);
            foreach ($overrides as $needle => $outcome) {
                if (str_contains($line, $needle)) {
                    return $outcome;
                }
            }
            if (str_contains($line, 'php -r echo PHP_VERSION;')) {
                return "8.3.10\n";
            }
            if (str_contains($line, 'ddev describe')) {
                return (string) json_encode([
                    'raw' => ['dbinfo' => ['database_type' => 'mariadb', 'database_version' => '10.11']],
                ]);
            }

            return '';
        });
    }

    public function testTheBuildSeedsInstallsAndExportsInThatOrder(): void
    {
        $runner = self::runner();

        [$php, $db] = $this->site($runner)->cleanInstallAndDump(
            '11',
            $this->root . '/tree',
            $this->root . '/throwaway',
            self::PROJECT,
            $this->root . '/clean-install.sql.gz',
        );

        self::assertSame('8.3.10', $php);
        self::assertSame('mariadb:10.11', $db);

        $lines = $runner->commandLines();
        $steps = ['cp -a', 'ddev config', 'ddev start', 'composer require drush/drush', 'site:install', 'export-db'];
        $order = [];
        foreach ($steps as $step) {
            $found = null;
            foreach ($lines as $index => $line) {
                if (str_contains($line, $step)) {
                    $found = $index;
                    break;
                }
            }
            self::assertNotNull($found, sprintf('Expected the build to issue "%s".', $step));
            $order[] = $found;
        }
        $sorted = $order;
        sort($sorted);
        self::assertSame($sorted, $order, 'Build steps must run in seed -> configure -> start -> install order.');

        // The canonical tree must stay module-free: drush goes into the copy.
        self::assertTrue($runner->issued('composer require drush/drush --no-interaction'));
        self::assertSame(
            $this->root . '/throwaway',
            $runner->invocations[1]['cwd'],
            'Every engine command after the copy runs inside the throwaway project.',
        );
        self::assertTrue($runner->issued('--file=' . $this->root . '/clean-install.sql.gz --gzip=true'));
    }

    /**
     * The DB engine identity goes into the artifact meta and is later compared
     * for skew, so an unreadable or half-shaped description must report
     * "unknown" rather than a partial identity that would compare unequal.
     */
    public function testTheDbEngineIdentityDegradesToUnknownRatherThanGuessing(): void
    {
        $cases = [
            'unparseable describe output' => ['describe' => 'not json', 'expected' => 'unknown'],
            'no database type' => [
                'describe' => (string) json_encode(['raw' => ['dbinfo' => ['database_version' => '10.11']]]),
                'expected' => 'unknown',
            ],
            'type without version' => [
                'describe' => (string) json_encode(['raw' => ['dbinfo' => ['database_type' => 'postgres']]]),
                'expected' => 'postgres',
            ],
        ];

        foreach ($cases as $name => $case) {
            [, $db] = $this->site(self::runner(['ddev describe' => $case['describe']]))->cleanInstallAndDump(
                '11',
                $this->root . '/tree',
                $this->root . '/throwaway',
                self::PROJECT,
                $this->root . '/clean-install.sql.gz',
            );

            self::assertSame($case['expected'], $db, $name);
        }
    }

    public function testAFailingBuildStepAbortsTheBuild(): void
    {
        $this->expectException(AdapterException::class);

        $this->site(self::runner(['site:install' => null]))->cleanInstallAndDump(
            '11',
            $this->root . '/tree',
            $this->root . '/throwaway',
            self::PROJECT,
            $this->root . '/clean-install.sql.gz',
        );
    }

    public function testTeardownDeletesTheEngineProjectBeforeTheTree(): void
    {
        mkdir($this->root . '/throwaway', 0o700, true);
        $runner = self::runner();

        $this->site($runner)->teardown($this->root . '/throwaway', self::PROJECT);

        self::assertSame([
            'ddev delete --omit-snapshot --yes ' . self::PROJECT,
            'rm -rf ' . $this->root . '/throwaway',
        ], $runner->commandLines());
    }

    /**
     * A build that failed before `ddev config` registered nothing to delete;
     * the tree still has to go.
     */
    public function testTeardownRemovesTheTreeEvenWhenTheEngineHasNothingToDelete(): void
    {
        mkdir($this->root . '/throwaway', 0o700, true);
        $runner = self::runner(['ddev delete' => null]);

        $this->site($runner)->teardown($this->root . '/throwaway', self::PROJECT);

        self::assertTrue($runner->issued('rm -rf ' . $this->root . '/throwaway'));
        self::assertContains('ddev delete reported a failure (project may never have been registered).', $this->log);
    }

    public function testTeardownOfATreeThatWasNeverCreatedIssuesNothing(): void
    {
        $runner = self::runner();

        $this->site($runner)->teardown($this->root . '/never-made', self::PROJECT);

        self::assertSame([], $runner->invocations);
    }
}

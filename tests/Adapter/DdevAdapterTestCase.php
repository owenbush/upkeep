<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\DdevContribAdapter;
use Upkeep\Adapter\EngineAddOn;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\FixtureAddOn;
use Upkeep\Adapter\ProjectName;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\Cockpit\Module;

/**
 * Scaffolding shared by the DdevContribAdapter tests.
 *
 * The adapter's job is orchestration: decide which commands to issue in which
 * order, and decide what each result means for the on-disk state of an
 * environment. Both halves are exercised here without a container runtime — the
 * commands go to a ScriptedCommandRunner, and the filesystem effects a real
 * `cp -a`, `git clone` or `ddev add-on get` would have had are reproduced by
 * the script inside a temp directory. Nothing in these tests touches docker,
 * the network, or anything outside that temp directory.
 */
abstract class DdevAdapterTestCase extends TestCase
{
    protected const MODULE = 'widget';
    protected const CORE = '11';
    protected const PRIMARY_URL = 'https://upkeep-widget-d11.ddev.site';
    protected const SEED_CORE_VERSION = '11.1.0';

    /** Everything the tests create lives under here and is removed in tearDown. */
    protected string $root;

    protected string $projectsRoot;

    protected string $artifactsDir;

    /** @var list<string> stage log lines the adapter emitted */
    protected array $log = [];

    protected function setUp(): void
    {
        $this->root = (string) realpath(sys_get_temp_dir()) . '/upkeep-adapter-' . bin2hex(random_bytes(6));
        $this->projectsRoot = $this->root . '/projects';
        $this->artifactsDir = $this->root . '/base-artifacts';

        mkdir($this->projectsRoot, 0o700, true);
        mkdir($this->artifactsDir . '/' . self::CORE . '/tree', 0o700, true);
        file_put_contents($this->artifactsDir . '/' . self::CORE . '/meta.yml', implode("\n", [
            'core_version: ' . self::SEED_CORE_VERSION,
            'core_major: ' . self::CORE,
            'php_version: 8.3.10',
            "db_engine: 'mariadb:10.11'",
            "built_at: '2026-01-01T00:00:00+00:00'",
            '',
        ]));

        $this->log = [];
    }

    protected function tearDown(): void
    {
        self::removeTree($this->root);
    }

    protected static function module(): Module
    {
        return new Module(self::MODULE, self::MODULE, [self::CORE]);
    }

    protected static function projectName(): string
    {
        return ProjectName::for(self::MODULE, self::CORE);
    }

    protected function projectPath(): string
    {
        return $this->projectsRoot . '/' . self::projectName();
    }

    protected function adapter(ScriptedCommandRunner $runner, ?string $projectsRoot = null): DdevContribAdapter
    {
        return new DdevContribAdapter(
            new ArtifactLayout($this->artifactsDir),
            $projectsRoot ?? $this->projectsRoot,
            $runner,
            function (string $line): void {
                $this->log[] = $line;
            },
        );
    }

    /**
     * An Environment as ensureEnv would have returned it, for the operations
     * that take one rather than building one.
     */
    protected function environment(): Environment
    {
        return new Environment(
            moduleName: self::MODULE,
            coreMajor: self::CORE,
            projectName: self::projectName(),
            projectPath: $this->projectPath(),
            primaryUrl: self::PRIMARY_URL,
            reused: false,
        );
    }

    /**
     * A runner scripted with the engine's default happy-path behaviour,
     * including the on-disk effects each command has.
     *
     * @param array<string, string|AdapterException|null> $overrides an
     *        AdapterException is thrown as written, so a test can reproduce an
     *        engine's own wording where failure handling reads it command-line substring => replacement outcome
     *                                              (null makes that command fail)
     */
    protected function engine(array $overrides = []): ScriptedCommandRunner
    {
        return new ScriptedCommandRunner(function (array $command, ?string $cwd) use ($overrides) {
            $line = implode(' ', $command);
            foreach ($overrides as $needle => $outcome) {
                if (str_contains($line, $needle)) {
                    return $outcome;
                }
            }

            return $this->defaultOutcome($command, $cwd);
        });
    }

    /**
     * Writes the environment's completion marker as a previous provision would
     * have left it, so the reuse decision has something to read.
     *
     * @param array<string, string> $overrides meta keys to write differently
     */
    protected function writeEnvironmentMeta(array $overrides = []): void
    {
        $data = array_merge([
            'module' => self::MODULE,
            'core_major' => self::CORE,
            'seed_core_version' => self::SEED_CORE_VERSION,
            'addon_version' => EngineAddOn::VERSION,
            'created_at' => '2026-01-01T00:00:00+00:00',
        ], $overrides);

        $lines = [];
        foreach ($data as $key => $value) {
            // Quoted throughout: an unquoted ISO-8601 scalar is parsed as a
            // timestamp integer, which is not what a written meta file holds.
            $lines[] = sprintf("%s: '%s'", $key, $value);
        }

        if (!is_dir($this->projectPath())) {
            mkdir($this->projectPath(), 0o700, true);
        }
        file_put_contents($this->projectPath() . '/.upkeep-env.yml', implode("\n", $lines) . "\n");
    }

    protected function loggedContaining(string $needle): bool
    {
        foreach ($this->log as $line) {
            if (str_contains($line, $needle)) {
                return true;
            }
        }

        return false;
    }

    /**
     * The filesystem effects and stdout of the engine's commands on a healthy
     * run. Anything not named here succeeds with empty output, which is what
     * the commands the adapter only checks for success do.
     *
     * @param list<string> $command
     */
    private function defaultOutcome(array $command, ?string $cwd): string
    {
        $line = implode(' ', $command);

        // `cp -a <tree> <project>` seeds the project tree from the base
        // artifact; the seeded tree carries the project composer.json.
        if ($command[0] === 'cp') {
            mkdir($command[3], 0o700, true);
            file_put_contents($command[3] . '/composer.json', '{"name": "drupal/recommended-project"}');

            return '';
        }

        // `git clone <url> <project>/module` lands the module working copy.
        if ($command[0] === 'git' && ($command[1] ?? '') === 'clone') {
            mkdir($command[3], 0o700, true);

            return '';
        }

        if ($command[0] === 'rm') {
            self::removeTree($command[2]);

            return '';
        }

        if (str_contains($line, 'add-on get ddev/ddev-drupal-contrib')) {
            mkdir((string) $cwd . '/.ddev', 0o700, true);
            file_put_contents(
                (string) $cwd . '/.ddev/config.contrib.yaml',
                "#ddev-generated\nweb_environment:\n  - DRUPAL_PROJECTS_PATH=modules/custom\n",
            );

            return '';
        }

        if (str_contains($line, 'add-on get ' . FixtureAddOn::source())) {
            $marker = (string) $cwd . '/.ddev/' . FixtureAddOn::MARKER;
            mkdir(\dirname($marker), 0o700, true);
            file_put_contents($marker, "#!/bin/bash\n");

            return '';
        }

        // Composer resolves the path repository into a symlink — the wiring
        // ownership constraint the adapter verifies right after.
        if (str_contains($line, 'composer require drupal/' . self::MODULE)) {
            $installed = (string) $cwd . '/web/modules/contrib/' . self::MODULE;
            mkdir(\dirname($installed), 0o700, true);
            symlink((string) $cwd . '/module', $installed);

            return '';
        }

        if (str_contains($line, 'symbolic-ref --short HEAD')) {
            return "1.0.x\n";
        }

        if (str_contains($line, 'drush status --field=drupal-version')) {
            return self::SEED_CORE_VERSION . "\n";
        }

        if (str_contains($line, 'ddev describe')) {
            return self::describeJson('running');
        }

        return '';
    }

    protected static function describeJson(string $status, string $primaryUrl = self::PRIMARY_URL): string
    {
        return (string) json_encode(['raw' => ['status' => $status, 'primary_url' => $primaryUrl]]);
    }

    /**
     * Reproduces the `rm -rf` the adapter issues, in PHP rather than a shell.
     *
     * This deliberately does not shell out. teardownProject() calls is_dir()
     * on the path immediately before issuing the removal, which populates PHP's
     * stat cache; an external `rm` then deletes the directory without PHP
     * knowing, so a following is_dir() — including the one inside
     * assertDirectoryDoesNotExist() — can read a stale "exists" and fail. PHP's
     * own unlink()/rmdir() invalidate the cache entry they act on, and the
     * explicit clearstatcache() covers the parent path as well.
     */
    private static function removeTree(string $path): void
    {
        if (!is_dir($path)) {
            return;
        }

        $entries = new \RecursiveIteratorIterator(
            new \RecursiveDirectoryIterator($path, \FilesystemIterator::SKIP_DOTS),
            \RecursiveIteratorIterator::CHILD_FIRST,
        );
        foreach ($entries as $entry) {
            // CURRENT_AS_FILEINFO is the directory iterator's default, so every
            // entry is an SplFileInfo — checked rather than assumed, matching
            // BaseArtifact\ArtifactScanner.
            if (!$entry instanceof \SplFileInfo) {
                continue;
            }

            $pathname = $entry->getPathname();
            if ($entry->isDir() && !$entry->isLink()) {
                rmdir($pathname);
                continue;
            }

            unlink($pathname);
        }

        rmdir($path);
        clearstatcache(true, $path);
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Adapter-owned engine mechanics for the base-artifact build: brings up a
 * throwaway ddev project seeded from a base tree, performs a module-free
 * clean install, exports the gzipped DB dump, and reports the install
 * environment's identity (PHP version, DB engine).
 *
 * Lives in the adapter namespace because it is engine knowledge — the
 * BaseArtifact layer orchestrates WHAT to build and never touches ddev
 * itself (grep guard: no "ddev" outside Upkeep\Adapter).
 */
final readonly class ThrowawaySite
{
    /**
     * @param \Closure(string): void $log
     */
    public function __construct(
        private ProcessRunner $runner,
        private \Closure $log,
    ) {
    }

    /**
     * Seeds a throwaway project from the base tree, clean-installs Drupal
     * (minimal profile), and exports the gzipped dump to $dumpPath.
     *
     * @return array{string, string} [php version, db engine identity] of the install environment
     */
    public function cleanInstallAndDump(string $coreMajor, string $treePath, string $throwawayPath, string $projectName, string $dumpPath): array
    {
        // Seed the throwaway by tree copy — the task-3-verified-identical path.
        ($this->log)(sprintf('Copying base tree to throwaway install project %s ...', $throwawayPath));
        $this->runner->run(['cp', '-a', $treePath, $throwawayPath]);

        ($this->log)('Configuring throwaway ddev project ...');
        $this->runner->run([
            'ddev', 'config',
            '--project-type=' . sprintf('drupal%s', $coreMajor),
            '--docroot=web',
            '--project-name=' . $projectName,
        ], $throwawayPath);

        ($this->log)('Starting ddev ...');
        $this->runner->run(['ddev', 'start', '-y'], $throwawayPath);

        // drush goes into the throwaway copy only; the canonical tree must
        // stay module-free. Run composer inside the container so resolution
        // happens against the same PHP the site will run on.
        ($this->log)('Requiring drush in the throwaway copy (container-side composer) ...');
        $this->runner->run(['ddev', 'composer', 'require', 'drush/drush', '--no-interaction'], $throwawayPath);

        ($this->log)('Installing Drupal (minimal profile, module-free) ...');
        $this->runner->run(['ddev', 'drush', 'site:install', 'minimal', '-y', '--account-pass=admin'], $throwawayPath);

        $phpVersion = trim($this->runner->run(['ddev', 'exec', 'php', '-r', 'echo PHP_VERSION;'], $throwawayPath));
        $dbEngine = $this->detectDbEngine($throwawayPath);
        ($this->log)(sprintf('Install environment: PHP %s, DB %s.', $phpVersion, $dbEngine));

        ($this->log)(sprintf('Exporting clean-install DB dump to %s ...', $dumpPath));
        $this->runner->run(['ddev', 'export-db', '--file=' . $dumpPath, '--gzip=true'], $throwawayPath);

        return [$phpVersion, $dbEngine];
    }

    public function teardown(string $throwawayPath, string $projectName): void
    {
        if (!is_dir($throwawayPath)) {
            return;
        }

        // Reclamation order per the task-3 verification: `ddev delete
        // --omit-snapshot` removes containers, all named volumes and
        // per-project built images. A bare rm -rf would leave containers
        // running and volumes orphaned — always ddev delete first, then
        // remove the tree.
        ($this->log)(sprintf('Tearing down throwaway install project %s ...', $projectName));
        if ($this->runner->tryRun(['ddev', 'delete', '--omit-snapshot', '--yes', $projectName]) === null) {
            // Best effort: config may not have been written yet if the build
            // failed before `ddev config` — nothing registered to delete.
            ($this->log)('ddev delete reported a failure (project may never have been registered).');
        }
        $this->runner->run(['rm', '-rf', $throwawayPath]);
    }

    private function detectDbEngine(string $throwawayPath): string
    {
        $json = $this->runner->run(['ddev', 'describe', '-j'], $throwawayPath);
        $decoded = json_decode($json, true);
        $dbinfo = $decoded['raw']['dbinfo'] ?? [];
        $type = $dbinfo['database_type'] ?? null;
        $version = $dbinfo['database_version'] ?? null;

        if (is_string($type) && $type !== '') {
            return is_string($version) && $version !== '' ? $type . ':' . $version : $type;
        }

        return 'unknown';
    }
}

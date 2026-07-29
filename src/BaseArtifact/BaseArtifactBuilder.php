<?php

declare(strict_types=1);

namespace Upkeep\BaseArtifact;

use Symfony\Component\Process\Process;

/**
 * Builds the two canonical per-core-version base artifacts:
 *
 *   1. a resolved, module-free drupal/recommended-project base tree, and
 *   2. a gzipped clean-install (minimal profile, module-free) SQL dump,
 *
 * stored under <cockpit>/base-artifacts/<core-major>/ with a meta.yml sidecar
 * and a `canonical` marker so pruning (task 16) never touches them.
 *
 * Seeding mechanism (task 3 verification, docs/contrib-maintainer-design.md
 * section 13, "Base-vendor-copy seeding"): copying a pristine resolved base
 * tree and layering `composer require` on top was verified byte-identical to a
 * from-scratch resolve (installed.json byte-identical, lock content-hash
 * equal, composer validate and install --dry-run clean). The base artifact
 * built here IS that pristine source-of-truth tree, so it is produced by a
 * full `composer create-project` riding the shared global Composer cache
 * (also verified: dists are reused across majors, 0 downloads on a warm
 * re-resolve); every downstream environment then seeds by tree copy with
 * confidence. The throwaway ddev site-install project used for the DB dump is
 * itself seeded from this tree by `cp -a` — the verified-identical copy path —
 * never by a second resolve, and drush is required only in the throwaway copy
 * so the canonical tree stays module-free.
 */
final readonly class BaseArtifactBuilder
{
    private const int PROCESS_TIMEOUT = 3600;

    /**
     * @param \Closure(string): void $log receives streamed process output/progress lines
     */
    public function __construct(
        private ArtifactLayout $layout,
        private string $scratchDir,
        private \Closure $log,
    ) {
    }

    public function build(string $coreMajor, bool $force): ArtifactMeta
    {
        $versionDir = $this->layout->versionDir($coreMajor);

        if (is_dir($versionDir)) {
            if (!$force) {
                throw new BuildException(sprintf(
                    'Base artifacts for core %s already exist at %s. Re-run with --force to rebuild deliberately.',
                    $coreMajor,
                    $versionDir,
                ));
            }
            // Deliberate stale rebuild: discard the previous artifact set. The
            // artifact directory never has a ddev project attached (only the
            // throwaway copy does), so plain removal is safe here — the
            // ddev-delete-first rule applies to the throwaway, in teardown().
            ($this->log)(sprintf('--force: removing existing artifact set at %s', $versionDir));
            $this->run(['rm', '-rf', $versionDir], null);
        }

        if (!mkdir($versionDir, 0755, true) && !is_dir($versionDir)) {
            throw new BuildException(sprintf('Could not create artifact directory "%s".', $versionDir));
        }

        try {
            return $this->doBuild($coreMajor, $versionDir);
        } catch (\Throwable $e) {
            // Never leave a partial artifact set behind: an existing version
            // directory must always mean the last build completed.
            ($this->log)(sprintf('Build failed — removing partial artifact set at %s', $versionDir));
            $this->run(['rm', '-rf', $versionDir], null);
            throw $e;
        }
    }

    private function doBuild(string $coreMajor, string $versionDir): ArtifactMeta
    {
        $treePath = $this->layout->treePath($coreMajor);

        // Full resolve riding the shared global Composer cache (see class
        // comment): this produces the pristine canonical tree all downstream
        // environments copy from. Never a bundled Composer — shell out.
        ($this->log)(sprintf('Resolving drupal/recommended-project:^%s into %s ...', $coreMajor, $treePath));
        $this->run([
            'composer', 'create-project',
            sprintf('drupal/recommended-project:^%s', $coreMajor),
            $treePath,
            '--no-interaction',
        ], null);

        ($this->log)('Validating resolved base tree (composer validate) ...');
        $this->run(['composer', 'validate', '--no-interaction'], $treePath);

        $coreVersion = ComposerLock::coreVersion((string) file_get_contents($treePath . '/composer.lock'));
        ($this->log)(sprintf('Resolved drupal/core %s.', $coreVersion));

        $projectName = sprintf('upkeep-base-d%s-%s', $coreMajor, substr(bin2hex(random_bytes(4)), 0, 6));
        $throwaway = rtrim($this->scratchDir, '/') . '/' . $projectName;

        try {
            [$phpVersion, $dbEngine] = $this->cleanInstallAndDump($coreMajor, $treePath, $throwaway, $projectName);
        } finally {
            $this->teardown($throwaway, $projectName);
        }

        $meta = new ArtifactMeta($coreVersion, $coreMajor, $phpVersion, $dbEngine, new \DateTimeImmutable());
        file_put_contents($this->layout->metaPath($coreMajor), $meta->toYaml());
        file_put_contents(
            $this->layout->canonicalMarkerPath($coreMajor),
            "This artifact set is canonical: never auto-pruned. Rebuild only via `upkeep base-artifacts:build --force`.\n",
        );

        return $meta;
    }

    /**
     * @return array{string, string} [php version, db engine identity] of the install environment
     */
    private function cleanInstallAndDump(string $coreMajor, string $treePath, string $throwaway, string $projectName): array
    {
        $scratchDir = \dirname($throwaway);
        if (!is_dir($scratchDir) && !mkdir($scratchDir, 0755, true) && !is_dir($scratchDir)) {
            throw new BuildException(sprintf('Could not create scratch directory "%s".', $scratchDir));
        }

        // Seed the throwaway by tree copy — the task-3-verified-identical path.
        ($this->log)(sprintf('Copying base tree to throwaway ddev project %s ...', $throwaway));
        $this->run(['cp', '-a', $treePath, $throwaway], null);

        ($this->log)('Configuring throwaway ddev project ...');
        $this->run([
            'ddev', 'config',
            '--project-type=' . sprintf('drupal%s', $coreMajor),
            '--docroot=web',
            '--project-name=' . $projectName,
        ], $throwaway);

        ($this->log)('Starting ddev ...');
        $this->run(['ddev', 'start', '-y'], $throwaway);

        // drush goes into the throwaway copy only; the canonical tree must
        // stay module-free. Run composer inside the container so resolution
        // happens against the same PHP the site will run on.
        ($this->log)('Requiring drush in the throwaway copy (container-side composer) ...');
        $this->run(['ddev', 'composer', 'require', 'drush/drush', '--no-interaction'], $throwaway);

        ($this->log)('Installing Drupal (minimal profile, module-free) ...');
        $this->run(['ddev', 'drush', 'site:install', 'minimal', '-y', '--account-pass=admin'], $throwaway);

        $phpVersion = trim($this->run(['ddev', 'exec', 'php', '-r', 'echo PHP_VERSION;'], $throwaway));
        $dbEngine = $this->detectDbEngine($throwaway);
        ($this->log)(sprintf('Install environment: PHP %s, DB %s.', $phpVersion, $dbEngine));

        $dumpPath = $this->layout->dumpPath($coreMajor);
        ($this->log)(sprintf('Exporting clean-install DB dump to %s ...', $dumpPath));
        $this->run(['ddev', 'export-db', '--file=' . $dumpPath, '--gzip=true'], $throwaway);

        if (!is_file($dumpPath) || (int) filesize($dumpPath) === 0) {
            throw new BuildException(sprintf('DB export did not produce a non-empty dump at "%s".', $dumpPath));
        }

        return [$phpVersion, $dbEngine];
    }

    private function detectDbEngine(string $throwaway): string
    {
        $json = $this->run(['ddev', 'describe', '-j'], $throwaway);
        $decoded = json_decode($json, true);
        $dbinfo = $decoded['raw']['dbinfo'] ?? [];
        $type = $dbinfo['database_type'] ?? null;
        $version = $dbinfo['database_version'] ?? null;

        if (is_string($type) && $type !== '') {
            return is_string($version) && $version !== '' ? $type . ':' . $version : $type;
        }

        return 'unknown';
    }

    private function teardown(string $throwaway, string $projectName): void
    {
        if (!is_dir($throwaway)) {
            return;
        }

        // Reclamation order per the task-3 verification: `ddev delete
        // --omit-snapshot` removes containers, all named volumes and per-project
        // built images. A bare rm -rf would leave containers running and
        // volumes orphaned — always ddev delete first, then remove the tree.
        ($this->log)(sprintf('Tearing down throwaway ddev project %s ...', $projectName));
        try {
            $this->run(['ddev', 'delete', '--omit-snapshot', '--yes', $projectName], null);
        } catch (BuildException $e) {
            // Best effort: config may not have been written yet if the build
            // failed before `ddev config` — nothing registered to delete.
            ($this->log)('ddev delete reported: ' . $e->getMessage());
        }
        $this->run(['rm', '-rf', $throwaway], null);
    }

    /**
     * @param list<string> $command
     */
    private function run(array $command, ?string $cwd): string
    {
        $process = new Process($command, $cwd, timeout: self::PROCESS_TIMEOUT);
        $process->run(function (string $type, string $buffer): void {
            foreach (explode("\n", rtrim($buffer, "\n")) as $line) {
                ($this->log)('  ' . $line);
            }
        });

        if (!$process->isSuccessful()) {
            throw new BuildException(sprintf(
                "Command failed (%s): %s\n%s",
                $process->getExitCode() ?? -1,
                $process->getCommandLine(),
                trim($process->getErrorOutput() . "\n" . $process->getOutput()),
            ));
        }

        return $process->getOutput();
    }
}

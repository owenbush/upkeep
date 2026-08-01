<?php

declare(strict_types=1);

namespace Upkeep\BaseArtifact;

use Symfony\Component\Process\Process;
use Upkeep\Adapter\ThrowawaySite;

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
 * confidence. The throwaway install project used for the DB dump (engine
 * mechanics owned by the adapter layer, see Upkeep\Adapter\ThrowawaySite) is
 * itself seeded from this tree by `cp -a` — the verified-identical copy path —
 * never by a second resolve, and drush is required only in the throwaway copy
 * so the canonical tree stays module-free.
 */
final readonly class BaseArtifactBuilder
{
    private const PROCESS_TIMEOUT = 3600;

    /**
     * @param \Closure(string): void $log receives streamed process output/progress lines
     */
    public function __construct(
        private ArtifactLayout $layout,
        private ThrowawaySite $installSite,
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
            // artifact directory never has an engine project attached (only
            // the throwaway copy does), so plain removal is safe here — the
            // delete-project-first reclamation rule applies to the throwaway,
            // inside the adapter's ThrowawaySite::teardown().
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

        $dumpPath = $this->layout->dumpPath($coreMajor);

        if (!is_dir($this->scratchDir) && !mkdir($this->scratchDir, 0755, true) && !is_dir($this->scratchDir)) {
            throw new BuildException(sprintf('Could not create scratch directory "%s".', $this->scratchDir));
        }

        try {
            [$phpVersion, $dbEngine] = $this->installSite->cleanInstallAndDump($coreMajor, $treePath, $throwaway, $projectName, $dumpPath);
        } finally {
            $this->installSite->teardown($throwaway, $projectName);
        }

        if (!is_file($dumpPath) || (int) filesize($dumpPath) === 0) {
            throw new BuildException(sprintf('DB export did not produce a non-empty dump at "%s".', $dumpPath));
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

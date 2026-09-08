<?php

declare(strict_types=1);

namespace Upkeep\BaseArtifact;

use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\CommandRunner;
use Upkeep\Adapter\ProcessRunner;
use Upkeep\Adapter\ThrowawaySite;
use Upkeep\Filesystem\FileWriter;

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
     * Every command this builder issues goes through the adapter's shell-out
     * seam, the same one ThrowawaySite uses — so the orchestration (which
     * commands, in what order, and what to conclude from each outcome) can be
     * exercised without a container runtime or a network, and so child output
     * reaches the log and the exception messages already redacted.
     */
    private CommandRunner $runner;

    /**
     * @param \Closure(string): void $log    receives streamed process output/progress lines
     * @param ?CommandRunner         $runner defaults to a real process runner sharing $log
     */
    public function __construct(
        private ArtifactLayout $layout,
        private ThrowawaySite $installSite,
        private string $scratchDir,
        private \Closure $log,
        ?CommandRunner $runner = null,
    ) {
        $this->runner = $runner ?? new ProcessRunner($log);
    }

    /**
     * Where a build in progress lives: a sibling of the live version
     * directory, inside the base-artifacts directory so the finished set moves
     * into place with a rename on the same filesystem rather than a copy.
     *
     * The leading dot keeps it out of `ArtifactLayout::versionsOnDisk()`,
     * which matches whole numbers — so a build in progress is invisible to
     * `base-artifacts:status`, to prune, and to the core inference that gives
     * an unregistered module its versions. A half-built tree must never read
     * as a core somebody can be offered.
     */
    private const STAGING_PREFIX = '.building-d';

    /** Where the outgoing set waits while the incoming one is moved in. */
    private const RETIRED_DIR = 'retired';

    public function build(string $coreMajor, bool $force, ?string $stability = null): ArtifactMeta
    {
        CoreConstraint::assertStability($stability);

        $versionDir = $this->layout->versionDir($coreMajor);
        $existing = is_dir($versionDir);

        if ($existing && !$force) {
            throw new BuildException(sprintf(
                'Base artifacts for core %s already exist at %s. Re-run with --force to rebuild deliberately.',
                $coreMajor,
                $versionDir,
            ));
        }

        // A rebuild used to remove the existing set first and resolve into the
        // empty directory, so a resolve that failed — a network blip, a
        // constraint that no longer resolves — left the core with no artifact
        // set at all and every environment for it unusable. The expensive,
        // failure-prone part now happens beside the live set and only a
        // rename touches it.
        $this->removeStaleStaging($coreMajor);
        $stagingRoot = $this->layout->baseArtifactsDir . '/' . self::STAGING_PREFIX . $coreMajor . '-'
            . bin2hex(random_bytes(4));
        $staging = new ArtifactLayout($stagingRoot);
        $stagingVersionDir = $staging->versionDir($coreMajor);

        // Silenced: a directory that cannot be created is reported as a build
        // failure naming it, not as a PHP warning printed mid-build.
        if (!@mkdir($stagingVersionDir, 0755, true) && !is_dir($stagingVersionDir)) {
            throw new BuildException(sprintf('Could not create artifact staging directory "%s".', $stagingVersionDir));
        }

        try {
            $meta = $this->doBuild($coreMajor, $staging, $stability);
        } catch (\Throwable $e) {
            // Never leave a partial artifact set behind: an existing version
            // directory must always mean the last build completed.
            ($this->log)(sprintf('Build failed — removing the staged artifact set at %s', $stagingRoot));
            $this->run(['rm', '-rf', $stagingRoot], null);
            if ($existing) {
                ($this->log)(sprintf(
                    'The existing base artifacts for core %s are untouched at %s.',
                    $coreMajor,
                    $versionDir,
                ));
            }
            throw $e;
        }

        $this->swapIntoPlace($coreMajor, $existing, $stagingRoot, $stagingVersionDir, $versionDir);

        return $meta;
    }

    /**
     * Moves a finished staged set into place: the outgoing one steps aside,
     * the incoming one takes the name, the staging directory goes.
     *
     * Both moves are renames within the base-artifacts directory, so each is
     * atomic and the whole swap is bounded by two of them rather than by the
     * minutes a resolve and a site install take. The residual window is real
     * but small: a process killed between the two renames leaves the core with
     * no version directory and both sets inside the staging directory, which
     * the failure message names for exactly that reason.
     */
    private function swapIntoPlace(
        string $coreMajor,
        bool $existing,
        string $stagingRoot,
        string $stagingVersionDir,
        string $versionDir,
    ): void {
        $retired = $stagingRoot . '/' . self::RETIRED_DIR;

        if ($existing) {
            ($this->log)(sprintf('Retiring the previous base artifacts for core %s ...', $coreMajor));
            if (!@rename($versionDir, $retired)) {
                throw new BuildException(sprintf(
                    "The new base artifacts for core %s built successfully, but the existing set at %s could not "
                    . "be moved aside.\nThe existing set is untouched; the new one is at %s.",
                    $coreMajor,
                    $versionDir,
                    $stagingVersionDir,
                ));
            }
        }

        if (!@rename($stagingVersionDir, $versionDir)) {
            // No branch on whether there was a previous set. Reporting the
            // staging directory as a whole is true either way, and the
            // alternative — a message that names the retired path only
            // sometimes — is a branch that cannot be reached: both renames
            // need write permission on the same two directories, so the
            // second cannot fail over permissions once the first has
            // succeeded.
            throw new BuildException(sprintf(
                "The new base artifacts for core %s built successfully but could not be moved to %s.\n"
                . "Nothing has been deleted: the finished set is at %s, and %s holds anything moved aside.\n"
                . 'Move the finished set into place by hand.',
                $coreMajor,
                $versionDir,
                $stagingVersionDir,
                $stagingRoot,
            ));
        }

        ($this->log)(sprintf('Base artifacts for core %s are in place at %s.', $coreMajor, $versionDir));
        $this->run(['rm', '-rf', $stagingRoot], null);
    }

    /**
     * Staging directories left by an earlier build that was killed outright.
     *
     * A crash between the staging mkdir and either exit path leaves a
     * multi-gigabyte tree that nothing else collects: the base-artifacts
     * directory is canonical, so prune protects it, and the leading-dot name
     * keeps it out of every listing. Removed at the start of the next build
     * for the same core, which is the next moment anyone is demonstrably not
     * relying on it.
     */
    private function removeStaleStaging(string $coreMajor): void
    {
        $stale = glob($this->layout->baseArtifactsDir . '/' . self::STAGING_PREFIX . $coreMajor . '-*', GLOB_ONLYDIR);
        foreach ($stale === false ? [] : $stale as $directory) {
            ($this->log)(sprintf('Removing a staging directory left by an interrupted build: %s', $directory));
            $this->run(['rm', '-rf', $directory], null);
        }
    }

    private function doBuild(string $coreMajor, ArtifactLayout $into, ?string $stability): ArtifactMeta
    {
        $treePath = $into->treePath($coreMajor);

        // Full resolve riding the shared global Composer cache (see class
        // comment): this produces the pristine canonical tree all downstream
        // environments copy from. Never a bundled Composer — shell out.
        $constraint = CoreConstraint::for($coreMajor, $stability);
        ($this->log)(sprintf('Resolving %s into %s ...', $constraint, $treePath));

        try {
            $this->run(['composer', 'create-project', $constraint, $treePath, '--no-interaction'], null);
        } catch (BuildException $e) {
            // Composer's own words first, then what can be done about them —
            // the same order a refused push uses. The commonest cause is a
            // core major that has no stable release yet, and nothing in
            // composer's output suggests there is a flag for that.
            throw new BuildException(
                $e->getMessage() . (CoreConstraint::unresolvableHint($coreMajor, $stability) ?? ''),
                0,
                $e,
            );
        }

        ($this->log)('Validating resolved base tree (composer validate) ...');
        $this->run(['composer', 'validate', '--no-interaction'], $treePath);

        $coreVersion = ComposerLock::coreVersion((string) file_get_contents($treePath . '/composer.lock'));
        ($this->log)(sprintf('Resolved drupal/core %s.', $coreVersion));

        $projectName = sprintf('upkeep-base-d%s-%s', $coreMajor, substr(bin2hex(random_bytes(4)), 0, 6));
        $throwaway = rtrim($this->scratchDir, '/') . '/' . $projectName;

        $dumpPath = $into->dumpPath($coreMajor);

        if (!is_dir($this->scratchDir) && !@mkdir($this->scratchDir, 0755, true) && !is_dir($this->scratchDir)) {
            throw new BuildException(sprintf('Could not create scratch directory "%s".', $this->scratchDir));
        }

        try {
            [$phpVersion, $dbEngine] = $this->installSite->cleanInstallAndDump(
                $coreMajor,
                $treePath,
                $throwaway,
                $projectName,
                $dumpPath,
            );
        } finally {
            $this->installSite->teardown($throwaway, $projectName);
        }

        if (!is_file($dumpPath) || (int) filesize($dumpPath) === 0) {
            throw new BuildException(sprintf('DB export did not produce a non-empty dump at "%s".', $dumpPath));
        }

        // Checked writes: a missing meta.yml or canonical marker makes the set
        // read as incomplete and stops prune protecting it, while the command
        // reports a successful build. The enclosing catch in build() removes
        // the whole version directory if either fails.
        $meta = new ArtifactMeta($coreVersion, $coreMajor, $phpVersion, $dbEngine, new \DateTimeImmutable());
        FileWriter::write($into->metaPath($coreMajor), $meta->toYaml(), FileWriter::MODE_SHARED);
        FileWriter::write(
            $into->canonicalMarkerPath($coreMajor),
            "This artifact set is canonical: never auto-pruned. Rebuild only via "
                . "`upkeep base-artifacts:build --force`.\n",
            FileWriter::MODE_SHARED,
        );

        return $meta;
    }

    /**
     * A failed step is a failed build, not an adapter problem the caller has
     * to know about: the adapter's exception type is translated here so the
     * build surface raises exactly one kind of error.
     *
     * @param list<string> $command
     */
    private function run(array $command, ?string $cwd): string
    {
        try {
            return $this->runner->run($command, $cwd, self::PROCESS_TIMEOUT);
        } catch (AdapterException $e) {
            throw new BuildException($e->getMessage(), 0, $e);
        }
    }
}

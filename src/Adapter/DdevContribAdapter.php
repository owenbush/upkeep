<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\BaseArtifact\ArtifactMeta;
use Upkeep\BaseArtifact\MetaException;
use Upkeep\Cockpit\Module;
use Upkeep\Gitlab\MergeRequest;

/**
 * EngineAdapterInterface implementation for the ddev + ddev-drupal-contrib
 * engine.
 *
 * Environment layout under the projects root (one directory per
 * (module x core-major) pair, named by ProjectName):
 *
 *   <projects-root>/upkeep-<module>-d<major>/
 *       (seeded base tree: composer.json, web/, vendor/, ...)
 *       .ddev/            engine project config + pinned add-on
 *       module/           git working copy of the module (adapter/git-owned;
 *                         composer only ever symlinks to it, never writes in it)
 *       web/modules/contrib/<module>  -> symlink into module/ (composer path repo)
 *       .upkeep-env.yml   EnvironmentMeta: provisioning completion marker +
 *                         identity for the reuse/health decision
 */
final class DdevContribAdapter implements EngineAdapterInterface
{
    private const string GIT_BASE_URL = 'https://git.drupalcode.org/';
    private const string MODULE_DIR = 'module';

    /**
     * @param \Closure(string): void $log
     */
    public function __construct(
        private readonly ArtifactLayout $layout,
        private readonly string $projectsRoot,
        private readonly ProcessRunner $runner,
        private readonly \Closure $log,
    ) {
    }

    public function ensureEnv(Module $module, string $coreMajor): Environment
    {
        $projectName = ProjectName::for($module->name, $coreMajor);
        $projectPath = rtrim($this->projectsRoot, '/') . '/' . $projectName;
        $artifactMeta = $this->requireArtifactMeta($coreMajor);

        if (is_dir($projectPath)) {
            $staleReasons = $this->staleReasons($module, $coreMajor, $artifactMeta, $projectName, $projectPath);
            if ($staleReasons === []) {
                ($this->log)(sprintf('Reusing existing environment %s at %s.', $projectName, $projectPath));

                return $this->reuse($module, $coreMajor, $projectName, $projectPath);
            }

            ($this->log)(sprintf('Existing environment %s is stale — re-provisioning:', $projectName));
            foreach ($staleReasons as $reason) {
                ($this->log)('  - ' . $reason);
            }
            $this->teardownProject($projectName, $projectPath);
        }

        return $this->provision($module, $coreMajor, $artifactMeta, $projectName, $projectPath);
    }

    public function applyMr(Environment $environment, MergeRequest $mergeRequest): void
    {
        throw new \BadMethodCallException('apply_mr is implemented by task 11 (per-MR operations).');
    }

    public function loadFixture(Environment $environment, string $fixtureName): void
    {
        throw new \BadMethodCallException('load_fixture is implemented by task 11 (per-MR operations).');
    }

    public function runChecks(Environment $environment, array $checks = []): CheckRunResult
    {
        throw new \BadMethodCallException('run_checks is implemented by task 11 (per-MR operations).');
    }

    public function serve(Environment $environment): ServeResult
    {
        throw new \BadMethodCallException('serve is implemented by task 11 (per-MR operations).');
    }

    public function teardown(Module $module, string $coreMajor): void
    {
        $projectName = ProjectName::for($module->name, $coreMajor);
        $projectPath = rtrim($this->projectsRoot, '/') . '/' . $projectName;

        if (!is_dir($projectPath) && $this->describe($projectName) === null) {
            ($this->log)(sprintf('Environment %s does not exist — nothing to tear down.', $projectName));

            return;
        }

        $this->teardownProject($projectName, $projectPath);
    }

    /**
     * The reuse/health decision: meta dotfile present, parseable, matching
     * the request and the pinned engine identity — and the engine still knows
     * the project. Any reason forces re-provisioning.
     *
     * @return list<string>
     */
    private function staleReasons(Module $module, string $coreMajor, ArtifactMeta $artifactMeta, string $projectName, string $projectPath): array
    {
        $metaPath = $projectPath . '/' . EnvironmentMeta::FILENAME;
        if (!is_file($metaPath)) {
            // The meta dotfile is written as the LAST provisioning step: its
            // absence means an interrupted provision, never a reusable env.
            return [sprintf('No %s completion marker — a previous provision did not finish.', EnvironmentMeta::FILENAME)];
        }

        try {
            $meta = EnvironmentMeta::fromYaml((string) file_get_contents($metaPath));
        } catch (AdapterException $e) {
            return ['Environment meta is unreadable: ' . $e->getMessage()];
        }

        $reasons = $meta->staleReasons($module->name, $coreMajor, $artifactMeta->coreVersion, EngineAddOn::VERSION);

        if ($this->describe($projectName) === null) {
            $reasons[] = 'The engine no longer reports the project (describe failed).';
        }

        return $reasons;
    }

    private function reuse(Module $module, string $coreMajor, string $projectName, string $projectPath): Environment
    {
        $described = $this->describe($projectName) ?? throw new AdapterException(sprintf('Engine lost project %s between health check and reuse.', $projectName));

        if (($described['status'] ?? '') !== 'running') {
            ($this->log)(sprintf('Environment %s is stopped — starting it.', $projectName));
            $this->runner->run(['ddev', 'start', '-y'], $projectPath);
            $described = $this->describe($projectName) ?? throw new AdapterException(sprintf('Project %s did not come back after start.', $projectName));
        }

        return new Environment(
            moduleName: $module->name,
            coreMajor: $coreMajor,
            projectName: $projectName,
            projectPath: $projectPath,
            primaryUrl: (string) ($described['primary_url'] ?? ''),
            reused: true,
        );
    }

    private function provision(Module $module, string $coreMajor, ArtifactMeta $artifactMeta, string $projectName, string $projectPath): Environment
    {
        ($this->log)(sprintf('Provisioning environment %s (module %s, Drupal %s) ...', $projectName, $module->name, $coreMajor));

        if (!is_dir($this->projectsRoot) && !mkdir($this->projectsRoot, 0755, true) && !is_dir($this->projectsRoot)) {
            throw new AdapterException(sprintf('Could not create projects root "%s".', $this->projectsRoot));
        }

        try {
            ($this->log)('Seeding codebase from the canonical base tree ...');
            $this->runner->run(['cp', '-a', $this->layout->treePath($coreMajor), $projectPath]);

            ($this->log)('Cloning the module working copy ...');
            $this->runner->run(['git', 'clone', self::GIT_BASE_URL . $module->project . '.git', $projectPath . '/' . self::MODULE_DIR]);

            ($this->log)('Configuring the engine project ...');
            $this->runner->run([
                'ddev', 'config',
                '--project-type=' . sprintf('drupal%s', $coreMajor),
                '--docroot=web',
                '--project-name=' . $projectName,
            ], $projectPath);

            ($this->log)(sprintf('Installing engine add-on %s at pinned version %s ...', EngineAddOn::NAME, EngineAddOn::VERSION));
            $this->runner->run(['ddev', 'add-on', 'get', EngineAddOn::NAME, '--version', EngineAddOn::VERSION], $projectPath);
            $this->adaptAddOnConfig($projectPath);

            ($this->log)('Starting the environment ...');
            $this->runner->run(['ddev', 'start', '-y'], $projectPath);

            ($this->log)('Requiring drush (container-side composer) ...');
            $this->runner->run(['ddev', 'composer', 'require', 'drush/drush', '--no-interaction'], $projectPath);

            $this->wireModule($module, $projectPath);

            ($this->log)('Restoring the clean-install database snapshot ...');
            $this->runner->run(['ddev', 'import-db', '--file=' . $this->layout->dumpPath($coreMajor)], $projectPath);

            // Provisioning health gate: the environment must bootstrap and
            // report the requested core major — read from the project itself.
            $drupalVersion = trim($this->runner->run(['ddev', 'drush', 'status', '--field=drupal-version'], $projectPath));
            if (!str_starts_with($drupalVersion, $coreMajor . '.')) {
                throw new AdapterException(sprintf('Environment %s reports Drupal "%s", expected major %s.', $projectName, $drupalVersion, $coreMajor));
            }
            ($this->log)(sprintf('Environment reports Drupal %s.', $drupalVersion));

            $described = $this->describe($projectName) ?? throw new AdapterException(sprintf('Engine does not report project %s after provisioning.', $projectName));

            // Written LAST: the completion marker. A crash before this line
            // leaves no marker, and the next ensure_env re-provisions.
            file_put_contents(
                $projectPath . '/' . EnvironmentMeta::FILENAME,
                new EnvironmentMeta($module->name, $coreMajor, $artifactMeta->coreVersion, EngineAddOn::VERSION, new \DateTimeImmutable())->toYaml(),
            );

            return new Environment(
                moduleName: $module->name,
                coreMajor: $coreMajor,
                projectName: $projectName,
                projectPath: $projectPath,
                primaryUrl: (string) ($described['primary_url'] ?? ''),
                reused: false,
            );
        } catch (\Throwable $e) {
            ($this->log)(sprintf('Provisioning failed — tearing down partial environment %s.', $projectName));
            try {
                $this->teardownProject($projectName, $projectPath);
            } catch (\Throwable $cleanup) {
                ($this->log)('Cleanup after failed provisioning also failed: ' . $cleanup->getMessage());
            }
            throw $e;
        }
    }

    /**
     * Wires the module working copy into the project via a Composer path
     * repository (see ModuleWiring for why not the engine's symlink-project):
     * composer resolves the module's dependencies but only ever creates a
     * symlink at web/modules/contrib/<module> — it never owns the checkout.
     */
    private function wireModule(Module $module, string $projectPath): void
    {
        ($this->log)('Wiring the module working copy via a Composer path repository ...');

        $composerJsonPath = $projectPath . '/composer.json';
        file_put_contents($composerJsonPath, ModuleWiring::withPathRepository(
            (string) file_get_contents($composerJsonPath),
            './' . self::MODULE_DIR,
        ));

        // Pin the exact branch the working copy has checked out: a bare
        // "*@dev" could resolve to a different dev branch published on
        // packages.drupal.org instead of the path repository.
        $branch = trim($this->runner->run(['git', '-C', $projectPath . '/' . self::MODULE_DIR, 'symbolic-ref', '--short', 'HEAD']));
        $this->runner->run([
            'ddev', 'composer', 'require',
            sprintf('drupal/%s:%s', $module->name, ModuleWiring::devConstraintForBranch($branch)),
            '--no-interaction',
        ], $projectPath);

        $installedPath = sprintf('%s/web/modules/contrib/%s', $projectPath, $module->name);
        if (!is_link($installedPath)) {
            throw new AdapterException(sprintf(
                'Module wiring violated the ownership constraint: "%s" is not a symlink into the working copy (composer mirrored the package instead).',
                $installedPath,
            ));
        }
    }

    private function adaptAddOnConfig(string $projectPath): void
    {
        $configPath = $projectPath . '/.ddev/' . EngineAddOn::CONFIG_FILENAME;
        if (!is_file($configPath)) {
            throw new AdapterException(sprintf('Engine add-on did not install its config at "%s" — cannot adapt it.', $configPath));
        }
        file_put_contents($configPath, EngineAddOn::adaptContribConfig((string) file_get_contents($configPath)));
    }

    /**
     * Disposal per the verified reclamation semantics: `ddev delete
     * --omit-snapshot` first (removes containers, named volumes, per-project
     * images; a bare rm -rf would leave containers running), then the tree.
     */
    private function teardownProject(string $projectName, string $projectPath): void
    {
        ($this->log)(sprintf('Deleting engine project %s (containers + volumes) ...', $projectName));
        if ($this->runner->tryRun(['ddev', 'delete', '--omit-snapshot', '--yes', $projectName]) === null) {
            ($this->log)('Engine delete reported a failure (project may not be registered) — continuing with tree removal.');
        }
        if (is_dir($projectPath)) {
            $this->runner->run(['rm', '-rf', $projectPath]);
        }
        ($this->log)(sprintf('Environment %s torn down.', $projectName));
    }

    /**
     * @return array<string, mixed>|null the engine's raw project description, null when unknown
     */
    private function describe(string $projectName): ?array
    {
        $json = $this->runner->tryRun(['ddev', 'describe', $projectName, '-j']);
        if ($json === null) {
            return null;
        }

        $decoded = json_decode($json, true);

        return is_array($decoded['raw'] ?? null) ? $decoded['raw'] : null;
    }

    private function requireArtifactMeta(string $coreMajor): ArtifactMeta
    {
        $metaPath = $this->layout->metaPath($coreMajor);
        if (!is_file($metaPath)) {
            throw new AdapterException(sprintf(
                'No base artifacts for Drupal %s (missing %s). Run `upkeep base-artifacts:build --core=%s` first.',
                $coreMajor,
                $metaPath,
                $coreMajor,
            ));
        }

        try {
            return ArtifactMeta::fromYaml((string) file_get_contents($metaPath));
        } catch (MetaException $e) {
            throw new AdapterException(sprintf('Base artifact meta for Drupal %s is unreadable: %s', $coreMajor, $e->getMessage()), previous: $e);
        }
    }
}

<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Upkeep\Cockpit\Module;
use Upkeep\Gitlab\MergeRequest;

/**
 * The engine adapter: upkeep's single structural boundary around the
 * environment engine (currently ddev + the ddev-drupal-contrib add-on).
 *
 * All knowledge of engine command names, project layout, and engine behavior
 * lives behind this interface — nothing outside Upkeep\Adapter may mention
 * the engine (enforced by grep guard: `grep -r "ddev" src/
 * --exclude-dir=Adapter` returns nothing). If a later feature needs an engine
 * detail, it gets a new method here, never a shell-out from command code.
 *
 * Environment identity is always the (module x core-major) pair; the adapter
 * owns how that maps to concrete projects on disk.
 */
interface EngineAdapterInterface
{
    /**
     * Provisions the environment for (module, core major), or reuses the
     * existing one when it is present and healthy (engine reports the
     * project, environment meta matches module, core, seed identity, and
     * pinned add-on version). Provisioning seeds the codebase from the
     * canonical base tree, installs the pinned engine add-on, wires the
     * module working copy in such that no composer operation can clobber it,
     * and restores the clean-install database snapshot.
     *
     * @throws AdapterException when base artifacts for the core version are missing or provisioning fails
     */
    public function ensureEnv(Module $module, string $coreMajor): Environment;

    /**
     * Checks out the merge request's code in the environment's module working
     * copy (fetched from the module's origin repository).
     *
     * @throws AdapterException
     */
    public function applyMr(Environment $environment, MergeRequest $mergeRequest): void;

    /**
     * Restores the named fixture's database state into the environment,
     * replacing whatever state it currently holds.
     *
     * @throws AdapterException
     */
    public function loadFixture(Environment $environment, string $fixtureName): void;

    /**
     * Runs the requested check suites against the module under maintenance
     * and reports per-check outcomes. An empty selection means the adapter's
     * default suite. Check failures are results, not exceptions.
     *
     * @param list<CheckType> $checks
     *
     * @throws AdapterException when a check cannot be executed at all
     */
    public function runChecks(Environment $environment, array $checks = []): CheckRunResult;

    /**
     * Makes the environment browsable and returns where: its primary URL and,
     * when available, a one-time authenticated login URL.
     *
     * @throws AdapterException
     */
    public function serve(Environment $environment): ServeResult;

    /**
     * Returns the absolute path to the (module, core major) environment
     * directory when it exists and was fully provisioned (completion marker
     * present), or null when no such environment has been provisioned.
     *
     * Never provisions, starts, or modifies the environment.
     */
    public function resolveEnvPath(string $moduleName, string $coreMajor): ?string;

    /**
     * Inspects the module working copy in an existing environment for local
     * work (uncommitted changes, untracked files, unpushed commits, or a
     * non-standard branch). Returns null when no provisioned environment
     * exists for this (module, core) pair.
     *
     * Never provisions, starts, or modifies the environment.
     */
    public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus;

    /**
     * Checks out the given branch in the module working copy. Syncs the
     * Composer pin so the project's dependency resolution stays satisfiable.
     *
     * @throws AdapterException when the checkout fails or the working copy is dirty
     */
    public function checkoutBranch(Environment $environment, string $branch): void;

    /**
     * Disposes the (module, core major) environment completely: engine
     * project, containers, named volumes, and the on-disk tree. A no-op when
     * the environment does not exist.
     *
     * @throws AdapterException when the engine refuses to release resources
     */
    public function teardown(Module $module, string $coreMajor): void;
}

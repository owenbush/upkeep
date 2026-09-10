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
     * Applies an already-downloaded patch file onto a branch off the module
     * working copy's base, and commits it so the checks that follow run
     * against a clean tree.
     *
     * A patch that does not apply is reported as an AdapterException naming
     * the patch and the base it was tried against — for a maintainer that is
     * a review finding ("needs a re-roll"), not a tool malfunction.
     *
     * @throws AdapterException when the working copy is dirty, the base cannot
     *                          be resolved, or the patch does not apply
     */
    public function applyPatch(
        Environment $environment,
        PatchApplication $patch,
        BaseRefresh $refresh = BaseRefresh::Update,
    ): void;

    /**
     * Opens the maintainer's own work branch for an issue, creating it off the
     * base or resuming it if it already exists.
     *
     * Unlike applyMr() and applyPatch(), this **never resets anything**. Those
     * two reset their disposable branch on every call so a contribution is
     * tested alone; this one holds the only copy of something a human wrote,
     * so an existing branch is checked out as it stands.
     *
     * @param ?string $baseBranch what to branch from; null resolves it from
     *                             the working copy, which is where that
     *                             knowledge lives
     *
     * @return bool true when an existing branch was resumed, false when one
     *              was created
     *
     * @throws AdapterException when the working copy is dirty or the base
     *                          cannot be checked out
     */
    public function startWork(
        Environment $environment,
        IssueBranch $branch,
        ?string $baseBranch = null,
        BaseRefresh $refresh = BaseRefresh::Update,
    ): bool;

    /**
     * Applies a patch onto the maintainer's own work branch for the issue and
     * commits it under the given message — the local half of turning a patch
     * contribution into a merge request.
     *
     * Deliberately not applyPatch() with a different branch argument. That one
     * resets its branch from the base on every call, which is right for a
     * disposable branch tested alone and catastrophic for a work branch that
     * may hold commits kept nowhere else. This routes through startWork(),
     * which resumes rather than resets, so the two behaviours cannot drift.
     *
     * The commit message is the caller's, because it is the whole point: it
     * carries the patch author's name into the history (see
     * Patches\PatchAttribution).
     *
     * @param bool $allowPartial when the patch will not apply, take the hunks
     *                            that still fit and leave the rest as `.rej`
     *                            files rather than refusing — the start of a
     *                            re-roll instead of a dead end
     *
     * @return PatchPromotion committed, or partial with nothing committed
     *
     * @throws AdapterException when the working copy is dirty or the patch
     *                          does not apply
     */
    public function promotePatch(
        Environment $environment,
        PatchApplication $patch,
        IssueBranch $branch,
        string $commitMessage,
        BaseRefresh $refresh = BaseRefresh::Update,
        bool $allowPartial = false,
    ): PatchPromotion;

    /**
     * Pushes a work branch to $remote, adding or re-pointing it as needed.
     *
     * The destination is a parameter because contributing to Drupal does not
     * put branches on the canonical project: the branch goes to the issue
     * fork, and the merge request is opened across projects. The command that
     * knows about issues and forks decides; the adapter puts the branch where
     * it is told. Origin is never pushed to and never altered, so fetch stays
     * anonymous and read-only work needs no key.
     *
     * Push only — never force, never delete. The remote copy may be the only
     * one, and a branch this tool did not create is not a branch it may
     * overwrite.
     *
     * @return string the head SHA that was pushed
     *
     * @throws AdapterException when the working copy is dirty, is not on that
     *                          branch, or the push is rejected
     */
    public function pushWork(Environment $environment, IssueBranch $branch, GitRemote $remote): string;

    /**
     * The base branch the working copy's contribution was cut from, as
     * `startWork()` / `applyMr()` / `applyPatch()` recorded it.
     *
     * Publishing needs it and cannot derive it: a merge request's target is a
     * *branch name* on the project (`2.0.x`, `8.x-1.x`), and nothing outside
     * the working copy knows which one this work belongs on. The tracked core
     * major is not a substitute — it names a version of Drupal, not a branch,
     * and no contrib project has a branch called "11".
     *
     * Null when nothing was recorded, which callers must treat as "ask the
     * operator" rather than guessing.
     */
    public function recordedBaseBranch(Environment $environment): ?string;

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

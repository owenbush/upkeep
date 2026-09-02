<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\BaseArtifact\ArtifactMeta;
use Upkeep\BaseArtifact\MetaException;
use Upkeep\Cockpit\Module;
use Upkeep\Filesystem\FileWriter;
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
    private const GIT_BASE_URL = 'https://git.drupalcode.org/';
    private const MODULE_DIR = 'module';

    /** Generous per-check timebox; a timeout is a failure with reason. */
    private const CHECK_TIMEOUT = 1800;

    /** The default suite: engine static/test checks, install, smoke, deprecation. */
    private const DEFAULT_CHECKS = [
        CheckType::PhpUnit,
        CheckType::PhpStan,
        CheckType::PhpCs,
        CheckType::ModuleInstall,
        CheckType::FunctionalSmoke,
        CheckType::Deprecation,
    ];

    /** Checks that need the dev toolchain (phpunit/phpstan/phpcs binaries) in vendor/. */
    private const TOOLCHAIN_CHECKS = [CheckType::PhpUnit, CheckType::PhpStan, CheckType::PhpCs];

    /**
     * What check provisioning installs (container-side, --dev): the same
     * toolchain drupal.org GitLab CI uses. core-dev pins phpunit and friends
     * to the seeded core; coder ships phpcs + the Drupal standards;
     * mglaman/phpstan-drupal + extension-installer + deprecation-rules make
     * the gitlab_templates phpstan.neon work as it does in CI.
     */
    private const TOOLCHAIN_PACKAGES = [
        'drupal/core-dev:^%s',
        'drupal/coder',
        'mglaman/phpstan-drupal',
        'phpstan/extension-installer',
        'phpstan/phpstan-deprecation-rules',
    ];

    /**
     * @param \Closure(string): void $log
     */
    public function __construct(
        private readonly ArtifactLayout $layout,
        private readonly string $projectsRoot,
        private readonly CommandRunner $runner,
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

            $moduleDir = $projectPath . '/' . self::MODULE_DIR;
            if (is_dir($moduleDir)) {
                $wcStatus = WorkingCopyStatus::inspect($moduleDir, $this->runner);
                if ($wcStatus->hasLocalWork()) {
                    throw new AdapterException(sprintf(
                        "Environment %s is stale and needs re-provisioning, but the module working "
                        . "copy has local work:\n  %s\n"
                        . 'Push or stash your work, then re-run.',
                        $projectName,
                        implode("\n  ", $wcStatus->describe()),
                    ));
                }
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
        $moduleDir = $environment->projectPath . '/' . self::MODULE_DIR;

        $wcStatus = WorkingCopyStatus::inspect($moduleDir, $this->runner);
        if ($wcStatus->isDirty()) {
            throw new AdapterException(sprintf(
                "Cannot apply MR !%d: the module working copy has uncommitted changes:\n  %s\n"
                . 'Commit or stash your changes first, then re-run.',
                $mergeRequest->iid,
                implode("\n  ", $wcStatus->describe()),
            ));
        }

        $currentBranch = $this->runner->tryRun(['git', '-C', $moduleDir, 'symbolic-ref', '--short', 'HEAD']);
        $recordedBase = $this->runner->tryRun(['git', '-C', $moduleDir, 'config', '--get', 'upkeep.base-branch']);
        $baseBranch = MrCheckout::resolveBaseBranch(
            $currentBranch !== null ? trim($currentBranch) : null,
            $recordedBase !== null ? trim($recordedBase) : null,
        );

        MrCheckout::assertNativeBase($mergeRequest, $baseBranch);

        ($this->log)(sprintf(
            'Applying MR !%d (%s -> %s) into the module working copy ...',
            $mergeRequest->iid,
            $mergeRequest->sourceBranch,
            $mergeRequest->targetBranch,
        ));

        // Step back onto the base first: git refuses to fetch into the
        // currently checked-out branch, which mr-<iid> is on a re-apply.
        $this->runner->run(['git', '-C', $moduleDir, 'checkout', $baseBranch]);
        $this->runner->run(['git', '-C', $moduleDir, 'fetch', 'origin', MrCheckout::fetchRefspec($mergeRequest->iid)]);
        $this->runner->run(['git', '-C', $moduleDir, 'checkout', MrCheckout::branchName($mergeRequest->iid)]);
        // Record the base so the next applyMr can validate native-base even
        // though the working copy now sits on an mr-* branch.
        $this->runner->run(['git', '-C', $moduleDir, 'config', 'upkeep.base-branch', $baseBranch]);

        $head = trim($this->runner->run(['git', '-C', $moduleDir, 'rev-parse', '--abbrev-ref', 'HEAD']));
        if ($head !== MrCheckout::branchName($mergeRequest->iid)) {
            throw new AdapterException(sprintf(
                'MR checkout did not stick: working copy is on "%s", expected "%s".',
                $head,
                MrCheckout::branchName($mergeRequest->iid),
            ));
        }

        $sha = trim($this->runner->run(['git', '-C', $moduleDir, 'rev-parse', 'HEAD']));
        if ($mergeRequest->headSha !== null && $sha !== $mergeRequest->headSha) {
            ($this->log)(sprintf(
                'Note: checked-out head %s differs from the MR model\'s head %s — the MR may have moved '
                . 'since it was fetched.',
                $sha,
                $mergeRequest->headSha,
            ));
        }

        ($this->log)('Syncing the composer pin to the MR branch (and resolving any dependencies the MR adds) ...');
        $this->requireWorkingCopyBranch($environment->projectPath, $environment->moduleName, $head);

        ($this->log)(sprintf(
            'MR !%d applied: working copy on %s at %s (base %s).',
            $mergeRequest->iid,
            $head,
            $sha,
            $baseBranch,
        ));
    }

    public function applyPatch(Environment $environment, PatchApplication $patch): void
    {
        $moduleDir = $environment->projectPath . '/' . self::MODULE_DIR;

        $wcStatus = WorkingCopyStatus::inspect($moduleDir, $this->runner);
        if ($wcStatus->isDirty()) {
            throw new AdapterException(sprintf(
                "Cannot apply patch \"%s\": the module working copy has uncommitted changes:\n  %s\n"
                . 'Commit or stash your changes first, then re-run.',
                $patch->name,
                implode("\n  ", $wcStatus->describe()),
            ));
        }

        $currentBranch = $this->runner->tryRun(['git', '-C', $moduleDir, 'symbolic-ref', '--short', 'HEAD']);
        $recordedBase = $this->runner->tryRun(['git', '-C', $moduleDir, 'config', '--get', 'upkeep.base-branch']);
        $baseBranch = MrCheckout::resolveBaseBranch(
            $currentBranch !== null ? trim($currentBranch) : null,
            $recordedBase !== null ? trim($recordedBase) : null,
        );

        $branch = $patch->branchName();
        ($this->log)(sprintf('Applying patch "%s" onto %s as %s ...', $patch->name, $baseBranch, $branch));

        // Reset the branch from the base on every apply: a re-roll must be
        // tested on its own, not stacked on whatever was applied last time.
        $this->runner->run(['git', '-C', $moduleDir, 'checkout', $baseBranch]);
        $this->runner->run(['git', '-C', $moduleDir, 'checkout', '-B', $branch, $baseBranch]);
        $this->runner->run(['git', '-C', $moduleDir, 'config', 'upkeep.base-branch', $baseBranch]);

        $this->applyPatchFile($moduleDir, $patch, $baseBranch);

        $this->runner->run([
            'git', '-C', $moduleDir,
            '-c', 'user.name=upkeep',
            '-c', 'user.email=upkeep@localhost',
            'commit', '--no-verify', '-m', PatchCheckout::commitMessage($patch),
        ]);

        $sha = trim($this->runner->run(['git', '-C', $moduleDir, 'rev-parse', 'HEAD']));

        ($this->log)('Syncing the composer pin to the patch branch ...');
        $this->requireWorkingCopyBranch($environment->projectPath, $environment->moduleName, $branch);

        ($this->log)(sprintf(
            'Patch "%s" applied: working copy on %s at %s (base %s).',
            $patch->name,
            $branch,
            $sha,
            $baseBranch,
        ));
    }

    /**
     * The apply itself: an escalation, not a single attempt.
     *
     * Each rung loosens something different, and none loosens what has to
     * *match* — the changed lines are compared exactly throughout:
     *
     *   1. straight — the patch as cut.
     *   2. three-way — resolves hunks plain context matching rejects, whenever
     *      the blobs the patch was generated against are in the repository,
     *      which for a drupal.org patch on its own project is common.
     *   3. reduced context — requires one line of surrounding context instead
     *      of three. The usual cause of needing this is not a stale patch but
     *      trailing-whitespace drift: drupal.org patches are generated against
     *      an export whose files may carry a trailing blank line the repository
     *      does not, so a hunk header promises seven context lines for a
     *      six-line file and git refuses all nine files over one of them.
     *
     * Only when all three fail is the patch genuinely stale, and then the
     * report names which files are stale rather than only that something was.
     */
    private function applyPatchFile(string $moduleDir, PatchApplication $patch, string $baseBranch): void
    {
        $attempts = [
            'straight' => PatchCheckout::applyArgs($patch->localPath),
            'three-way' => PatchCheckout::threeWayApplyArgs($patch->localPath),
            'reduced context' => PatchCheckout::reducedContextApplyArgs($patch->localPath),
        ];

        $first = true;
        foreach ($attempts as $label => $args) {
            if (!$first) {
                ($this->log)(sprintf('Retrying with %s ...', $label));
            }
            $first = false;

            $result = $this->runner->capture(array_merge(['git', '-C', $moduleDir], $args));
            if ($result->exitCode !== 0) {
                continue;
            }

            if ($label !== 'straight') {
                // A hunk placed on one line of context is a weaker guarantee
                // than one placed on three. The operator is told which they
                // got, because they are the one reviewing the result.
                ($this->log)(sprintf(
                    'Applied via %s — the patch did not match the working copy exactly; review the result with '
                    . 'that in mind.',
                    $label,
                ));
            }

            return;
        }

        // Nothing applied. Ask git what it wanted and where it failed, so the
        // report can name the stale file rather than just the stale patch.
        $stat = $this->runner->capture(
            array_merge(['git', '-C', $moduleDir], PatchCheckout::statArgs($patch->localPath)),
        );
        $check = $this->runner->capture(
            array_merge(['git', '-C', $moduleDir], PatchCheckout::checkArgs($patch->localPath)),
        );

        // Leave the working copy on the base rather than half-patched: the
        // next command must not inherit a tree nobody chose.
        $this->runner->tryRun(['git', '-C', $moduleDir, 'reset', '--hard']);
        $this->runner->tryRun(['git', '-C', $moduleDir, 'checkout', $baseBranch]);

        throw PatchCheckout::unappliableException($patch, $baseBranch, $stat->output, $check->output);
    }

    public function startWork(Environment $environment, IssueBranch $branch, ?string $baseBranch = null): bool
    {
        $moduleDir = $environment->projectPath . '/' . self::MODULE_DIR;

        $status = WorkingCopyStatus::inspect($moduleDir, $this->runner);
        if ($status->isDirty()) {
            throw new AdapterException(sprintf(
                "Cannot start work on \"%s\": the module working copy has uncommitted changes:\n  %s\n"
                . 'Commit or stash them first — starting here would mix them into the new branch.',
                $branch->name,
                implode("\n  ", $status->describe()),
            ));
        }

        // Existing work is resumed exactly as it stands. `checkout -B`, which
        // the disposable-branch paths use, would silently discard commits that
        // may exist nowhere else.
        $existing = $this->runner->tryRun(['git', '-C', $moduleDir, 'rev-parse', '--verify', $branch->name]);
        if ($existing !== null) {
            $this->runner->run(['git', '-C', $moduleDir, 'checkout', $branch->name]);
            ($this->log)(sprintf('Resumed existing work branch "%s".', $branch->name));
            $this->requireWorkingCopyBranch($environment->projectPath, $environment->moduleName, $branch->name);

            return true;
        }

        // A branch already pushed but not yet local — the maintainer started
        // this on another machine, or in another environment for another core.
        $remote = $this->runner->tryRun(
            ['git', '-C', $moduleDir, 'ls-remote', '--exit-code', '--heads', 'origin', $branch->name],
        );
        if ($remote !== null && trim($remote) !== '') {
            $this->runner->run(['git', '-C', $moduleDir, 'fetch', 'origin', $branch->name]);
            $this->runner->run(['git', '-C', $moduleDir, 'checkout', '-b', $branch->name, 'FETCH_HEAD']);
            ($this->log)(sprintf('Resumed work branch "%s" from origin.', $branch->name));
            $this->requireWorkingCopyBranch($environment->projectPath, $environment->moduleName, $branch->name);

            return true;
        }

        // The base defaults to whatever the working copy already sits on —
        // the same rule applyMr and applyPatch resolve against, so a branch
        // started here and a contribution checked out here share an origin.
        $base = $baseBranch ?? MrCheckout::resolveBaseBranch(
            self::trimmed($this->runner->tryRun(['git', '-C', $moduleDir, 'symbolic-ref', '--short', 'HEAD'])),
            self::trimmed($this->runner->tryRun(['git', '-C', $moduleDir, 'config', '--get', 'upkeep.base-branch'])),
        );

        ($this->log)(sprintf('Starting work branch "%s" off %s ...', $branch->name, $base));
        $this->runner->run(['git', '-C', $moduleDir, 'checkout', $base]);
        $this->runner->run(['git', '-C', $moduleDir, 'checkout', '-b', $branch->name, $base]);
        // Recorded so a later applyMr/applyPatch from this working copy knows
        // what the base was, exactly as those paths record it for each other.
        $this->runner->run(['git', '-C', $moduleDir, 'config', 'upkeep.base-branch', $base]);
        $this->requireWorkingCopyBranch($environment->projectPath, $environment->moduleName, $branch->name);

        return false;
    }

    public function recordedBaseBranch(Environment $environment): ?string
    {
        $recorded = self::trimmed($this->runner->tryRun([
            'git', '-C', $environment->projectPath . '/' . self::MODULE_DIR,
            'config', '--get', 'upkeep.base-branch',
        ]));

        return $recorded === '' ? null : $recorded;
    }

    public function promotePatch(
        Environment $environment,
        PatchApplication $patch,
        IssueBranch $branch,
        string $commitMessage,
    ): string {
        $moduleDir = $environment->projectPath . '/' . self::MODULE_DIR;

        // startWork owns the dirty-tree refusal and the resume-never-reset
        // rule. Promoting must not hold a second, subtly different copy of
        // either: the branch this lands on can be the only place the work
        // exists.
        $this->startWork($environment, $branch);

        ($this->log)(sprintf('Applying patch "%s" onto %s ...', $patch->name, $branch->name));

        // The work branch is what the patch is applied onto, so it is also
        // what a failure names and returns to — an unappliable patch leaves
        // the branch exactly as it was found.
        $this->applyPatchFile($moduleDir, $patch, $branch->name);

        $this->runner->run([
            'git', '-C', $moduleDir,
            '-c', 'user.name=upkeep',
            '-c', 'user.email=upkeep@localhost',
            'commit', '--no-verify', '-m', $commitMessage,
        ]);

        $sha = trim($this->runner->run(['git', '-C', $moduleDir, 'rev-parse', 'HEAD']));
        ($this->log)(sprintf('Committed onto %s at %s.', $branch->name, substr($sha, 0, 8)));

        return $sha;
    }

    private static function trimmed(?string $value): ?string
    {
        return $value === null ? null : trim($value);
    }

    public function pushWork(Environment $environment, IssueBranch $branch): string
    {
        $moduleDir = $environment->projectPath . '/' . self::MODULE_DIR;

        $status = WorkingCopyStatus::inspect($moduleDir, $this->runner);
        if ($status->isDirty()) {
            throw new AdapterException(sprintf(
                "Cannot publish \"%s\": the module working copy has uncommitted changes:\n  %s\n"
                . 'Commit them first — what is not committed cannot be pushed.',
                $branch->name,
                implode("\n  ", $status->describe()),
            ));
        }

        $head = self::trimmed($this->runner->tryRun(['git', '-C', $moduleDir, 'symbolic-ref', '--short', 'HEAD']));
        if ($head !== $branch->name) {
            throw new AdapterException(sprintf(
                'The module working copy is on "%s", not "%s". Publishing would push a branch you are not looking '
                . 'at; switch to it first.',
                $head ?? 'a detached HEAD',
                $branch->name,
            ));
        }

        // No --force, and no lease: a rejected push means the remote moved,
        // which is a thing to look at rather than to overwrite.
        ($this->log)(sprintf('Pushing %s to origin ...', $branch->name));
        $this->runner->run(['git', '-C', $moduleDir, 'push', '--set-upstream', 'origin', $branch->name]);

        return trim($this->runner->run(['git', '-C', $moduleDir, 'rev-parse', 'HEAD']));
    }

    public function loadFixture(Environment $environment, string $fixtureName): void
    {
        $this->ensureFixtureAddOn($environment);

        ($this->log)(sprintf('Loading fixture "%s" via the engine fixture command ...', $fixtureName));
        // The add-on command owns all fixture resolution and load semantics
        // (module scope, shared library, snapshot fast path) — the adapter
        // only invokes it and surfaces failure as AdapterException.
        $this->runner->run(['ddev', 'upkeep-fixture-load', $fixtureName], $environment->projectPath);
    }

    public function runChecks(Environment $environment, array $checks = []): CheckRunResult
    {
        $checks = $checks === [] ? self::DEFAULT_CHECKS : $checks;

        $needsToolchain = false;
        foreach ($checks as $check) {
            if (\in_array($check, self::TOOLCHAIN_CHECKS, true)) {
                $needsToolchain = true;
                break;
            }
        }
        if ($needsToolchain) {
            $this->ensureCheckToolchain($environment);
        }

        $results = [];
        foreach ($checks as $check) {
            ($this->log)(sprintf('Running check: %s ...', $check->value));
            $results[] = $this->runCheck($environment, $check);
        }

        return new CheckRunResult($results);
    }

    public function serve(Environment $environment): ServeResult
    {
        ($this->log)(sprintf('Ensuring module %s is installed ...', $environment->moduleName));
        $this->runner->run(['ddev', 'drush', 'pm:install', $environment->moduleName, '-y'], $environment->projectPath);

        $login = $this->runner->tryRun(
            ['ddev', 'drush', 'uli', '--uri=' . $environment->primaryUrl],
            $environment->projectPath,
        );

        return new ServeResult(
            url: $environment->primaryUrl,
            loginUrl: $login !== null && trim($login) !== '' ? trim($login) : null,
        );
    }

    public function resolveEnvPath(string $moduleName, string $coreMajor): ?string
    {
        $projectPath = rtrim($this->projectsRoot, '/') . '/' . ProjectName::for($moduleName, $coreMajor);

        if (!is_dir($projectPath) || !is_file($projectPath . '/' . EnvironmentMeta::FILENAME)) {
            return null;
        }

        return $projectPath;
    }

    public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus
    {
        $projectPath = $this->resolveEnvPath($moduleName, $coreMajor);
        if ($projectPath === null) {
            return null;
        }

        $moduleDir = $projectPath . '/' . self::MODULE_DIR;
        if (!is_dir($moduleDir)) {
            return null;
        }

        return WorkingCopyStatus::inspect($moduleDir, $this->runner);
    }

    public function checkoutBranch(Environment $environment, string $branch): void
    {
        $moduleDir = $environment->projectPath . '/' . self::MODULE_DIR;

        $status = WorkingCopyStatus::inspect($moduleDir, $this->runner);
        if ($status->isDirty()) {
            throw new AdapterException(sprintf(
                "Cannot switch branches: the module working copy has uncommitted changes:\n  %s",
                implode("\n  ", $status->describe()),
            ));
        }

        $result = $this->runner->tryRun(['git', '-C', $moduleDir, 'checkout', $branch]);
        if ($result === null) {
            $this->runner->run(['git', '-C', $moduleDir, 'fetch', 'origin']);
            $this->runner->run(['git', '-C', $moduleDir, 'checkout', $branch]);
        }

        ($this->log)(sprintf('Module working copy on branch "%s".', $branch));
        $this->requireWorkingCopyBranch($environment->projectPath, $environment->moduleName, $branch);
    }

    public function teardown(Module $module, string $coreMajor): void
    {
        $projectName = ProjectName::for($module->name, $coreMajor);
        $projectPath = rtrim($this->projectsRoot, '/') . '/' . $projectName;

        if (!is_dir($projectPath) && $this->describe($projectName) === null) {
            ($this->log)(sprintf('Environment %s does not exist — nothing to tear down.', $projectName));

            return;
        }

        $moduleDir = $projectPath . '/' . self::MODULE_DIR;
        if (is_dir($moduleDir)) {
            $status = WorkingCopyStatus::inspect($moduleDir, $this->runner);
            if ($status->hasLocalWork()) {
                throw new AdapterException(sprintf(
                    "Refusing to tear down %s: the module working copy has local work:\n  %s\n"
                    . 'Push or stash your work first.',
                    $projectName,
                    implode("\n  ", $status->describe()),
                ));
            }
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
    private function staleReasons(
        Module $module,
        string $coreMajor,
        ArtifactMeta $artifactMeta,
        string $projectName,
        string $projectPath,
    ): array {
        $metaPath = $projectPath . '/' . EnvironmentMeta::FILENAME;
        if (!is_file($metaPath)) {
            // The meta dotfile is written as the LAST provisioning step: its
            // absence means an interrupted provision, never a reusable env.
            return [sprintf(
                'No %s completion marker — a previous provision did not finish.',
                EnvironmentMeta::FILENAME,
            )];
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
        $described = $this->describe($projectName) ?? throw new AdapterException(sprintf(
            'Engine lost project %s between health check and reuse.',
            $projectName,
        ));

        if ($described->stringOrNull('status') !== 'running') {
            ($this->log)(sprintf('Environment %s is stopped — starting it.', $projectName));
            $this->runner->run(['ddev', 'start', '-y'], $projectPath);
            $described = $this->describe($projectName) ?? throw new AdapterException(sprintf(
                'Project %s did not come back after start.',
                $projectName,
            ));
        }

        // Stamp the reuse. `prune --older-than` filters on this: without it,
        // age is time-since-creation and prune deletes environments that are
        // being used daily.
        EnvironmentMeta::stampLastUsed($projectPath);

        return new Environment(
            moduleName: $module->name,
            coreMajor: $coreMajor,
            projectName: $projectName,
            projectPath: $projectPath,
            primaryUrl: $described->stringOrNull('primary_url') ?? '',
            reused: true,
        );
    }

    private function provision(
        Module $module,
        string $coreMajor,
        ArtifactMeta $artifactMeta,
        string $projectName,
        string $projectPath,
    ): Environment {
        ($this->log)(sprintf(
            'Provisioning environment %s (module %s, Drupal %s) ...',
            $projectName,
            $module->name,
            $coreMajor,
        ));

        // Asked before anything is built. Engine project names are global to
        // the machine while the projects root is configurable, so a moved root
        // collides with whatever the old one registered — and the engine
        // refuses that at `config` time, which is after a codebase has been
        // seeded and a repository cloned. Same refusal, a minute earlier, and
        // with the recovery command in it.
        $registration = ProjectRegistration::of($projectName, $this->describe($projectName));
        if ($registration->conflictsWith($projectPath)) {
            throw $registration->conflictException($projectPath);
        }

        // Warning suppressed, not the failure: the reason a projects root
        // cannot be created belongs in the AdapterException naming the path,
        // not in a PHP notice on stderr ahead of it.
        if (!is_dir($this->projectsRoot) && !@mkdir($this->projectsRoot, 0755, true) && !is_dir($this->projectsRoot)) {
            throw new AdapterException(sprintf('Could not create projects root "%s".', $this->projectsRoot));
        }

        try {
            ($this->log)('Seeding codebase from the canonical base tree ...');
            $this->runner->run(['cp', '-a', $this->layout->treePath($coreMajor), $projectPath]);

            ($this->log)('Cloning the module working copy ...');
            $this->runner->run([
                'git', 'clone', self::GIT_BASE_URL . $module->project . '.git', $projectPath . '/' . self::MODULE_DIR,
            ]);

            ($this->log)('Configuring the engine project ...');
            $this->runner->run([
                'ddev', 'config',
                '--project-type=' . sprintf('drupal%s', $coreMajor),
                '--docroot=web',
                '--project-name=' . $projectName,
            ], $projectPath);

            ($this->log)(sprintf(
                'Installing engine add-on %s at pinned version %s ...',
                EngineAddOn::NAME,
                EngineAddOn::VERSION,
            ));
            $this->runner->run(
                ['ddev', 'add-on', 'get', EngineAddOn::NAME, '--version', EngineAddOn::VERSION],
                $projectPath,
            );
            $this->adaptAddOnConfig($projectPath);
            $this->ensureFixtureAddOn($projectPath);

            ($this->log)('Starting the environment ...');
            $this->runner->run(['ddev', 'start', '-y'], $projectPath);

            ($this->log)('Requiring drush (container-side composer) ...');
            $this->runner->run(['ddev', 'composer', 'require', 'drush/drush', '--no-interaction'], $projectPath);

            $this->wireModule($module, $projectPath);

            ($this->log)('Restoring the clean-install database snapshot ...');
            $this->runner->run(['ddev', 'import-db', '--file=' . $this->layout->dumpPath($coreMajor)], $projectPath);

            // Provisioning health gate: the environment must bootstrap and
            // report the requested core major — read from the project itself.
            $drupalVersion = trim($this->runner->run(
                ['ddev', 'drush', 'status', '--field=drupal-version'],
                $projectPath,
            ));
            if (!str_starts_with($drupalVersion, $coreMajor . '.')) {
                throw new AdapterException(sprintf(
                    'Environment %s reports Drupal "%s", expected major %s.',
                    $projectName,
                    $drupalVersion,
                    $coreMajor,
                ));
            }
            ($this->log)(sprintf('Environment reports Drupal %s.', $drupalVersion));

            $described = $this->describe($projectName) ?? throw new AdapterException(sprintf(
                'Engine does not report project %s after provisioning.',
                $projectName,
            ));

            // Written LAST: the completion marker. A crash before this line
            // leaves no marker, and the next ensure_env re-provisions. The
            // write is checked — a silently missing marker would make every
            // later invocation tear down and rebuild this environment.
            $now = new \DateTimeImmutable();
            (new EnvironmentMeta(
                $module->name,
                $coreMajor,
                $artifactMeta->coreVersion,
                EngineAddOn::VERSION,
                $now,
                $now,
            ))->writeTo($projectPath);

            return new Environment(
                moduleName: $module->name,
                coreMajor: $coreMajor,
                projectName: $projectName,
                projectPath: $projectPath,
                primaryUrl: $described->stringOrNull('primary_url') ?? '',
                reused: false,
            );
        } catch (\Throwable $e) {
            ($this->log)(sprintf('Provisioning failed — tearing down partial environment %s.', $projectName));
            try {
                $this->teardownProject($projectName, $projectPath);
            } catch (\Throwable $cleanup) {
                ($this->log)('Cleanup after failed provisioning also failed: ' . $cleanup->getMessage());
            }

            // The pre-flight above catches this when the engine can still
            // describe the old registration. It cannot when the old directory
            // is gone but the record survives — so the engine's own refusal is
            // translated too, rather than surfacing as an unactionable wall of
            // its output.
            if (ProjectRegistration::isRootConflict($e->getMessage())) {
                throw ProjectRegistration::of($projectName, null)->rootConflictException($projectPath, $e);
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
        $composerJson = @file_get_contents($composerJsonPath);
        if ($composerJson === false) {
            throw new AdapterException(sprintf('Cannot read the project composer.json at "%s".', $composerJsonPath));
        }
        // Read-modify-write over a file the environment cannot function
        // without: the rewrite is atomic, so a crash can never leave an
        // unparseable composer.json behind a still-valid completion marker.
        FileWriter::write(
            $composerJsonPath,
            ModuleWiring::withPathRepository($composerJson, './' . self::MODULE_DIR),
        );

        // Pin the exact branch the working copy has checked out: a bare
        // "*@dev" could resolve to a different dev branch published on
        // packages.drupal.org instead of the path repository.
        $branch = trim($this->runner->run(
            ['git', '-C', $projectPath . '/' . self::MODULE_DIR, 'symbolic-ref', '--short', 'HEAD'],
        ));
        $this->requireWorkingCopyBranch($projectPath, $module->name, $branch);
    }

    /**
     * (Re-)pins the project's composer requirement to the branch the module
     * working copy has checked out, resolving through the path repository.
     * Every branch switch in the working copy must be followed by this sync:
     * a stale pin (e.g. "1.0.x-dev" while the checkout is mr-2) makes every
     * later composer resolution in the project unsatisfiable. The partial
     * update also materializes dependencies the checked-out branch newly
     * requires in the module's composer.json.
     */
    private function requireWorkingCopyBranch(string $projectPath, string $moduleName, string $branch): void
    {
        $this->runner->run([
            'ddev', 'composer', 'require',
            sprintf('drupal/%s:%s', $moduleName, ModuleWiring::devConstraintForBranch($branch)),
            '--no-interaction',
        ], $projectPath);

        $installedPath = sprintf('%s/web/modules/contrib/%s', $projectPath, $moduleName);
        if (!is_link($installedPath)) {
            throw new AdapterException(sprintf(
                'Module wiring violated the ownership constraint: "%s" is not a symlink into the working copy '
                . '(composer mirrored the package instead).',
                $installedPath,
            ));
        }
    }

    /**
     * Installs the ddev-upkeep fixture add-on when the environment lacks it.
     * Called during provisioning (new environments get it from birth) and
     * lazily by loadFixture() (environments provisioned before the add-on
     * became part of the layout get it on first use, without re-provisioning).
     */
    private function ensureFixtureAddOn(Environment|string $environmentOrPath): void
    {
        $projectPath = $environmentOrPath instanceof Environment ? $environmentOrPath->projectPath : $environmentOrPath;
        if (is_file($projectPath . '/.ddev/' . FixtureAddOn::MARKER)) {
            return;
        }

        ($this->log)(sprintf('Installing fixture add-on from %s ...', FixtureAddOn::source()));
        $this->runner->run(['ddev', 'add-on', 'get', FixtureAddOn::source()], $projectPath);
    }

    /**
     * Ensures the check toolchain is present in the environment (probe:
     * vendor/bin binaries). Installs TOOLCHAIN_PACKAGES container-side and
     * allows the composer plugins they need (the phpcs standards installer;
     * extension-installer is already allowed by the base tree).
     */
    private function ensureCheckToolchain(Environment $environment): void
    {
        $binaries = ['phpunit', 'phpstan', 'phpcs'];
        $allPresent = true;
        foreach ($binaries as $binary) {
            if (!is_file($environment->projectPath . '/vendor/bin/' . $binary)) {
                $allPresent = false;
                break;
            }
        }
        if ($allPresent) {
            return;
        }

        ($this->log)('Provisioning the check toolchain (core-dev, coder, phpstan-drupal) ...');
        $this->runner->run([
            'ddev', 'composer', 'config', '--no-plugins',
            'allow-plugins.dealerdirect/phpcodesniffer-composer-installer', 'true',
        ], $environment->projectPath);
        $packages = array_map(
            static fn (string $package): string => sprintf($package, $environment->coreMajor),
            self::TOOLCHAIN_PACKAGES,
        );
        $this->runner->run([
            'ddev', 'composer', 'require', '--dev', '--with-all-dependencies', '--no-interaction',
            ...$packages,
        ], $environment->projectPath);
    }

    /**
     * The one dispatch point for every check type: each arm names both how the
     * check is run and, for the command checks, the exact command line.
     * Exhaustive over CheckType with no default arm on purpose — adding a case
     * to the enum then fails here, at the place that has to decide how to run
     * it, rather than silently falling through to a command that does not
     * exist. Keeping the command lines in the arms is what makes that possible
     * without a second, partial match somewhere downstream.
     */
    private function runCheck(Environment $environment, CheckType $check): CheckResult
    {
        return match ($check) {
            CheckType::Deprecation => $this->runDeprecationCheck($environment),
            CheckType::FunctionalSmoke => $this->runSmokeCheck($environment),
            // Engine command as shipped: an existing path argument makes it
            // run exactly that directory (host-side path, container cwd is
            // the project root).
            CheckType::PhpUnit => $this->runCommandCheck($environment, $check, [
                'ddev', 'phpunit', sprintf('web/%s/%s', EngineAddOn::PROJECTS_PATH, $environment->moduleName),
            ]),
            CheckType::EsLint, CheckType::StyleLint
                => $this->runCommandCheck($environment, $check, ['ddev', $check->value]),
            CheckType::PhpCs => $this->runCommandCheck($environment, $check, [
                'ddev', 'exec', 'bash', '-c', implode("\n", [
                    'set -eu',
                    'test -e phpcs.xml.dist || curl -sSOL https://git.drupalcode.org/project/'
                    . 'gitlab_templates/-/raw/default-ref/assets/phpcs.xml.dist',
                    sprintf(
                        'phpcs -s --report-full --report-summary --report-source %s --ignore=*/.ddev/*',
                        self::containerModulePath($environment),
                    ),
                ]),
            ]),
            CheckType::PhpStan => $this->runCommandCheck($environment, $check, [
                'ddev', 'exec', 'bash', '-c', implode("\n", [
                    'set -eu',
                    'test -e phpstan.neon || curl -sSOL https://git.drupalcode.org/project/'
                    . 'gitlab_templates/-/raw/default-ref/assets/phpstan.neon',
                    "sed -i 's/BASELINE_PLACEHOLDER/phpstan-baseline.neon/g' phpstan.neon",
                    'test -e phpstan-baseline.neon || touch phpstan-baseline.neon',
                    'phpstan analyze ' . self::containerModulePath($environment),
                ]),
            ]),
            CheckType::ModuleInstall => $this->runCommandCheck($environment, $check, [
                'ddev', 'drush', 'pm:install', $environment->moduleName, '-y',
            ]),
        };
    }

    /**
     * The in-container path of the module under maintenance, for the phpcs and
     * phpstan invocations.
     *
     * Checks target exactly that module — never all of DRUPAL_PROJECTS_PATH,
     * where composer also materializes the module's real dependencies (their
     * packaged code must not pollute results). $DDEV_DOCROOT and
     * $DRUPAL_PROJECTS_PATH expand inside the web container. The module name is
     * quoted rather than trusted: it is spliced into a shell script body, so
     * its safety must not depend on a validator two classes away.
     */
    private static function containerModulePath(Environment $environment): string
    {
        return sprintf(
            '"$DDEV_DOCROOT/$DRUPAL_PROJECTS_PATH"/%s',
            ShellArgument::quote($environment->moduleName),
        );
    }

    /**
     * A check that is just a command line run inside the environment: the
     * outcome is data, so a non-zero exit becomes a failed CheckResult rather
     * than an exception, and a timeout becomes a timed-out one.
     *
     * @param list<string> $command
     */
    private function runCommandCheck(Environment $environment, CheckType $check, array $command): CheckResult
    {
        $process = $this->runner->capture($command, $environment->projectPath, self::CHECK_TIMEOUT);
        if ($process->timedOut) {
            return CheckResult::timedOut($check, $process->output, $process->durationSeconds, self::CHECK_TIMEOUT);
        }

        return CheckResult::fromProcess($check, $process->exitCode, $process->output, $process->durationSeconds);
    }

    /**
     * Front page over HTTP with the module enabled: requests the
     * environment's primary URL and passes only on HTTP 200.
     */
    private function runSmokeCheck(Environment $environment): CheckResult
    {
        $process = $this->runner->capture(
            ['curl', '-ksS', '-o', '/dev/null', '-w', '%{http_code}', $environment->primaryUrl],
            $environment->projectPath,
            120,
        );
        if ($process->timedOut) {
            return CheckResult::timedOut(CheckType::FunctionalSmoke, $process->output, $process->durationSeconds, 120);
        }
        if ($process->exitCode !== 0) {
            return new CheckResult(
                CheckType::FunctionalSmoke,
                CheckStatus::Failed,
                $process->exitCode,
                'Request failed: ' . $process->output,
                $process->durationSeconds,
            );
        }

        $httpCode = trim($process->output);
        $status = $httpCode === '200' ? CheckStatus::Passed : CheckStatus::Failed;

        return new CheckResult(
            CheckType::FunctionalSmoke,
            $status,
            0,
            sprintf('GET %s -> HTTP %s', $environment->primaryUrl, $httpCode),
            $process->durationSeconds,
        );
    }

    /**
     * Deprecation/upgrade-status: only when the pinned engine provides a
     * command for it; otherwise an explicit Unavailable result — never a
     * silent omission. ddev-drupal-contrib 1.1.5 ships no such command.
     */
    private function runDeprecationCheck(Environment $environment): CheckResult
    {
        $commandPath = $environment->projectPath . '/.ddev/commands/web/upgrade-status';
        if (!is_file($commandPath)) {
            return CheckResult::unavailable(CheckType::Deprecation, sprintf(
                'The pinned engine add-on (%s %s) provides no deprecation/upgrade-status command for Drupal %s.',
                EngineAddOn::NAME,
                EngineAddOn::VERSION,
                $environment->coreMajor,
            ));
        }

        $process = $this->runner->capture(['ddev', 'upgrade-status'], $environment->projectPath, self::CHECK_TIMEOUT);
        if ($process->timedOut) {
            return CheckResult::timedOut(
                CheckType::Deprecation,
                $process->output,
                $process->durationSeconds,
                self::CHECK_TIMEOUT,
            );
        }

        return CheckResult::fromProcess(
            CheckType::Deprecation,
            $process->exitCode,
            $process->output,
            $process->durationSeconds,
        );
    }

    private function adaptAddOnConfig(string $projectPath): void
    {
        $configPath = $projectPath . '/.ddev/' . EngineAddOn::CONFIG_FILENAME;
        // One read decides it: the add-on either installed a readable config
        // or it did not, and both spellings of "it did not" are the same
        // problem for the caller — the adaptation cannot proceed.
        $config = is_file($configPath) ? @file_get_contents($configPath) : false;
        if ($config === false) {
            throw new AdapterException(sprintf(
                'Cannot read the engine add-on config at "%s" — the add-on did not install it, or it is unreadable.',
                $configPath,
            ));
        }
        FileWriter::write($configPath, EngineAddOn::adaptContribConfig($config));
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
            ($this->log)(
                'Engine delete reported a failure (project may not be registered) — continuing '
                . 'with tree removal.',
            );
        }
        if (is_dir($projectPath)) {
            $this->runner->run(['rm', '-rf', $projectPath]);
        }
        ($this->log)(sprintf('Environment %s torn down.', $projectName));
    }

    /**
     * The engine's project description, or null when the project is unknown
     * to it.
     */
    private function describe(string $projectName): ?EngineDescription
    {
        return EngineDescription::fromJson($this->runner->tryRun(['ddev', 'describe', $projectName, '-j']));
    }

    private function requireArtifactMeta(string $coreMajor): ArtifactMeta
    {
        $metaPath = $this->layout->metaPath($coreMajor);
        if (!is_file($metaPath)) {
            throw new AdapterException(sprintf(
                'No base artifacts for Drupal %s (missing %s). Run `upkeep base-artifacts:build --version=%s` first.',
                $coreMajor,
                $metaPath,
                $coreMajor,
            ));
        }

        try {
            return ArtifactMeta::fromYaml((string) file_get_contents($metaPath));
        } catch (MetaException $e) {
            throw new AdapterException(
                sprintf('Base artifact meta for Drupal %s is unreadable: %s', $coreMajor, $e->getMessage()),
                previous: $e,
            );
        }
    }
}

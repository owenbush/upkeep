<?php

declare(strict_types=1);

namespace Upkeep\Tests\Support;

use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\IssueBranch;
use Upkeep\Adapter\PatchApplication;
use Upkeep\Adapter\ServeResult;
use Upkeep\Adapter\WorkingCopyStatus;
use Upkeep\Cockpit\Module;
use Upkeep\Gitlab\MergeRequest;

/**
 * A configurable EngineAdapterInterface double for the command tests.
 *
 * Every operation defaults to "not exercised by this test" (a
 * BadMethodCallException, so an unexpected call is loud); the named
 * constructors switch on only what the scenario under test needs.
 */
final class FakeEngineAdapter implements EngineAdapterInterface
{
    /** @var list<string> */
    public array $checkedOutBranches = [];

    /** @var list<string> fixture names loaded, in order */
    public array $loadedFixtures = [];

    /** @var list<PatchApplication> patches applied, in order */
    public array $appliedPatches = [];

    /** @var list<string> work branches started or resumed, in order */
    public array $startedBranches = [];

    /** @var list<string> the base each work branch was started from */
    public array $startedBases = [];

    /** @var list<string> work branches pushed, in order */
    public array $pushedBranches = [];

    private function __construct(
        private readonly ?Environment $environment = null,
        private readonly ?string $envPath = null,
        private readonly ?\Throwable $failure = null,
        private readonly ?\Throwable $branchFailure = null,
        private readonly ?CheckRunResult $checkRun = null,
        private readonly ?ServeResult $serveResult = null,
        private readonly ?\Throwable $fixtureFailure = null,
        private readonly ?\Throwable $patchFailure = null,
        private readonly ?\Throwable $workFailure = null,
        private readonly bool $resumeWork = false,
        private readonly ?\Throwable $pushFailure = null,
        private readonly string $pushedSha = 'abc1234def5678',
    ) {
    }

    /** An environment whose push is rejected — the remote moved. */
    public static function withFailingPush(Environment $environment, \Throwable $failure): self
    {
        return new self(environment: $environment, pushFailure: $failure);
    }

    /** An environment where the issue's work branch already exists. */
    public static function resumingWork(Environment $environment): self
    {
        return new self(environment: $environment, resumeWork: true);
    }

    /** An environment that refuses to start work — a dirty working copy. */
    public static function withFailingWorkStart(Environment $environment, \Throwable $failure): self
    {
        return new self(environment: $environment, workFailure: $failure);
    }

    /** An environment whose patch apply fails — a patch needing a re-roll. */
    public static function withFailingPatchApply(Environment $environment, \Throwable $failure): self
    {
        return new self(environment: $environment, patchFailure: $failure);
    }

    /** An environment that applies patches and then reports these checks. */
    public static function withPatchCheckRun(Environment $environment, CheckRunResult $run): self
    {
        return new self(environment: $environment, checkRun: $run);
    }

    public static function withEnvironment(Environment $environment): self
    {
        return new self(environment: $environment);
    }

    public static function withEnvPath(?string $envPath): self
    {
        return new self(envPath: $envPath);
    }

    /** An environment whose checks have already been decided by the test. */
    public static function withCheckRun(Environment $environment, CheckRunResult $run): self
    {
        return new self(environment: $environment, checkRun: $run);
    }

    /** An environment whose fixture load fails — an unknown fixture name. */
    public static function withFailingFixtureLoad(Environment $environment, \Throwable $failure): self
    {
        return new self(environment: $environment, fixtureFailure: $failure);
    }

    /** An environment the engine can put in front of a browser. */
    public static function withServeResult(Environment $environment, ServeResult $serve): self
    {
        return new self(environment: $environment, serveResult: $serve);
    }

    public static function failing(\Throwable $failure): self
    {
        return new self(failure: $failure);
    }

    public static function withFailingBranchCheckout(Environment $environment, \Throwable $failure): self
    {
        return new self(environment: $environment, branchFailure: $failure);
    }

    public function ensureEnv(Module $module, string $coreMajor): Environment
    {
        if ($this->failure !== null) {
            throw $this->failure;
        }

        return $this->environment ?? throw new \BadMethodCallException('ensureEnv() not configured');
    }

    /** @var list<MergeRequest> merge requests applied, in order */
    public array $appliedMrs = [];

    public function applyMr(Environment $environment, MergeRequest $mergeRequest): void
    {
        $this->appliedMrs[] = $mergeRequest;
    }

    public function applyPatch(Environment $environment, PatchApplication $patch): void
    {
        if ($this->patchFailure !== null) {
            throw $this->patchFailure;
        }

        $this->appliedPatches[] = $patch;
    }

    public function startWork(Environment $environment, IssueBranch $branch, ?string $baseBranch = null): bool
    {
        if ($this->workFailure !== null) {
            throw $this->workFailure;
        }

        $this->startedBranches[] = $branch->name;
        $this->startedBases[] = $baseBranch ?? '(from working copy)';

        return $this->resumeWork;
    }

    /** What startWork/promotePatch would have recorded; null means nothing was. */
    public ?string $baseBranch = null;

    public function recordedBaseBranch(Environment $environment): ?string
    {
        return $this->baseBranch;
    }

    /**
     * Records the branch and, above all, the message — the message is what a
     * promoted patch's attribution *is*, so a test that did not assert on it
     * would not be testing the feature.
     *
     * @var list<array{branch: string, patch: string, message: string}>
     */
    public array $promotions = [];

    public ?\Throwable $promoteFailure = null;

    public string $promotedSha = 'prom0ted00000000000000000000000000000000';

    public function promotePatch(
        Environment $environment,
        PatchApplication $patch,
        IssueBranch $branch,
        string $commitMessage,
    ): string {
        if ($this->promoteFailure !== null) {
            throw $this->promoteFailure;
        }

        $this->promotions[] = [
            'branch' => $branch->name,
            'patch' => $patch->name,
            'message' => $commitMessage,
        ];

        return $this->promotedSha;
    }

    public function pushWork(Environment $environment, IssueBranch $branch): string
    {
        if ($this->pushFailure !== null) {
            throw $this->pushFailure;
        }

        $this->pushedBranches[] = $branch->name;

        return $this->pushedSha;
    }

    public function loadFixture(Environment $environment, string $fixtureName): void
    {
        if ($this->fixtureFailure !== null) {
            throw $this->fixtureFailure;
        }

        $this->loadedFixtures[] = $fixtureName;
    }

    public function runChecks(Environment $environment, array $checks = []): CheckRunResult
    {
        return $this->checkRun ?? throw new \BadMethodCallException('runChecks() not configured');
    }

    public function serve(Environment $environment): ServeResult
    {
        return $this->serveResult ?? throw new \BadMethodCallException('serve() not configured');
    }

    public function resolveEnvPath(string $moduleName, string $coreMajor): ?string
    {
        return $this->envPath;
    }

    public function teardown(Module $module, string $coreMajor): void
    {
    }

    /** Null is "no provisioned environment", which is a case commands handle. */
    public ?WorkingCopyStatus $workingCopy = null;

    public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus
    {
        return $this->workingCopy;
    }

    public function checkoutBranch(Environment $environment, string $branch): void
    {
        if ($this->branchFailure !== null) {
            throw $this->branchFailure;
        }

        $this->checkedOutBranches[] = $branch;
    }
}

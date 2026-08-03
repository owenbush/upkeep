<?php

declare(strict_types=1);

namespace Upkeep\Tests\Support;

use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\Environment;
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

    private function __construct(
        private readonly ?Environment $environment = null,
        private readonly ?string $envPath = null,
        private readonly ?\Throwable $failure = null,
        private readonly ?\Throwable $branchFailure = null,
        private readonly ?CheckRunResult $checkRun = null,
        private readonly ?ServeResult $serveResult = null,
        private readonly ?\Throwable $fixtureFailure = null,
    ) {
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

    public function applyMr(Environment $environment, MergeRequest $mergeRequest): void
    {
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

    public function inspectWorkingCopy(string $moduleName, string $coreMajor): ?WorkingCopyStatus
    {
        return null;
    }

    public function checkoutBranch(Environment $environment, string $branch): void
    {
        if ($this->branchFailure !== null) {
            throw $this->branchFailure;
        }

        $this->checkedOutBranches[] = $branch;
    }
}

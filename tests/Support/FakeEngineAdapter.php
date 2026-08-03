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

    private function __construct(
        private readonly ?Environment $environment = null,
        private readonly ?string $envPath = null,
        private readonly ?\Throwable $failure = null,
        private readonly ?\Throwable $branchFailure = null,
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
    }

    public function runChecks(Environment $environment, array $checks = []): CheckRunResult
    {
        throw new \BadMethodCallException('runChecks() not configured');
    }

    public function serve(Environment $environment): ServeResult
    {
        throw new \BadMethodCallException('serve() not configured');
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

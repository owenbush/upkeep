<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Aggregate outcome of EngineAdapterInterface::runChecks().
 */
final readonly class CheckRunResult
{
    /**
     * @param list<CheckResult> $results one entry per requested check, in run order
     */
    public function __construct(public array $results)
    {
    }

    public function allPassed(): bool
    {
        return array_all($this->results, static fn (CheckResult $result): bool => $result->passed);
    }

    /**
     * @return list<CheckResult>
     */
    public function failures(): array
    {
        return array_values(array_filter($this->results, static fn (CheckResult $result): bool => !$result->passed));
    }
}

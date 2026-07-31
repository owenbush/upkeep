<?php

declare(strict_types=1);

namespace Upkeep\Gate;

/**
 * Outcome of one gate classification: a status plus every machine-readable
 * reason that denied READY-AUTO (empty exactly when the row is READY-AUTO).
 *
 * All deny reasons are collected, not just the first: a draft bot MR with no
 * pipeline is denied by both facts independently, and consumers (dashboard,
 * task 14's merge review) should see the complete picture.
 */
final readonly class GateVerdict
{
    /**
     * @param list<string> $reasons machine-readable reason tokens
     *                              (e.g. "ci-red", "local-failed:phpunit")
     */
    public function __construct(
        public GateStatus $status,
        public array $reasons = [],
    ) {
    }

    /** Rendered form for the dashboard STATUS column: "REVIEW draft, ci-missing". */
    public function describe(): string
    {
        return $this->status->value
            . ($this->reasons === [] ? '' : ' ' . implode(', ', $this->reasons));
    }
}

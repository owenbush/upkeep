<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Results\CachedResult;

/**
 * What is known locally about one contribution, across every core it was
 * checked on.
 *
 * Core stopped being part of a row's identity and became part of its evidence
 * — a module *branch* supports several cores at once (pathauto's single
 * 8.x-1.x declares `^10.2 || ^11 || ^12`), so multiplying rows by a tracked
 * core list produced duplicates that said nothing new. See
 * `docs/dashboard-row-model.md`.
 *
 * That leaves one question this answers: several cores can disagree, and the
 * cell is one string. **Worst case wins, and names the core it came from.**
 * The actionable fact is that something is broken and where; a cell reading
 * `pass` because two of three cores were green would be worse than useless.
 * Every core is listed individually under `-v`.
 */
final readonly class LocalEvidence
{
    /**
     * @param array<array-key, ?CachedResult> $byCore keyed by core major; a
     *        null value is a core that applies but was never checked.
     *        **array-key, not string**, and not by choice: PHP stores "10" =>
     *        x as 10 => x, so no caller can hand this string keys however they
     *        write them. Declaring string here would be a type that is false
     *        at every call site; the cast happens in cores() instead.
     * @param ?string $currentRevision what the evidence must match to be
     *                                 fresh — an MR head SHA, or a patch's
     *                                 revision. Null makes everything stale,
     *                                 which is the safe direction.
     */
    private function __construct(
        private array $byCore,
        private ?string $currentRevision,
    ) {
    }

    /**
     * @param array<array-key, ?CachedResult> $byCore see the constructor
     */
    public static function of(array $byCore, ?string $currentRevision): self
    {
        return new self($byCore, $currentRevision);
    }

    /** Nothing applicable, nothing known — a row with no evidence at all. */
    public static function none(): self
    {
        return new self([], null);
    }

    /**
     * The cores this evidence covers, in order.
     *
     * @return list<string>
     */
    public function cores(): array
    {
        // Cast, because PHP silently turns a numeric string array key into an
        // int: "10" => x is stored as 10 => x, and array_keys() then hands
        // back list<int> where list<string> is declared. The same coercion
        // has already caught ModuleSummary once.
        $cores = array_map(strval(...), array_keys($this->byCore));
        usort($cores, static fn (string $a, string $b): int => (int) $a <=> (int) $b);

        return $cores;
    }

    /**
     * Whether every applicable core is green against the current revision.
     *
     * The fast lane's question, and stricter than what it used to ask. A row
     * per (subject x core) meant a merge request green on 11 and unchecked on
     * 10 produced one READY-AUTO row and one ordinary one, and the fast lane
     * saw the ready one — partial evidence, silently. One row cannot do that:
     * a core that applies and has no fresh pass denies it.
     */
    public function allGreen(): bool
    {
        if ($this->byCore === []) {
            return false;
        }

        foreach ($this->cores() as $core) {
            if ($this->stateOf($core) !== 'pass') {
                return false;
            }
        }

        return true;
    }

    /** Whether any applicable core has a fresh failing result. */
    public function anyFailed(): bool
    {
        return $this->coresIn('fail') !== [];
    }

    /** Whether any applicable core's evidence is out of date. */
    public function anyStale(): bool
    {
        return $this->coresIn('stale') !== [];
    }

    /** Whether any applicable core has never been checked. */
    public function anyUnchecked(): bool
    {
        return $this->coresIn('unchecked') !== [];
    }

    /**
     * The cell: worst case, naming the core it came from.
     *
     * Ordered by how much it should worry a maintainer — a failure outranks
     * stale evidence, which outranks a gap, which outranks a pass. Only the
     * worst state's cores are named, because listing the passing ones beside a
     * failure buries the fact that matters.
     */
    public function cell(): string
    {
        if ($this->byCore === []) {
            return '–';
        }

        foreach (['fail', 'stale'] as $state) {
            $cores = $this->coresIn($state);
            if ($cores !== []) {
                return $state . ' ' . implode(',', $cores);
            }
        }

        $passed = $this->coresIn('pass');
        $unchecked = $this->coresIn('unchecked');

        // Nothing checked anywhere reads as the plain dash it always did.
        if ($passed === []) {
            return '–';
        }

        // Green where checked, with the gap named — a pass that covers only
        // half the applicable cores must not read like a full one.
        return $unchecked === []
            ? 'pass ' . implode(',', $passed)
            : 'pass ' . implode(',', $passed) . ' · ? ' . implode(',', $unchecked);
    }

    /**
     * Every core with its own state, for `-v`. The detail half of
     * worst-case-plus-detail: the cell says what is wrong, this says where.
     */
    public function describe(): string
    {
        if ($this->byCore === []) {
            return '–';
        }

        $parts = [];
        foreach ($this->cores() as $core) {
            $parts[] = $core . ':' . $this->stateOf($core);
        }

        return implode(' ', $parts);
    }

    /**
     * @return list<string>
     */
    private function coresIn(string $state): array
    {
        return array_values(array_filter(
            $this->cores(),
            fn (string $core): bool => $this->stateOf($core) === $state,
        ));
    }

    /** pass | fail | stale | unchecked */
    private function stateOf(string $core): string
    {
        $result = $this->byCore[$core] ?? null;
        if ($result === null) {
            return 'unchecked';
        }

        if ($this->currentRevision === null || $result->sha !== $this->currentRevision) {
            return 'stale';
        }

        return $result->result->allPassed() ? 'pass' : 'fail';
    }
}

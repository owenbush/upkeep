<?php

declare(strict_types=1);

namespace Upkeep\Gate;

use Upkeep\Config\BotPattern;
use Upkeep\Dashboard\LocalEvidence;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\PipelineStatus;

/**
 * The fast-lane gate classifier: decides, for one MR row on the dashboard,
 * whether it is READY-AUTO (eligible for the one-keypress human-approved
 * merge) or must route to REVIEW/BLOCKED with machine-readable reasons.
 *
 * Deliberately conservative pure logic with no I/O: READY-AUTO requires
 * EVERY condition to hold, on EVERY core the row applies to; any unknown
 * (missing pipeline, missing or stale local results) denies. A
 * misclassification here merges the wrong thing, so this class stays
 * standalone and exhaustively unit-tested.
 */
final class FastLaneGate
{
    /**
     * Yields the bot pattern to gate against for one target core version.
     *
     * @var \Closure(string): BotPattern
     */
    private readonly \Closure $patternForCore;

    /**
     * @param ?callable(string): BotPattern $patternForCore defaults to
     *                                                      BotPattern::forCore(), the one config point for
     *                                                      per-core pattern differences
     */
    public function __construct(?callable $patternForCore = null)
    {
        $this->patternForCore = $patternForCore === null ? BotPattern::forCore(...) : $patternForCore(...);
    }

    /**
     * @param list<string>  $cores the cores that apply to this row — every one
     *                             of them must be green for READY-AUTO
     * @param LocalEvidence $local what is known locally across those cores
     */
    public function classify(MergeRequest $mr, array $cores, LocalEvidence $local): GateVerdict
    {
        $reasons = [];
        $blocked = false;

        // The bot pattern is asked per core because BotPattern::forCore() is
        // the one place a future per-core bot account or branch would land.
        // Every applicable core must agree, and no cores at all is not
        // agreement — a row with nothing to test on is not a row to merge.
        if ($cores === [] || !self::matchesEveryCore($this->patternForCore, $cores, $mr)) {
            $reasons[] = 'not-bot-author';
        }

        // Draft is signaled two ways by the API (the draft flag and
        // detailed_merge_status "draft_status"); either alone denies.
        if ($mr->draft || $mr->detailedMergeStatus === 'draft_status') {
            $reasons[] = 'draft';
        }

        $pipeline = $mr->headPipeline;
        if ($pipeline === null) {
            $reasons[] = 'ci-missing';
        } elseif ($pipeline->status === PipelineStatus::Failed) {
            $reasons[] = 'ci-red';
            $blocked = true;
        } elseif (!$pipeline->status->isGreen()) {
            $reasons[] = 'ci-not-green:'
                . ($pipeline->rawStatus !== '' ? $pipeline->rawStatus : $pipeline->status->value);
        }

        // Local evidence must exist for *every* applicable core AND be for the
        // MR's current head SHA. This is where the row model changed the
        // answer: a merge request green on 11 and unchecked on 10 used to be
        // two rows, one of them READY-AUTO, and the fast lane took the ready
        // one — merging on evidence that covered half the cores, with nothing
        // saying so. One row cannot hide it.
        if ($local->cores() === [] || $local->anyUnchecked()) {
            $reasons[] = 'local-missing';
        }
        if ($local->anyStale()) {
            $reasons[] = 'local-stale';
        }
        foreach ($local->failedChecks() as $check) {
            $reasons[] = 'local-failed:' . $check;
        }

        if ($reasons === []) {
            return new GateVerdict(GateStatus::ReadyAuto);
        }

        return new GateVerdict($blocked ? GateStatus::Blocked : GateStatus::Review, $reasons);
    }

    /**
     * @param \Closure(string): BotPattern $patternForCore
     * @param list<string>                 $cores
     */
    private static function matchesEveryCore(\Closure $patternForCore, array $cores, MergeRequest $mr): bool
    {
        foreach ($cores as $core) {
            if (!$patternForCore($core)->matches($mr->authorUsername, $mr->authorId, $mr->sourceBranch)) {
                return false;
            }
        }

        return true;
    }
}

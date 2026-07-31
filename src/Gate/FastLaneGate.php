<?php

declare(strict_types=1);

namespace Upkeep\Gate;

use Upkeep\Config\BotPattern;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\PipelineStatus;
use Upkeep\Results\CachedResult;

/**
 * The fast-lane gate classifier: decides, for one MR row on the dashboard,
 * whether it is READY-AUTO (eligible for the one-keypress human-approved
 * merge) or must route to REVIEW/BLOCKED with machine-readable reasons.
 *
 * Deliberately conservative pure logic with no I/O: READY-AUTO requires
 * EVERY condition to hold; any unknown (missing pipeline, missing or stale
 * local results) denies. A misclassification here merges the wrong thing, so
 * this class stays standalone and exhaustively unit-tested.
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
     * @param string        $coreMajor target core version of the row ("11")
     * @param ?CachedResult $local     latest cached local check result for
     *                                 (module, MR, core), or null when never checked
     */
    public function classify(MergeRequest $mr, string $coreMajor, ?CachedResult $local): GateVerdict
    {
        $reasons = [];
        $blocked = false;

        if (!($this->patternForCore)($coreMajor)->matches($mr->authorUsername, $mr->authorId, $mr->sourceBranch)) {
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
            $reasons[] = 'ci-not-green:' . ($pipeline->rawStatus !== '' ? $pipeline->rawStatus : $pipeline->status->value);
        }

        // Local evidence must exist AND be for the MR's current head SHA;
        // unverifiable freshness (unknown head) counts as stale.
        if ($local === null) {
            $reasons[] = 'local-missing';
        } elseif ($mr->headSha === null || $local->sha !== $mr->headSha) {
            $reasons[] = 'local-stale';
        } else {
            foreach ($local->result->failures() as $failure) {
                $reasons[] = 'local-failed:' . $failure->type->value;
            }
        }

        if ($reasons === []) {
            return new GateVerdict(GateStatus::ReadyAuto);
        }

        return new GateVerdict($blocked ? GateStatus::Blocked : GateStatus::Review, $reasons);
    }
}

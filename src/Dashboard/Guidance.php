<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Gate\GateStatus;
use Upkeep\Patches\Contribution;

/**
 * What a row *is*, in a phrase, and what to run about it.
 *
 * The dashboard used to render the fast-lane gate's own vocabulary —
 * `REVIEW not-bot-author, ci-missing, local-missing` — which is precise,
 * machine-readable, and answers a question nobody asked. A maintainer looking
 * at a hundred rows needs to know which one to touch and what to type; the
 * reason tokens tell them neither, and `not-bot-author` appears on every
 * human-authored merge request, so the ordinary case reads as a defect.
 *
 * So the tokens move behind `-v` (anything scripted against them still has
 * them) and every row carries two human things instead: a status phrase, and
 * the literal command. **Every row has one** — there is no row this tool has
 * nothing to say about, and the two that briefly did (red CI, drafts) were the
 * ones a maintainer most wanted a way into.
 *
 * **The order the reasons are considered is the design.** A row usually has
 * several, and only one can be shown, so they are ranked by what actually
 * blocks progress: something upkeep cannot fix, then evidence the work is
 * wrong, then evidence that is missing, then nothing at all to do. The phrase
 * a maintainer sees is therefore the most actionable true thing about the row,
 * not the first one the gate happened to record.
 */
final readonly class Guidance
{
    private function __construct(
        public string $status,
        /**
         * Never null. Every row has something worth running, including the
         * ones that briefly did not: a red pipeline is when you most want the
         * branch locally, and a draft is often work somebody started and could
         * not finish, which is a thing to pick up rather than to wait on.
         */
        public string $command,
    ) {
    }

    public static function forRow(DashboardRow $row): self
    {
        if ($row->moduleFailure !== null) {
            return new self('unavailable', sprintf('upkeep dashboard --refresh=%s', $row->module));
        }

        return $row->contribution !== null
            ? self::forPatch($row, $row->contribution)
            : self::forMergeRequest($row);
    }

    /**
     * A merge request, ranked by what stands between it and being merged.
     */
    private static function forMergeRequest(DashboardRow $row): self
    {
        $verdict = $row->verdict;
        $reasons = $verdict === null ? [] : $verdict->reasons;
        $iid = $row->mergeRequest === null ? 0 : $row->mergeRequest->iid;

        if ($verdict?->status === GateStatus::ReadyAuto) {
            return new self('ready to merge', 'upkeep merge --fast-lane');
        }

        // Evidence the work is wrong, and it is *your* evidence — worth
        // telling the contributor about, which is what needs-work does.
        $failed = self::failedChecks($reasons);
        if ($failed !== []) {
            return new self(
                \count($failed) === 1 ? $failed[0] . ' failed' : \count($failed) . ' checks failed',
                sprintf('upkeep needs-work %s %d', $row->module, $iid),
            );
        }

        // Red CI and draft are *modifiers*: they change what the row is, not
        // what to do about it. Both spent a while suggesting nothing, and both
        // were wrong to. A red pipeline is when you most want the branch on
        // your own machine to reproduce the failure; a draft is frequently
        // something a contributor started and could not finish, which is a
        // thing to pick up rather than to wait on.
        //
        // GateStatus::Blocked is the same condition as ci-red: the gate sets it
        // from red CI and from nothing else, never inspecting mergeability,
        // whatever "cannot proceed (e.g. merge conflicts)" in the older docs
        // suggested.
        $ciRed = \in_array('ci-red', $reasons, true) || $verdict?->status === GateStatus::Blocked;
        $prefix = \in_array('draft', $reasons, true) ? 'draft, ' : '';

        // What to *do* is decided by the evidence you hold, independently of
        // either: nothing yet, or something about an older revision, means run
        // the checks; anything else means the change itself is what is left to
        // look at.
        if (self::needsChecking($reasons)) {
            $status = match (true) {
                $ciRed => 'CI failed',
                \in_array('local-stale', $reasons, true) => 'checks are stale',
                default => 'needs a check',
            };

            return new self($prefix . $status, self::checkCommand($row, $iid));
        }

        // Checked and green. Either the fast lane will never take it because
        // it is not a bot MR, or your checks disagree with drupal.org's — and
        // a disagreement is exactly a thing to go and look at.
        return new self(
            $prefix . ($ciRed ? 'CI failed, local green' : 'needs your review'),
            sprintf('upkeep review %s %d', $row->module, $iid),
        );
    }

    /**
     * A patch contribution. There is no gate here — nothing about a patch is
     * mergeable — so the ranking is simply whether it has been checked.
     */
    private static function forPatch(DashboardRow $row, Contribution $contribution): self
    {
        $nid = $contribution->issue->nid;
        $patches = $contribution->issue->patchCount();
        $local = $row->localCell();

        if ($patches === 0) {
            return new self('no patch attached', sprintf('upkeep issue %s %d', $row->module, $nid));
        }

        $status = $patches === 1 ? '1 patch' : $patches . ' patches';
        // An empty merge request beside a patch is the thing worth flagging:
        // the row looks covered and is not.
        if (str_contains($contribution->mergeRequestCell(), 'empty')) {
            $status .= ', empty MR';
        }

        if ($local === 'pass') {
            return new self($status . ', checked', sprintf('upkeep patch:apply %s %d', $row->module, $nid));
        }
        if ($local === 'fail') {
            return new self($status . ', failed', sprintf('upkeep issue %s %d', $row->module, $nid));
        }

        return new self(
            $local === 'stale' ? $status . ', stale check' : $status,
            self::patchCheckCommand($row, $nid),
        );
    }

    /**
     * The check commands carry `--version` only when the row is one of
     * several: a module tracking one core does not need telling which.
     */
    private static function checkCommand(DashboardRow $row, int $iid): string
    {
        return sprintf('upkeep check %s %d%s', $row->module, $iid, self::coreSuffix($row));
    }

    private static function patchCheckCommand(DashboardRow $row, int $nid): string
    {
        return sprintf('upkeep patch:check %s %d%s', $row->module, $nid, self::coreSuffix($row));
    }

    private static function coreSuffix(DashboardRow $row): string
    {
        return $row->core === '-' ? '' : ' --version=' . $row->core;
    }

    /**
     * The named checks that failed, from `local-failed:<check>` reasons.
     *
     * @param list<string> $reasons
     * @return list<string>
     */
    private static function failedChecks(array $reasons): array
    {
        $failed = [];
        foreach ($reasons as $reason) {
            if (str_starts_with($reason, 'local-failed:')) {
                $failed[] = substr($reason, \strlen('local-failed:'));
            }
        }

        return $failed;
    }

    /**
     * @param list<string> $reasons
     */
    private static function needsChecking(array $reasons): bool
    {
        return \in_array('local-missing', $reasons, true) || \in_array('local-stale', $reasons, true);
    }
}

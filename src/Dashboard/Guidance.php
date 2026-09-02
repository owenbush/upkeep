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
 * the literal command.
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
        public ?string $command,
        /** Why there is no command — shown in its place, in parentheses. */
        public ?string $note,
    ) {
    }

    public static function forRow(DashboardRow $row): self
    {
        if ($row->moduleFailure !== null) {
            return new self(
                'unavailable',
                sprintf('upkeep dashboard --refresh=%s', $row->module),
                null,
            );
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
            return new self('ready to merge', 'upkeep merge --fast-lane', null);
        }

        // Evidence the work is wrong, and it is *your* evidence — worth
        // telling the contributor about, which is what needs-work does.
        $failed = self::failedChecks($reasons);
        if ($failed !== []) {
            return new self(
                \count($failed) === 1 ? $failed[0] . ' failed' : \count($failed) . ' checks failed',
                sprintf('upkeep needs-work %s %d', $row->module, $iid),
                null,
            );
        }

        // Evidence the work is wrong, but it is upstream's — a maintainer
        // reporting their own CI back to them adds nothing.
        //
        // This is also what GateStatus::Blocked means. The gate sets Blocked
        // from red CI and from nothing else — it never inspects mergeability,
        // whatever "cannot proceed (e.g. merge conflicts)" in the older docs
        // suggested — so there is one condition here, not two, and calling it
        // "conflicts" would have told maintainers to ask for a rebase that
        // nothing had asked for.
        if (\in_array('ci-red', $reasons, true) || $verdict?->status === GateStatus::Blocked) {
            return new self('CI failed', null, 'the contributor\'s move');
        }

        // Explicitly not finished, so checking it is premature.
        if (\in_array('draft', $reasons, true)) {
            return new self('draft', null, 'not ready for review yet');
        }

        // The common case, and the one the old output buried under three
        // tokens: nobody has run the checks.
        if (self::needsChecking($reasons)) {
            return new self(
                \in_array('local-stale', $reasons, true) ? 'checks are stale' : 'needs a check',
                self::checkCommand($row, $iid),
                null,
            );
        }

        // Checked, green, and not a bot compat MR — so the fast lane will
        // never take it and a human has to look at the change itself.
        return new self('needs your review', sprintf('upkeep review %s %d', $row->module, $iid), null);
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
            return new self('no patch attached', sprintf('upkeep issue %s %d', $row->module, $nid), null);
        }

        $status = $patches === 1 ? '1 patch' : $patches . ' patches';
        // An empty merge request beside a patch is the thing worth flagging:
        // the row looks covered and is not.
        if (str_contains($contribution->mergeRequestCell(), 'empty')) {
            $status .= ', empty MR';
        }

        if ($local === 'pass') {
            return new self(
                $status . ', checked',
                sprintf('upkeep patch:apply %s %d', $row->module, $nid),
                null,
            );
        }
        if ($local === 'fail') {
            return new self($status . ', failed', sprintf('upkeep issue %s %d', $row->module, $nid), null);
        }

        return new self(
            $local === 'stale' ? $status . ', stale check' : $status,
            self::patchCheckCommand($row, $nid),
            null,
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

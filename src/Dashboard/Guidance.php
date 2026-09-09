<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Gate\GateStatus;

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

        // An empty merge request is not work to check. Applying it changes
        // nothing, so the checks would run against the branch as it already
        // stands and the verdict would be reported as though it were about the
        // contribution. Reported from a real dashboard, where a row showed
        // "!1 empty" and "1" patch side by side and then said to check the
        // merge request.
        //
        // `=== false` and never a truthiness test: carriesChanges() is
        // deliberately nullable, and null means the list endpoint did not
        // carry diff_refs — "not known", which no caller may read as empty.
        if ($row->mergeRequest?->carriesChanges() === false) {
            return self::forEmptyMergeRequest($row);
        }

        return $row->mergeRequest !== null
            ? self::forMergeRequest($row)
            : self::forDormantIssue($row);
    }

    /**
     * A merge request that carries nothing.
     *
     * With a patch beside it this is the shape the whole patch surface exists
     * for — the row looks covered and is not — so the patch is the work and
     * forDormantIssue already says all of that. With no patch there is simply
     * nothing to check yet, and the honest move is to go and look at the
     * issue rather than to name a command that would do nothing.
     */
    private static function forEmptyMergeRequest(DashboardRow $row): self
    {
        if ($row->patchCount > 0) {
            return self::forDormantIssue($row);
        }

        return new self(
            'empty MR, nothing to check',
            sprintf('upkeep issue %s %d', $row->module, $row->requireMergeRequest()->iid),
        );
    }

    /**
     * A merge request, ranked by what stands between it and being merged.
     */
    private static function forMergeRequest(DashboardRow $row): self
    {
        $verdict = $row->verdict;
        $reasons = $verdict === null ? [] : $verdict->reasons;
        $iid = $row->requireMergeRequest()->iid;

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

        // A landing outranks everything else this row could say. An open
        // issue whose work is already merged is the one thing a maintainer
        // cannot read off the row at all, and it is exactly what a promoted-
        // and-merged patch leaves behind: the bot's draft still sitting there,
        // looking like the only contribution on the issue.
        $landing = self::landing($row, self::checkCommand($row, $iid));
        if ($landing !== null) {
            return $landing;
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
     * An issue with nothing of its own open: patches waiting, work already
     * landed, or both.
     *
     * There is no gate here — nothing about a patch is mergeable — so the
     * ranking is what has already happened, then whether the patch has been
     * checked.
     */
    private static function forDormantIssue(DashboardRow $row): self
    {
        $nid = $row->issueNid ?? 0;
        $next = self::patchCheckCommand($row, $nid);

        $landing = self::landing($row, $next);
        if ($landing !== null) {
            return $landing;
        }

        $status = $row->patchCount === 1 ? '1 patch' : $row->patchCount . ' patches';

        // An empty merge request beside a patch is the thing worth flagging:
        // the row looks covered and is not.
        if (str_contains($row->mergeRequestCell(), 'empty')) {
            $status .= ', empty MR';
        }

        // Green on every applicable core, and stricter than it reads: a patch
        // checked on 11 and never checked on 10 is not "checked".
        if ($row->local->allGreen()) {
            return new self(
                $status . ', checked',
                sprintf('upkeep patch:apply %s %d', $row->module, $nid),
            );
        }

        // A failing patch is work to pick up rather than a verdict to deliver:
        // `start` opens a branch on the issue, which is what a maintainer does
        // next with a patch that does not hold up.
        if ($row->local->anyFailed()) {
            return new self($status . ', failed', sprintf('upkeep start %s %d', $row->module, $nid));
        }

        return new self($row->local->anyStale() ? $status . ', stale check' : $status, $next);
    }

    /**
     * The landing phrase, shared by both row shapes, or null when nothing on
     * the issue has merged.
     *
     * @param string $ifNewerWork what to run when the landing is not the last
     *                            word — a bot that posts again after its work
     *                            merged has raised new work to check
     */
    private static function landing(DashboardRow $row, string $ifNewerWork): ?self
    {
        if ($row->landed === null) {
            return null;
        }

        $merged = substr((string) $row->landed->mergedAt, 0, 10);

        if ($row->newerWorkSinceLanding) {
            return new self(sprintf('merged %s, newer work since', $merged), $ifNewerWork);
        }

        return new self(
            sprintf('merged %s', $merged),
            sprintf('upkeep issue %s %d', $row->module, $row->landed->iid),
        );
    }

    /**
     * The check commands carry `--version` only when a core needs attention:
     * a row whose evidence is green everywhere has nothing to re-run, and the
     * core named is the one the LOCAL cell named, so the table and the command
     * cannot disagree about which core is the problem.
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
        $core = $row->local->attentionCore();

        return $core === null ? '' : ' --version=' . $core;
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

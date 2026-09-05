<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * What updating a base branch decides and what it says about it.
 *
 * Pure, and separated from the git calls for the usual reason: the wrong
 * answer here is a green check on code that was never tested against what CI
 * will test it against, which is the failure mode this whole class exists to
 * close.
 *
 * The rule it encodes: **cut from what origin has, and say how far that was
 * from what you had.** A maintainer who is told "2.0.x was 47 commits behind"
 * knows why a check that passed yesterday fails today; one who is told nothing
 * goes looking in the patch.
 */
final readonly class BaseBranchUpdate
{
    /**
     * Where a disposable or work branch is cut from.
     *
     * `FETCH_HEAD` rather than `origin/<base>`: `git fetch origin <base>`
     * always writes it, while a remote-tracking ref is a property of how the
     * clone was configured and is not there to be relied on.
     */
    public static function cutPoint(string $baseBranch, BaseRefresh $refresh): string
    {
        return $refresh === BaseRefresh::Update ? 'FETCH_HEAD' : $baseBranch;
    }

    /**
     * What to report once the base has been fetched.
     *
     * Says nothing when the base was already current. A line per run saying
     * "up to date" is a line people stop reading, and the whole value of this
     * message is that it appears exactly when something moved.
     */
    public static function describe(string $baseBranch, int $behind): ?string
    {
        if ($behind < 1) {
            return null;
        }

        return sprintf(
            '%s was %d commit%s behind origin — updated. CI tests your work merged into this, '
            . 'so a check against the old tip could have disagreed with it.',
            $baseBranch,
            $behind,
            $behind === 1 ? '' : 's',
        );
    }

    /**
     * The base moved in a way a fast-forward cannot follow: somebody has local
     * commits on it.
     *
     * Never resolved automatically. Cutting from origin is still right — it is
     * what CI will merge into — but the local commits are now not in the tree
     * being checked, and that is a thing a maintainer has to be told rather
     * than have decided for them.
     */
    public static function diverged(string $baseBranch): string
    {
        return sprintf(
            'Local %s has commits origin does not, so it was left alone. Cutting from origin/%s instead, '
            . 'because that is what CI merges into — your local commits are not in what is about to be checked.',
            $baseBranch,
            $baseBranch,
        );
    }

    /** What a Skip says, so a stale verdict is never a silent one. */
    public static function skipped(string $baseBranch): string
    {
        return sprintf(
            'Not updating %s (--no-update). This checks against the base as it stands here, '
            . 'which may not be what CI merges into.',
            $baseBranch,
        );
    }

    /**
     * Asked to update and could not.
     *
     * A refusal rather than a warning, and deliberately: everything else here
     * degrades and says so, but the degraded result in this one case is a
     * verdict that looks exactly like a good one and gets cached as evidence
     * the fast-lane gate reads. Exit rather than record it — and name the flag
     * that makes it a choice.
     */
    public static function unreachable(string $baseBranch, string $output): AdapterException
    {
        return new AdapterException(sprintf(
            "Could not fetch %s from origin, so upkeep cannot tell what CI would test this against:\n%s\n"
            . 'Re-run with --no-update to check against the base as it stands here instead.',
            $baseBranch,
            trim($output) === '' ? '  (no output from git)' : '  ' . trim($output),
        ));
    }
}

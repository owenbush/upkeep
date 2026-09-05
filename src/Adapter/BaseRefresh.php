<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Whether to bring the base branch up to date before cutting from it.
 *
 * `Update` is the default everywhere, and the reason is a bug that cost a
 * maintainer hours. A module working copy is cloned once and then never
 * fetched again on any path that cuts a branch, so its `2.0.x` stays frozen at
 * whatever it was on the day of the clone. A patch applied on top of that is
 * checked against code that is months old.
 *
 * That alone would only make the verdict *old*. What made it wrong is that
 * drupal.org's CI does not check your branch: it checks
 * `refs/merge-requests/<iid>/merge`, which is your branch merged into the
 * **current** tip of the target. A base sixteen months stale and a target that
 * had since been rewritten for Drupal 12 merged cleanly — git had no conflict
 * to report, because the patch only added a function — and the merged file was
 * missing the `use` import that the rewrite had removed and the patch still
 * relied on. Local: green. CI: one line, one undefined class, no explanation.
 *
 * So the base is refreshed before it is used as a cut point, and `Skip` exists
 * for the cases where that is not what is wanted — working offline, or
 * reproducing a verdict against the tree as it was.
 */
enum BaseRefresh
{
    /** Fetch the base from origin and cut from what came back. */
    case Update;

    /** Cut from the working copy's base exactly as it stands. */
    case Skip;

    /** Built from the `--no-update` flag, which is the only way to get Skip. */
    public static function fromNoUpdateFlag(bool $noUpdate): self
    {
        return $noUpdate ? self::Skip : self::Update;
    }
}

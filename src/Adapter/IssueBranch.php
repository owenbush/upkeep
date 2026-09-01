<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The branch a maintainer's own work on an issue lives on.
 *
 * Named to drupal.org's issue-fork convention — `<nid>-<slug>` — which is not
 * decoration: it is the shape `Drupal\IssueReference` already parses, so a
 * branch started here is one every other part of this tool (and drupal.org
 * itself) recognises as belonging to that issue. Push it and the merge request
 * that follows is linked to the issue with nothing further to configure.
 *
 * **A work branch is not a managed branch.** `ManagedBranch` names the
 * disposable ones — `mr-<iid>`, `patch-<nid>` — which upkeep resets from the
 * base on every apply, because re-testing somebody else's contribution must
 * test that contribution alone. A work branch holds the only copy of something
 * a human wrote. Nothing in this tool may reset, force, or discard it, and the
 * two kinds are deliberately unable to be confused: no managed prefix can ever
 * begin with a digit, and a work branch always does.
 */
final readonly class IssueBranch
{
    /** Long enough to be recognisable in `git branch`, short enough to type. */
    private const MAX_SLUG_LENGTH = 40;

    private function __construct(
        public int $issueNid,
        public string $name,
    ) {
    }

    /**
     * The branch for an issue, named from its title.
     *
     * Takes the id and title rather than an Issue so this stays clear of the
     * drupal.org models: branch naming is adapter territory, and Workflow
     * already depends on Adapter in the other direction.
     *
     * The slug is cosmetic — everything that matters keys on the leading node
     * id — so a title that reduces to nothing still yields a usable branch
     * rather than a failure.
     */
    public static function forIssue(int $issueNid, string $title): self
    {
        $slug = self::slug($title);

        return new self($issueNid, $slug === '' ? (string) $issueNid : $issueNid . '-' . $slug);
    }

    /** An explicitly named branch, still anchored to its issue. */
    public static function named(int $issueNid, string $name): self
    {
        return new self($issueNid, $name);
    }

    /**
     * Whether a branch name is a work branch for this issue — the test that
     * lets `start` resume yesterday's work instead of starting beside it.
     */
    public function matches(string $branch): bool
    {
        return $branch === $this->name
            || preg_match('/^' . $this->issueNid . '(-|$)/', $branch) === 1;
    }

    /**
     * Whether any branch name looks like issue work, whoever created it.
     *
     * Used to refuse the disposable-branch machinery a target it must not
     * touch: `git checkout -B` on one of these would discard commits nobody
     * else has a copy of.
     */
    public static function isWorkBranch(string $branch): bool
    {
        return preg_match('/^\d{4,}(-|$)/', $branch) === 1;
    }

    /**
     * A title reduced to a branch-safe slug: lowercase, words joined by
     * hyphens, truncated on a word boundary so the name stays readable.
     */
    public static function slug(string $title): string
    {
        $slug = strtolower($title);
        $slug = preg_replace('/[^a-z0-9]+/', '-', $slug) ?? '';
        $slug = trim($slug, '-');

        if (\strlen($slug) <= self::MAX_SLUG_LENGTH) {
            return $slug;
        }

        $cut = substr($slug, 0, self::MAX_SLUG_LENGTH);
        $lastHyphen = strrpos($cut, '-');

        return trim($lastHyphen === false ? $cut : substr($cut, 0, $lastHyphen), '-');
    }
}

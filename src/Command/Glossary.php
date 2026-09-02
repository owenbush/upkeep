<?php

declare(strict_types=1);

namespace Upkeep\Command;

/**
 * What every term upkeep prints actually means.
 *
 * The tool emits six overlapping vocabularies — gate statuses, gate reason
 * tokens, CI states, local-check states, contribution kinds, drupal.org issue
 * statuses — and some words appear in more than one with different meanings
 * ("review" is both a gate verdict and an issue status). A maintainer reading
 * `patch↑` had no way to find out what it meant: it was in the source and
 * nowhere else.
 *
 * So the definitions live in one place, as data, and `upkeep explain` prints
 * them. Kept next to nothing — deliberately not next to the code that emits
 * each term — because the point is that a reader can find them all at once.
 */
final readonly class Glossary
{
    /**
     * Term => [what it means, where it appears].
     *
     * @return array<string, array{string, string}>
     */
    public static function terms(): array
    {
        return [
            // ------------------------------------------------ what to do next
            'ready to merge' => [
                'A Project Update Bot compatibility MR with green CI and green local checks. The only thing the '
                    . 'fast lane will offer to merge.',
                'dashboard STATUS',
            ],
            'needs a check' => [
                'Nobody has run the checks against this yet. Running them is what turns it into evidence.',
                'dashboard STATUS',
            ],
            'checks are stale' => [
                'The checks were run, but against an older revision — the branch has moved, or the patch was '
                    . 're-rolled since. The verdict no longer describes what is there now.',
                'dashboard STATUS',
            ],
            'needs your review' => [
                'Checked, green, and not a bot MR — so the fast lane will never take it and a human has to look '
                    . 'at the change itself.',
                'dashboard STATUS',
            ],
            'CI failed' => [
                "drupal.org's own pipeline is red. The row still points at `upkeep check`, because a red pipeline "
                    . 'is exactly when you want the branch on your own machine to reproduce the failure.',
                'dashboard STATUS',
            ],
            'CI failed, local green' => [
                "drupal.org's pipeline is red but your own checks pass against the current head. The two disagree, "
                    . 'which is itself the thing to go and look at — often a difference in core version or '
                    . 'toolchain rather than in the change.',
                'dashboard STATUS',
            ],
            'draft' => [
                'Marked as a draft on GitLab. It prefixes the rest of the status rather than replacing it, and '
                    . 'the row is still checkable: unfinished is frequently abandoned, and work somebody could '
                    . 'not carry on is a thing to pick up rather than to wait on.',
                'dashboard STATUS',
            ],
            'empty MR' => [
                'A merge request whose branch holds no commits the target does not already have. It exists, it '
                    . 'can be linked from an issue, and it covers nothing — the Project Update Bot leaves these '
                    . 'on many projects. Any patch beside it is the only work there is.',
                'dashboard STATUS, patches MR column',
            ],
            'unclaimed' => [
                'An open issue with no merge request and no patch. Nobody has started it — which makes it the '
                    . 'most actionable row on an issue list, not the least.',
                'issues CONTRIBUTION',
            ],

            // ------------------------------------------------------ the cells
            'patch↑' => [
                'The issue carries a patch newer than the merge request\'s last update. Someone posted a patch '
                    . 'after the branch was last touched, so the branch may be behind the issue.',
                'dashboard ISSUE',
            ],
            'CI' => [
                "drupal.org's pipeline for the branch: pass, fail, a raw pipeline state, or an en dash when no "
                    . 'pipeline has run. Patches never have one — drupal.org runs CI on branches, not on '
                    . 'attachments.',
                'dashboard column',
            ],
            'LOCAL' => [
                'Your own cached check result: pass, fail, stale, or an en dash for never checked. It is what '
                    . '`upkeep check` and `upkeep patch:check` write.',
                'dashboard column',
            ],
            'stale' => [
                'Evidence recorded against a revision that is no longer current — an older MR head, or a patch '
                    . 'that has since been re-rolled. A stale pass is not a pass.',
                'dashboard LOCAL',
            ],
            'NEXT' => [
                'The command to run for that row. Every row has one — red CI and draft describe the row without '
                    . 'changing what to do about it, since both are exactly when you want the branch locally.',
                'dashboard column',
            ],
            'PATCH ISSUES' => [
                'How many *issues* on that module carry patch files. A row\'s own patch count is a number of '
                    . 'files, which is why this column does not say "patches".',
                'dashboard overview column',
            ],
            'CI FAILED' => [
                "How many rows drupal.org's pipeline has failed. It is the gate's BLOCKED verdict under its real "
                    . 'name — that verdict is set by red CI and by nothing else.',
                'dashboard overview column',
            ],
            'UNCHECKED' => [
                'Rows with no local verdict, plus rows whose verdict is stale. The actionable number on the '
                    . 'overview.',
                'dashboard overview column',
            ],

            // ----------------------------------------------- the gate's tokens
            'READY-AUTO' => [
                'The gate verdict behind "ready to merge". Shown under -v.',
                'dashboard STATUS (-v)',
            ],
            'REVIEW' => [
                'The gate verdict meaning a human has to look. Shown under -v, with the reasons that denied '
                    . 'fast-lane eligibility.',
                'dashboard STATUS (-v)',
            ],
            'BLOCKED' => [
                "The gate verdict set by red CI, and by nothing else — the gate does not inspect mergeability, so "
                    . 'this does not mean merge conflicts. Shown under -v, and as "CI failed" without it.',
                'dashboard STATUS (-v)',
            ],
            'ci-red' => [
                "A gate reason: drupal.org's pipeline for the head commit failed. It is also the only thing that "
                    . 'makes the gate verdict BLOCKED.',
                'dashboard STATUS (-v)',
            ],
            'not-bot-author' => [
                'A gate reason: this is not a Project Update Bot compatibility MR, so it is not fast-lane '
                    . 'eligible. It appears on every human-authored MR and is not a problem.',
                'dashboard STATUS (-v)',
            ],
            'local-missing' => [
                'A gate reason: no cached check result for this (module, MR, core).',
                'dashboard STATUS (-v)',
            ],
            'local-stale' => [
                "A gate reason: the cached result is for a different revision than the MR's current head.",
                'dashboard STATUS (-v)',
            ],
            'ci-missing' => [
                'A gate reason: GitLab reports no pipeline for the head commit.',
                'dashboard STATUS (-v)',
            ],

            // --------------------------------------------- drupal.org statuses
            'active' => [
                'A drupal.org issue status: open, and waiting on nobody in particular. Where new work begins.',
                'issues STATUS',
            ],
            'review' => [
                'A drupal.org issue status (Needs review): someone has submitted work and is waiting on a '
                    . 'maintainer. Not the same as the gate\'s REVIEW verdict.',
                'issues STATUS',
            ],
            'RTBC' => [
                'A drupal.org issue status (Reviewed & tested by the community): someone else has reviewed it '
                    . 'and believes it is ready. Waiting on a maintainer.',
                'issues STATUS',
            ],
            'needs work' => [
                'A drupal.org issue status: reviewed and found wanting. Waiting on the contributor.',
                'issues STATUS',
            ],
        ];
    }

    /**
     * The terms whose name or meaning matches a query, so a half-remembered
     * word still finds its definition.
     *
     * @return array<string, array{string, string}>
     */
    public static function search(string $query): array
    {
        $needle = mb_strtolower(trim($query));
        if ($needle === '') {
            return self::terms();
        }

        $matched = [];
        foreach (self::terms() as $term => $entry) {
            if (str_contains(mb_strtolower($term), $needle) || str_contains(mb_strtolower($entry[0]), $needle)) {
                $matched[$term] = $entry;
            }
        }

        return $matched;
    }
}

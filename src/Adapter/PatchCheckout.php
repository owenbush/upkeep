<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Pure logic behind EngineAdapterInterface::applyPatch(): how a downloaded
 * patch file maps onto git operations in the module working copy.
 *
 * A patch has no ref to fetch, so unlike a merge request it cannot simply be
 * checked out. It is applied onto a fresh branch off the base and committed,
 * for two reasons that both matter to what the checks then report:
 *
 *   - The checks must run against a *clean* tree. Left uncommitted, the patch
 *     would show up as local modification to every subsequent inspection, and
 *     applyMr's dirty-working-copy guard would refuse to run afterwards.
 *   - The branch is reset from the base on every apply, so re-running with a
 *     newer re-roll tests that re-roll alone rather than the sum of every
 *     patch ever applied to the issue.
 *
 * Patch files on drupal.org are `git diff` output taken at the repository
 * root, so they apply at -p1. A patch that will not apply is a result, not a
 * crash: the caller is told which patch failed against which base, because
 * "this needs a re-roll" is exactly the review outcome worth reporting.
 */
final readonly class PatchCheckout
{
    /**
     * Arguments for the apply attempt. `--index` stages what it applies, so
     * the commit that follows needs no separate `git add`, and a partial
     * application cannot leave staged and unstaged halves disagreeing.
     *
     * @return list<string>
     */
    public static function applyArgs(string $localPath): array
    {
        return ['apply', '--index', '-p1', $localPath];
    }

    /**
     * A three-way apply, retried when the straight one fails. Git can often
     * place a hunk that context-matching alone rejects, provided the blobs the
     * patch was generated against are in the repository — which for a
     * drupal.org patch cut from the same project they generally are.
     *
     * @return list<string>
     */
    public static function threeWayApplyArgs(string $localPath): array
    {
        return ['apply', '--index', '-p1', '--3way', $localPath];
    }

    /**
     * A reduced-context apply, tried when both exact attempts fail.
     *
     * The common cause is not a stale patch but a trailing-whitespace drift:
     * drupal.org patches are generated against an export whose files may carry
     * a trailing blank line the repository does not, so a hunk header promises
     * seven context lines for a six-line file and git refuses the whole thing.
     * `-C1` requires one line of context instead of three, which resolves that
     * without loosening what has to *match* — the changed lines are still
     * compared exactly.
     *
     * Reported to the operator when it is what succeeded, because a hunk
     * placed on one line of context is a weaker guarantee than one placed on
     * three, and a maintainer reviewing the result should know which they got.
     *
     * @return list<string>
     */
    public static function reducedContextApplyArgs(string $localPath): array
    {
        return ['apply', '--index', '-p1', '-C1', $localPath];
    }

    /**
     * A dry run that reports per file rather than stopping at the first
     * failure — the difference between "this patch is stale" and "one of its
     * nine files is stale".
     *
     * @return list<string>
     */
    public static function checkArgs(string $localPath): array
    {
        return ['apply', '--check', '-v', '-p1', $localPath];
    }

    /**
     * What the patch wants to touch, whether or not it applies.
     *
     * @return list<string>
     */
    public static function statArgs(string $localPath): array
    {
        return ['apply', '--stat', '-p1', $localPath];
    }

    /**
     * The commit message recording what was applied. It names the file rather
     * than just the issue, because an issue routinely carries several
     * re-rolls and the working copy should say which one it holds.
     */
    public static function commitMessage(PatchApplication $patch): string
    {
        return sprintf('Apply %s (issue #%d) [upkeep]', $patch->name, $patch->issueNid);
    }

    /**
     * The failure a patch that will not apply produces.
     *
     * Phrased as a review finding rather than a tool error: a patch that no
     * longer applies to its target branch has told the maintainer something
     * true and useful about the contribution. And it says *what* is stale —
     * "one of these nine files" is actionable, "the patch failed" is not.
     *
     * @param string $statOutput  `git apply --stat` output
     * @param string $checkOutput `git apply --check -v` output
     */
    public static function unappliableException(
        PatchApplication $patch,
        string $baseBranch,
        string $statOutput,
        string $checkOutput,
        string $moduleName,
    ): AdapterException {
        $failed = self::failedFiles($checkOutput);
        $touched = self::touchedFiles($statOutput);

        $lines = [sprintf(
            'Patch "%s" (issue #%d) does not apply to "%s", even with a three-way merge and reduced context.',
            $patch->name,
            $patch->issueNid,
            $baseBranch,
        )];

        if ($touched > 0) {
            $lines[] = '';
            $lines[] = $failed === []
                ? sprintf('It changes %d file(s).', $touched)
                : sprintf(
                    'It changes %d file(s); %d apply, %d do not:',
                    $touched,
                    max(0, $touched - \count($failed)),
                    \count($failed),
                );
            foreach ($failed as $file) {
                $lines[] = '  ' . $file;
            }
        }

        $context = self::searchedContext($checkOutput);
        if ($context !== null) {
            $lines[] = '';
            $lines[] = 'git looked for this and did not find it:';
            foreach (explode("\n", $context) as $line) {
                $lines[] = '  ' . $line;
            }
        }

        $lines[] = '';
        if (self::cutFromReleaseTarball($checkOutput)) {
            // Not staleness, and saying "that file has moved on" sends
            // somebody looking for changes that were never made.
            $lines[] = sprintf(
                'This patch was cut against a release tarball, not a git checkout: the context it is looking '
                . 'for contains lines the drupal.org packaging script adds to .info.yml files at release time '
                . "and the repository does not have. Re-rolling against \"%s\" will not help until it is "
                . 'regenerated from a checkout.',
                $baseBranch,
            );
        } else {
            $lines[] = $failed === []
                ? 'Re-roll it against "' . $baseBranch . '", or check the issue for a newer patch.'
                : sprintf(
                    'That file has moved on since the patch was cut. Re-roll against "%s", or check the issue '
                    . 'for a newer patch.',
                    $baseBranch,
                );
        }

        $lines[] = '';
        $lines[] = 'To start the re-roll from what still fits:';
        $lines[] = sprintf('  upkeep patch:promote %s %d --partial', $moduleName, $patch->issueNid);

        return new AdapterException(implode("\n", $lines));
    }

    /**
     * A `--reject` apply: takes every hunk that fits and writes the rest to
     * `<file>.rej` beside the file it could not change.
     *
     * No `--index`, unlike every other rung. The point of this one is to hand
     * back a working copy somebody is about to edit, so staging half of it
     * would put them in a state where `git diff` hides the very changes they
     * came to look at. It exits non-zero even when it applied most of the
     * patch, so the caller reads the output rather than the status.
     *
     * @return list<string>
     */
    public static function rejectApplyArgs(string $localPath): array
    {
        return ['apply', '-p1', '--reject', $localPath];
    }

    /**
     * Whether the patch was cut against a drupal.org release tarball rather
     * than a git checkout.
     *
     * The packaging script appends `version`, `project` and `datestamp` to
     * every `.info.yml` when it builds a release. Those lines exist in the
     * tarball and in no commit, so a patch generated from an unpacked release
     * carries them as *context* — and no amount of re-rolling against the
     * branch will make that context match, because the branch never had it.
     *
     * Worth telling apart, because the two failures give opposite advice. A
     * stale patch wants re-rolling against the branch. This one is not
     * necessarily stale at all; it wants regenerating from a checkout, and
     * being told to re-roll against "1.0.x" sends you to look for changes that
     * are not there.
     */
    public static function cutFromReleaseTarball(string $checkOutput): bool
    {
        return str_contains($checkOutput, 'Information added by Drupal.org packaging script')
            || (bool) preg_match('/^\s*datestamp:\s*\d+/m', $checkOutput);
    }

    /**
     * Files whose hunks were rejected, read from `git apply --reject` output.
     *
     * @return list<string>
     */
    public static function rejectedFiles(string $rejectOutput): array
    {
        preg_match_all('/^Applying patch (.+?) with \d+ reject/m', $rejectOutput, $matches);

        return array_values(array_unique($matches[1]));
    }

    /**
     * Files the reject apply changed cleanly.
     *
     * @return list<string>
     */
    public static function cleanlyApplied(string $rejectOutput): array
    {
        preg_match_all('/^Applied patch (.+?) cleanly\./m', $rejectOutput, $matches);

        return array_values(array_unique($matches[1]));
    }

    /**
     * The files git named as failing, deduplicated and in order.
     *
     * @return list<string>
     */
    public static function failedFiles(string $checkOutput): array
    {
        preg_match_all('/^error: patch failed: (.+?):\d+$/m', $checkOutput, $m);

        return array_values(array_unique($m[1]));
    }

    /**
     * How many files the patch touches, from `git apply --stat`'s summary line.
     */
    public static function touchedFiles(string $statOutput): int
    {
        return preg_match('/^\s*(\d+) files? changed/m', $statOutput, $m) === 1 ? (int) $m[1] : 0;
    }

    /**
     * The block git printed after "while searching for:" — the actual text it
     * could not find, which is what tells a maintainer whether the file moved
     * on or the patch was cut against something else entirely.
     */
    public static function searchedContext(string $checkOutput): ?string
    {
        if (preg_match('/error: while searching for:\n(.*?)\nerror: patch failed:/s', $checkOutput, $m) !== 1) {
            return null;
        }

        $context = rtrim($m[1]);

        return $context === '' ? null : $context;
    }
}

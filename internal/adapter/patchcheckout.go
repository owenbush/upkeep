package adapter

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// PatchApplication is a patch file as the adapter consumes it: which issue it
// belongs to, what it is called, and where it has already been downloaded to
// on this machine.
//
// The adapter never fetches. By the time a patch reaches this boundary it is a
// local file that something else has already retrieved and verified — the same
// division as everywhere else here, where the adapter owns engine mechanics
// and nothing more.
type PatchApplication struct {
	IssueNid  int
	Name      string
	LocalPath string
	// BaseBranch is the branch this patch was generated against, when it is
	// known.
	//
	// An issue is filed against a version and its patches are cut from that
	// branch. Without this the adapter used whatever the working copy sat on —
	// the clone's default — so a 2.0.x patch was applied to 1.0.x and reported
	// as needing a re-roll. Empty means "nobody could tell", and the adapter
	// falls back to resolving from the working copy.
	BaseBranch string
}

// BranchName is the local branch this patch is applied on.
func (p PatchApplication) BranchName() string { return ManagedBranchForPatch(p.IssueNid) }

// Pure logic behind applying a patch: how a downloaded patch file maps onto
// git operations in the module working copy.
//
// A patch has no ref to fetch, so unlike a merge request it cannot simply be
// checked out. It is applied onto a fresh branch off the base and committed,
// for two reasons that both matter to what the checks then report:
//
//   - The checks must run against a *clean* tree. Left uncommitted, the patch
//     would show up as local modification to every subsequent inspection, and
//     the merge-request path's dirty-working-copy guard would refuse to run
//     afterwards.
//   - The branch is reset from the base on every apply, so re-running with a
//     newer re-roll tests that re-roll alone rather than the sum of every
//     patch ever applied to the issue.
//
// Patch files on drupal.org are `git diff` output taken at the repository
// root, so they apply at -p1. A patch that will not apply is a result, not a
// crash: the caller is told which patch failed against which base, because
// "this needs a re-roll" is exactly the review outcome worth reporting.

// ApplyArgs are the arguments for the apply attempt.
//
// --index stages what it applies, so the commit that follows needs no separate
// `git add`, and a partial application cannot leave staged and unstaged halves
// disagreeing.
func ApplyArgs(localPath string) []string {
	return []string{"apply", "--index", "-p1", localPath}
}

// ThreeWayApplyArgs is a three-way apply, retried when the straight one fails.
//
// Git can often place a hunk that context-matching alone rejects, provided the
// blobs the patch was generated against are in the repository — which for a
// drupal.org patch cut from the same project they generally are.
func ThreeWayApplyArgs(localPath string) []string {
	return []string{"apply", "--index", "-p1", "--3way", localPath}
}

// ReducedContextApplyArgs is a reduced-context apply, tried when both exact
// attempts fail.
//
// The common cause is not a stale patch but a trailing-whitespace drift:
// drupal.org patches are generated against an export whose files may carry a
// trailing blank line the repository does not, so a hunk header promises seven
// context lines for a six-line file and git refuses the whole thing. -C1
// requires one line of context instead of three, which resolves that without
// loosening what has to *match* — the changed lines are still compared
// exactly.
//
// Reported to the operator when it is what succeeded, because a hunk placed on
// one line of context is a weaker guarantee than one placed on three, and a
// maintainer reviewing the result should know which they got.
func ReducedContextApplyArgs(localPath string) []string {
	return []string{"apply", "--index", "-p1", "-C1", localPath}
}

// CheckArgs is a dry run that reports per file rather than stopping at the
// first failure — the difference between "this patch is stale" and "one of its
// nine files is stale".
func CheckArgs(localPath string) []string {
	return []string{"apply", "--check", "-v", "-p1", localPath}
}

// StatArgs is what the patch wants to touch, whether or not it applies.
func StatArgs(localPath string) []string {
	return []string{"apply", "--stat", "-p1", localPath}
}

// RejectApplyArgs is a --reject apply: it takes every hunk that fits and
// writes the rest to <file>.rej beside the file it could not change.
//
// No --index, unlike every other rung. The point of this one is to hand back a
// working copy somebody is about to edit, so staging half of it would put them
// in a state where `git diff` hides the very changes they came to look at. It
// exits non-zero even when it applied most of the patch, so the caller reads
// the output rather than the status.
func RejectApplyArgs(localPath string) []string {
	return []string{"apply", "-p1", "--reject", localPath}
}

// PatchCommitMessage records what was applied.
//
// It names the file rather than just the issue, because an issue routinely
// carries several re-rolls and the working copy should say which one it holds.
func PatchCommitMessage(patch PatchApplication) string {
	return fmt.Sprintf("Apply %s (issue #%d) [upkeep]", patch.Name, patch.IssueNid)
}

var (
	packagingScript = regexp.MustCompile(`(?m)^\s*datestamp:\s*\d+`)
	rejectedFile    = regexp.MustCompile(`(?m)^Applying patch (.+?) with \d+ reject`)
	cleanFile       = regexp.MustCompile(`(?m)^Applied patch (.+?) cleanly\.`)
	failedFile      = regexp.MustCompile(`(?m)^error: patch failed: (.+?):\d+$`)
	changedFiles    = regexp.MustCompile(`(?m)^\s*(\d+) files? changed`)
	searchedFor     = regexp.MustCompile(`(?s)error: while searching for:\n(.*?)\nerror: patch failed:`)
)

// CutFromReleaseTarball reports whether the patch was cut against a drupal.org
// release tarball rather than a git checkout.
//
// The packaging script appends version, project and datestamp to every
// .info.yml when it builds a release. Those lines exist in the tarball and in
// no commit, so a patch generated from an unpacked release carries them as
// *context* — and no amount of re-rolling against the branch will make that
// context match, because the branch never had it.
//
// Worth telling apart, because the two failures give opposite advice. A stale
// patch wants re-rolling against the branch. This one is not necessarily stale
// at all; it wants regenerating from a checkout, and being told to re-roll
// against "1.0.x" sends you to look for changes that are not there.
func CutFromReleaseTarball(checkOutput string) bool {
	return strings.Contains(checkOutput, "Information added by Drupal.org packaging script") ||
		packagingScript.MatchString(checkOutput)
}

// RejectedFiles are the files whose hunks were rejected, read from
// `git apply --reject` output.
func RejectedFiles(rejectOutput string) []string { return uniqueMatches(rejectedFile, rejectOutput) }

// CleanlyApplied are the files the reject apply changed cleanly.
func CleanlyApplied(rejectOutput string) []string { return uniqueMatches(cleanFile, rejectOutput) }

// FailedFiles are the files git named as failing, deduplicated and in order.
func FailedFiles(checkOutput string) []string { return uniqueMatches(failedFile, checkOutput) }

func uniqueMatches(pattern *regexp.Regexp, output string) []string {
	found := []string{}
	for _, match := range pattern.FindAllStringSubmatch(output, -1) {
		if !slices.Contains(found, match[1]) {
			found = append(found, match[1])
		}
	}

	return found
}

// TouchedFiles is how many files the patch touches, from `git apply --stat`'s
// summary line.
func TouchedFiles(statOutput string) int {
	match := changedFiles.FindStringSubmatch(statOutput)
	if match == nil {
		return 0
	}
	count, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}

	return count
}

// SearchedContext is the block git printed after "while searching for:" — the
// actual text it could not find, which is what tells a maintainer whether the
// file moved on or the patch was cut against something else entirely.
func SearchedContext(checkOutput string) string {
	match := searchedFor.FindStringSubmatch(checkOutput)
	if match == nil {
		return ""
	}

	return strings.TrimRight(match[1], " \t\n\r\v\f\x00")
}

// UnappliablePatchError is the failure a patch that will not apply produces.
//
// Phrased as a review finding rather than a tool error: a patch that no longer
// applies to its target branch has told the maintainer something true and
// useful about the contribution. And it says *what* is stale — "one of these
// nine files" is actionable, "the patch failed" is not.
func UnappliablePatchError(
	patch PatchApplication,
	baseBranch, statOutput, checkOutput, moduleName string,
) error {
	failed := FailedFiles(checkOutput)
	touched := TouchedFiles(statOutput)

	lines := []string{fmt.Sprintf(
		"Patch %q (issue #%d) does not apply to %q, even with a three-way merge and reduced context.",
		patch.Name, patch.IssueNid, baseBranch,
	)}

	if touched > 0 {
		lines = append(lines, "")
		if len(failed) == 0 {
			lines = append(lines, fmt.Sprintf("It changes %d file(s).", touched))
		} else {
			applied := touched - len(failed)
			if applied < 0 {
				applied = 0
			}
			lines = append(lines, fmt.Sprintf(
				"It changes %d file(s); %d apply, %d do not:", touched, applied, len(failed),
			))
		}
		for _, file := range failed {
			lines = append(lines, "  "+file)
		}
	}

	if context := SearchedContext(checkOutput); context != "" {
		lines = append(lines, "", "git looked for this and did not find it:")
		for _, line := range strings.Split(context, "\n") {
			lines = append(lines, "  "+line)
		}
	}

	lines = append(lines, "")
	switch {
	case CutFromReleaseTarball(checkOutput):
		// Not staleness, and saying "that file has moved on" sends somebody
		// looking for changes that were never made.
		lines = append(lines, fmt.Sprintf(
			"This patch was cut against a release tarball, not a git checkout: the context it is looking "+
				"for contains lines the drupal.org packaging script adds to .info.yml files at release time "+
				"and the repository does not have. Re-rolling against %q will not help until it is "+
				"regenerated from a checkout.",
			baseBranch,
		))
	case len(failed) == 0:
		lines = append(lines, fmt.Sprintf(
			"Re-roll it against %q, or check the issue for a newer patch.", baseBranch,
		))
	default:
		lines = append(lines, fmt.Sprintf(
			"That file has moved on since the patch was cut. Re-roll against %q, or check the issue "+
				"for a newer patch.",
			baseBranch,
		))
	}

	// promote, whichever command you were running. It is the only patch
	// command that lands on the issue work branch, which upkeep never resets —
	// patch-<nid> is rebuilt from the base on every apply, so rejects resolved
	// there would be destroyed by the next run.
	lines = append(lines,
		"",
		"To start the re-roll from what still fits — promote, because it is the only patch command",
		"whose branch survives the next apply:",
		fmt.Sprintf("  upkeep patch:promote %s %d --partial", moduleName, patch.IssueNid),
	)

	return fmt.Errorf("%s", strings.Join(lines, "\n"))
}

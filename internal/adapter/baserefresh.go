package adapter

import (
	"fmt"
	"strings"
)

// BaseRefresh is whether to bring the base branch up to date before cutting
// from it.
//
// Update is the default everywhere, and the reason is a bug that cost a
// maintainer hours. A module working copy is cloned once and then never
// fetched again on any path that cuts a branch, so its 2.0.x stays frozen at
// whatever it was on the day of the clone. A patch applied on top of that is
// checked against code that is months old.
//
// That alone would only make the verdict *old*. What made it wrong is that
// drupal.org's CI does not check your branch: it checks
// refs/merge-requests/<iid>/merge, which is your branch merged into the
// **current** tip of the target. A base sixteen months stale and a target that
// had since been rewritten for Drupal 12 merged cleanly — git had no conflict
// to report, because the patch only added a function — and the merged file was
// missing the `use` import that the rewrite had removed and the patch still
// relied on. Local: green. CI: one line, one undefined class, no explanation.
//
// So the base is refreshed before it is used as a cut point, and Skip exists
// for the cases where that is not what is wanted — working offline, or
// reproducing a verdict against the tree as it was.
type BaseRefresh int

const (
	// RefreshUpdate fetches the base from origin and cuts from what came back.
	RefreshUpdate BaseRefresh = iota
	// RefreshSkip cuts from the working copy's base exactly as it stands.
	RefreshSkip
)

// RefreshFromNoUpdateFlag is built from --no-update, which is the only way to
// get a skip.
func RefreshFromNoUpdateFlag(noUpdate bool) BaseRefresh {
	if noUpdate {
		return RefreshSkip
	}

	return RefreshUpdate
}

// What updating a base branch decides and what it says about it.
//
// Pure, and separated from the git calls for the usual reason: the wrong
// answer here is a green check on code that was never tested against what CI
// will test it against, which is the failure this whole area exists to close.
//
// The rule it encodes: **cut from what origin has, and say how far that was
// from what you had.** A maintainer told "2.0.x was 47 commits behind" knows
// why a check that passed yesterday fails today; one told nothing goes looking
// in the patch.

// CutPoint is where a disposable or work branch is cut from.
//
// FETCH_HEAD rather than origin/<base>: `git fetch origin <base>` always
// writes it, while a remote-tracking ref is a property of how the clone was
// configured and is not there to be relied on.
func CutPoint(baseBranch string, refresh BaseRefresh) string {
	if refresh == RefreshUpdate {
		return "FETCH_HEAD"
	}

	return baseBranch
}

// DescribeBaseUpdate is what to report once the base has been fetched.
//
// It says nothing when the base was already current. A line per run saying "up
// to date" is a line people stop reading, and the whole value of this message
// is that it appears exactly when something moved.
func DescribeBaseUpdate(baseBranch string, behind int) string {
	if behind < 1 {
		return ""
	}

	plural := "s"
	if behind == 1 {
		plural = ""
	}

	return fmt.Sprintf(
		"%s was %d commit%s behind origin — updated. CI tests your work merged into this, "+
			"so a check against the old tip could have disagreed with it.",
		baseBranch, behind, plural,
	)
}

// DivergedBase is the base moving in a way a fast-forward cannot follow:
// somebody has local commits on it.
//
// Never resolved automatically. Cutting from origin is still right — it is
// what CI will merge into — but the local commits are now not in the tree
// being checked, and that is a thing a maintainer has to be told rather than
// have decided for them.
func DivergedBase(baseBranch string) string {
	return fmt.Sprintf(
		"Local %s has commits origin does not, so it was left alone. Cutting from origin/%s instead, "+
			"because that is what CI merges into — your local commits are not in what is about to be checked.",
		baseBranch, baseBranch,
	)
}

// SkippedBaseUpdate is what a skip says, so a stale verdict is never a silent
// one.
func SkippedBaseUpdate(baseBranch string) string {
	return fmt.Sprintf(
		"Not updating %s (--no-update). This checks against the base as it stands here, "+
			"which may not be what CI merges into.",
		baseBranch,
	)
}

// UnreachableBaseError is asked to update and could not.
//
// A refusal rather than a warning, and deliberately: everything else here
// degrades and says so, but the degraded result in this one case is a verdict
// that looks exactly like a good one and gets cached as evidence the fast-lane
// gate reads. Exit rather than record it — and name the flag that makes it a
// choice.
func UnreachableBaseError(baseBranch, output string) error {
	detail := "  (no output from git)"
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		detail = "  " + trimmed
	}

	return fmt.Errorf(
		"could not fetch %s from origin, so upkeep cannot tell what CI would test this against:\n%s\n"+
			"Re-run with --no-update to check against the base as it stands here instead",
		baseBranch, detail,
	)
}

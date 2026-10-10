package adapter

import (
	"fmt"
	"slices"

	"github.com/owenbush/upkeep/internal/gitlab"
)

// Pure logic behind applying a merge request: how one maps onto git refs in
// the module working copy, and the native-base rule.
//
// **The ref is the merge, not the head, and that is the whole point.** GitLab
// publishes two refs per merge request: /head is the contributor's branch,
// /merge is that branch merged into the *current* tip of the target. CI
// analyses /merge. upkeep fetched /head, so the two were reading different
// trees on any merge request whose branch had fallen behind — which is nearly
// all of them. Measured on pathauto: of 25 open merge requests, **23 have a
// merge tree that differs from their head tree**, and branches run 7 to 41
// commits behind the target.
//
// A merge-request branch is not stale in the way a fetch fixes. It is one
// commit of work on top of the target *as it was months ago*, and the tree CI
// runs exists on neither side until GitLab computes it. Fetching the branch
// harder never produces it. This is the same failure as applying a patch to a
// stale base — a clean merge whose result nobody has ever compiled — arriving
// by the other door.
//
// /merge is absent when GitLab cannot compute it, which means the merge
// request conflicts with its target. That is worth saying rather than quietly
// substituting /head, so the fallback is loud.
//
// Backport testing — running a merge request against a base other than the
// branch it targets — is out of scope, so applying refuses a target/base
// mismatch up front.

// MergeRef is GitLab's ref for the branch merged into the current target tip.
func MergeRef(iid int) string { return fmt.Sprintf("refs/merge-requests/%d/merge", iid) }

// HeadRef is GitLab's ref for the contributor's branch as it stands.
func HeadRef(iid int) string { return fmt.Sprintf("refs/merge-requests/%d/head", iid) }

// FetchRefspec is the refspec to fetch, for whichever ref is being used.
//
// Force-updating (+) so re-applying a merge request that moved since the last
// fetch updates mr-<iid> instead of failing non-fast-forward. The merge ref
// moves for a second reason the head ref does not: GitLab recomputes it
// whenever the *target* gains a commit, so a re-check after an unchanged merge
// request can still be a different tree — which is exactly the thing worth
// re-checking.
func FetchRefspec(ref string, iid int) string {
	return fmt.Sprintf("+%s:%s", ref, ManagedBranchForMergeRequest(iid))
}

// PreferredRef is which ref to check out, given what the remote actually
// advertises.
//
// It reports false when the merge request has neither, which is not a state to
// guess about: the iid is wrong, or the merge request was removed.
func PreferredRef(advertised []string, iid int) (string, bool) {
	for _, ref := range []string{MergeRef(iid), HeadRef(iid)} {
		if slices.Contains(advertised, ref) {
			return ref, true
		}
	}

	return "", false
}

// NoMergeRefWarning is what to say when only the head ref exists.
//
// GitLab computes no merge ref for a merge request that conflicts with its
// target, so this is a diagnosis and not a detail: the contribution does not
// currently apply, CI has nothing to run either, and whatever is checked
// locally is the branch alone.
func NoMergeRefWarning(iid int) string {
	return fmt.Sprintf(
		"MR !%d has no merge ref: GitLab could not merge it into its target, which normally means a conflict. "+
			"Checking the branch on its own instead — this is not what CI runs, and the result says nothing "+
			"about how the work behaves once merged.",
		iid,
	)
}

// ErrNoRefsAtAll is the refusal for a merge request that advertises no ref.
func ErrNoRefsAtAll(iid int) error {
	return fmt.Errorf(
		"MR !%d publishes no refs on origin — neither %s nor %s. Check the merge request number",
		iid, MergeRef(iid), HeadRef(iid),
	)
}

// ResolveBaseBranch is the working copy's base branch.
//
// Shared by both apply paths: a patch applied on top of a merge request's
// branch, or the reverse, would be testing two contributions at once while
// reporting on one.
//
// currentBranch is "" when HEAD is detached; recordedBase is the
// upkeep.base-branch git config value, when set.
func ResolveBaseBranch(currentBranch, recordedBase string) (string, error) {
	// The record is a fallback, not an override. It exists to answer the one
	// question the working copy cannot: what an upkeep-managed branch was cut
	// from, while you are sitting on it. When the working copy is on a real
	// branch, *that* is the base, and the record is at best a description of
	// some earlier state.
	//
	// It used to win outright, which made a stale record permanent. Check a
	// patch (recording 1.0.x), move to the branch a merge request targets, and
	// the check still refused on a 1.0.x base — telling you to run
	// `upkeep dev <module> --branch=2.0.x`, which is what you had just run.
	// Found by the nightly full check, twice.
	//
	// Clearing the record on checkout would not have been enough: the module
	// working copy is a real clone and `git checkout` in it is ordinary,
	// expected use, so the record can go stale without upkeep ever being told.
	if currentBranch != "" && !IsManagedBranch(currentBranch) && !IsWorkBranch(currentBranch) {
		return currentBranch, nil
	}

	if recordedBase != "" {
		return recordedBase, nil
	}

	if currentBranch == "" {
		return "", fmt.Errorf(
			"cannot determine the module working copy's base branch: HEAD is detached " +
				"and no base branch is recorded",
		)
	}

	if IsManagedBranch(currentBranch) {
		return "", fmt.Errorf(
			"cannot determine the module working copy's base branch: it sits on the upkeep-managed branch %q "+
				"and no base branch is recorded",
			currentBranch,
		)
	}

	// A work branch is a base nobody meant. Cutting mr-<iid> or patch-<nid>
	// from it would test the contribution *plus* whatever the maintainer has
	// written and not pushed, and report the result as a verdict on the
	// contribution alone. Refused rather than guessed, because the wrong
	// answer here is a green check on code that was never actually tested.
	//
	// The last case, and unconditional: everything a real branch could answer
	// was answered at the top, so anything still here is a work branch with no
	// record behind it.
	return "", fmt.Errorf(
		"the module working copy is on your own work branch %q. Checking a contribution from here would "+
			"test it on top of that work. Switch to the target branch first "+
			"(git -C <module> checkout <base>), then re-run",
		currentBranch,
	)
}

// AssertNativeBase is the native-base rule: a merge request may only be
// applied against the branch it targets.
//
// Applying it to any other base would be backport testing, which upkeep
// deliberately does not do.
func AssertNativeBase(mergeRequest gitlab.MergeRequest, baseBranch, moduleName string) error {
	if mergeRequest.TargetBranch == baseBranch {
		return nil
	}

	// Named, not described. "Apply the merge request in an environment whose
	// base is its target branch" is true and leaves you working out how — and
	// the how is one command. Every other refusal here ends in something you
	// can paste; this one did not, and a module whose default branch is not
	// the branch its merge requests target hits it on the first try.
	return fmt.Errorf(
		"MR !%d targets branch %q, but the environment's module working copy is based on branch %q. "+
			"Backport testing is out of scope: a verdict from the wrong base is about code nobody proposed.\n"+
			"Re-base the working copy on the branch the MR targets, then re-run:\n"+
			"  upkeep dev %s --branch=%s",
		mergeRequest.IID, mergeRequest.TargetBranch, baseBranch, moduleName, mergeRequest.TargetBranch,
	)
}

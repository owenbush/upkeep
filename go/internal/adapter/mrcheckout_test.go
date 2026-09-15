package adapter

import (
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/gitlab"
)

// CI analyses the merge, not the branch. Measured on pathauto: of 25 open
// merge requests, 23 have a merge tree that differs from their head tree.
func TestTheMergeRefIsPreferredOverTheHead(t *testing.T) {
	both := []string{
		"refs/heads/2.0.x",
		HeadRef(12),
		MergeRef(12),
	}

	ref, found := PreferredRef(both, 12)
	if !found {
		t.Fatal("neither ref was found")
	}
	if ref != MergeRef(12) {
		t.Errorf("chose %q, want the merge ref", ref)
	}
}

// GitLab computes no merge ref for a merge request that conflicts with its
// target, so the fallback is a diagnosis rather than a detail.
func TestWithNoMergeRefTheHeadIsUsedLoudly(t *testing.T) {
	ref, found := PreferredRef([]string{HeadRef(12)}, 12)
	if !found || ref != HeadRef(12) {
		t.Fatalf("got %q, %v", ref, found)
	}

	warning := NoMergeRefWarning(12)
	for _, want := range []string{"!12", "conflict", "not what CI runs", "says nothing"} {
		if !strings.Contains(warning, want) {
			t.Errorf("the warning does not carry %q:\n%s", want, warning)
		}
	}
}

// Neither ref at all is not a state to guess about: the iid is wrong, or the
// merge request was removed.
func TestNeitherRefIsARefusalNotAFallback(t *testing.T) {
	if ref, found := PreferredRef([]string{"refs/heads/2.0.x", MergeRef(99)}, 12); found {
		t.Errorf("found %q for a merge request that publishes nothing", ref)
	}

	err := ErrNoRefsAtAll(12)
	for _, want := range []string{"!12", "refs/merge-requests/12/merge", "Check the merge request number"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %v", want, err)
		}
	}
}

// Force-updating, because the merge ref moves whenever the *target* gains a
// commit — a re-check after an unchanged merge request can still be a
// different tree.
func TestTheRefspecForcesAndLandsOnTheManagedBranch(t *testing.T) {
	spec := FetchRefspec(MergeRef(12), 12)

	if !strings.HasPrefix(spec, "+") {
		t.Errorf("refspec %q does not force-update", spec)
	}
	if !strings.HasSuffix(spec, ":"+ManagedBranchForMergeRequest(12)) {
		t.Errorf("refspec %q does not land on the managed branch", spec)
	}
}

// The record is a fallback, not an override. It used to win outright, which
// made a stale record permanent — found by the nightly full check, twice.
func TestARealBranchOutranksAStaleRecord(t *testing.T) {
	got, err := ResolveBaseBranch("2.0.x", "1.0.x")
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if got != "2.0.x" {
		t.Errorf("got %q, want the branch the working copy is actually on", got)
	}
}

// The record answers the one question the working copy cannot: what a managed
// branch was cut from, while you are sitting on it.
func TestTheRecordAnswersForAManagedBranch(t *testing.T) {
	for _, branch := range []string{ManagedBranchForMergeRequest(12), ManagedBranchForPatch(3603341)} {
		got, err := ResolveBaseBranch(branch, "2.0.x")
		if err != nil {
			t.Errorf("%s: unexpected refusal: %v", branch, err)

			continue
		}
		if got != "2.0.x" {
			t.Errorf("%s: got %q", branch, got)
		}
	}
}

// Cutting a disposable branch from a work branch would test the contribution
// *plus* unpushed work, and report the result as a verdict on the
// contribution alone.
func TestAWorkBranchIsNeverABase(t *testing.T) {
	_, err := ResolveBaseBranch("3603341-drupal-12-compatibility", "")
	if err == nil {
		t.Fatal("a work branch was accepted as a base")
	}
	for _, want := range []string{"your own work branch", "on top of that work", "git -C <module> checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %v", want, err)
		}
	}

	// And a record behind it is still the answer.
	got, err := ResolveBaseBranch("3603341-drupal-12-compatibility", "2.0.x")
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if got != "2.0.x" {
		t.Errorf("got %q", got)
	}
}

// A detached HEAD with nothing recorded has no base to find.
func TestADetachedHeadWithNoRecordIsARefusal(t *testing.T) {
	_, err := ResolveBaseBranch("", "")
	if err == nil {
		t.Fatal("a base was resolved from nothing")
	}
	if !strings.Contains(err.Error(), "HEAD is detached") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	got, err := ResolveBaseBranch("", "2.0.x")
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if got != "2.0.x" {
		t.Errorf("got %q", got)
	}
}

func TestAManagedBranchWithNoRecordIsARefusalThatNamesIt(t *testing.T) {
	_, err := ResolveBaseBranch(ManagedBranchForMergeRequest(12), "")
	if err == nil {
		t.Fatal("a base was resolved from a managed branch alone")
	}
	if !strings.Contains(err.Error(), "upkeep-managed branch \"mr-12\"") {
		t.Errorf("the refusal does not name the branch: %v", err)
	}
}

// A verdict from the wrong base is about code nobody proposed.
func TestAMergeRequestMayOnlyBeAppliedAgainstItsOwnTarget(t *testing.T) {
	mr := gitlab.MergeRequest{IID: 12, TargetBranch: "2.0.x"}

	if err := AssertNativeBase(mr, "2.0.x", "pathauto"); err != nil {
		t.Errorf("its own target was refused: %v", err)
	}

	err := AssertNativeBase(mr, "1.0.x", "pathauto")
	if err == nil {
		t.Fatal("backport testing was allowed")
	}
	// Named, not described: every other refusal here ends in something you can
	// paste.
	if !strings.Contains(err.Error(), "upkeep dev pathauto --branch=2.0.x") {
		t.Errorf("the refusal does not name the command that fixes it:\n%v", err)
	}
	for _, want := range []string{"!12", "\"2.0.x\"", "\"1.0.x\"", "Backport testing is out of scope"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q:\n%v", want, err)
		}
	}
}

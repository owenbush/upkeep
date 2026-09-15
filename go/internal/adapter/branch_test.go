package adapter

import (
	"strings"
	"testing"
)

// The two kinds are deliberately unable to be confused: no managed prefix can
// ever begin with a digit, and a work branch always does.
//
// That is what stops `git checkout -B` — which the managed branches get on
// every apply — from ever landing on a branch holding the only copy of
// somebody's work.
func TestAWorkBranchAndAManagedBranchCannotBeConfused(t *testing.T) {
	managed := []string{
		ManagedBranchForMergeRequest(12),
		ManagedBranchForMergeRequest(3597808),
		ManagedBranchForPatch(3603341),
	}
	work := []string{
		IssueBranchFor(3603341, "Drupal 12 compatibility").Name,
		IssueBranchFor(3603341, "").Name,
		"3597808-fix-the-thing",
		"3597808",
	}

	for _, branch := range managed {
		if !IsManagedBranch(branch) {
			t.Errorf("%q is not recognised as managed", branch)
		}
		if IsWorkBranch(branch) {
			t.Errorf("%q reads as a work branch — checkout -B would discard commits", branch)
		}
	}

	for _, branch := range work {
		if IsManagedBranch(branch) {
			t.Errorf("%q reads as managed — it would be reset from the base", branch)
		}
		if !IsWorkBranch(branch) {
			t.Errorf("%q is not recognised as issue work", branch)
		}
	}
}

// Resolving a base of patch-3597808 would silently test the next contribution
// on top of the previous one.
func TestOnlyTheTwoManagedShapesAreManaged(t *testing.T) {
	for _, branch := range []string{
		"2.0.x", "1.0.x", "main", "mr-", "patch-", "mr-abc", "patch-x",
		"mr-12-extra", "feature/mr-12", "MR-12", "3603341-mr-12",
	} {
		if IsManagedBranch(branch) {
			t.Errorf("%q was treated as a branch upkeep created", branch)
		}
	}
}

// Keyed by issue, not by file: re-applying a different patch from the same
// issue replaces the branch rather than accumulating one per re-roll.
func TestAPatchBranchIsKeyedByIssue(t *testing.T) {
	if ManagedBranchForPatch(3603341) != ManagedBranchForPatch(3603341) {
		t.Error("two applies of the same issue named different branches")
	}
	if ManagedBranchForPatch(3603341) == ManagedBranchForMergeRequest(3603341) {
		t.Error("a patch and a merge request with the same number share a branch")
	}
}

// Everything that matters keys on the leading node id, so a title that reduces
// to nothing still yields a usable branch rather than a failure.
func TestATitleThatReducesToNothingStillNamesABranch(t *testing.T) {
	for _, title := range []string{"", "   ", "!!!", "---", "日本語"} {
		branch := IssueBranchFor(3603341, title)
		if branch.Name != "3603341" {
			t.Errorf("title %q gave branch %q", title, branch.Name)
		}
		if !IsWorkBranch(branch.Name) {
			t.Errorf("branch %q is not usable as issue work", branch.Name)
		}
	}
}

// Named to drupal.org's convention so the merge request that follows is linked
// to the issue with nothing further to configure.
func TestTheBranchNameFollowsDrupalOrgsConvention(t *testing.T) {
	branch := IssueBranchFor(3603341, "Drupal 12 compatibility")

	if branch.Name != "3603341-drupal-12-compatibility" {
		t.Errorf("got %q", branch.Name)
	}
}

// Truncated on a word boundary so the name stays readable.
func TestALongTitleIsCutOnAWordBoundary(t *testing.T) {
	slug := Slug("Automated Drupal 12 compatibility fixes for the pathauto module and everything else")

	if len(slug) > maxSlugLength {
		t.Errorf("slug %q is %d characters", slug, len(slug))
	}
	if strings.HasSuffix(slug, "-") {
		t.Errorf("slug %q ends mid-join", slug)
	}
	// The cut lands between words, not inside one.
	if strings.HasPrefix("compatibility", slug[strings.LastIndex(slug, "-")+1:]) &&
		slug[strings.LastIndex(slug, "-")+1:] != "compatibility" {
		t.Errorf("slug %q cut a word in half", slug)
	}
}

// A single word longer than the limit has no boundary to cut on, and must
// still produce something.
func TestAnUnbreakableTitleStillSlugs(t *testing.T) {
	slug := Slug(strings.Repeat("x", 100))

	if slug == "" {
		t.Error("an unbreakable title reduced to nothing")
	}
	if len(slug) > maxSlugLength {
		t.Errorf("slug is %d characters", len(slug))
	}
}

// The test that lets `start` resume yesterday's work instead of starting
// beside it.
func TestABranchMatchesItsOwnIssueAndNothingElse(t *testing.T) {
	branch := IssueBranchFor(3603341, "Drupal 12 compatibility")

	for _, name := range []string{
		"3603341-drupal-12-compatibility",
		"3603341",
		"3603341-something-else-entirely",
	} {
		if !branch.Matches(name) {
			t.Errorf("%q did not match its own issue's branch", name)
		}
	}

	for _, name := range []string{
		"3603342-drupal-12-compatibility",
		"36033410",
		"36033411-fix",
		"2.0.x",
		"mr-3603341",
	} {
		if branch.Matches(name) {
			t.Errorf("%q matched an issue it does not belong to", name)
		}
	}
}

// An explicitly named branch is still anchored to its issue, so resuming works
// the same way.
func TestAnExplicitlyNamedBranchKeepsItsIssue(t *testing.T) {
	branch := NamedIssueBranch(3603341, "my-own-name")

	if branch.IssueNid != 3603341 {
		t.Errorf("issue %d", branch.IssueNid)
	}
	if !branch.Matches("my-own-name") {
		t.Error("it does not match itself")
	}
	if !branch.Matches("3603341-anything") {
		t.Error("it lost its anchor to the issue")
	}
}

// Four digits is the threshold: a drupal.org node id is always longer than
// that, and it keeps short numeric branch names out.
func TestAShortNumericBranchIsNotIssueWork(t *testing.T) {
	for _, branch := range []string{"1", "12", "123", "1-x", "123-fix"} {
		if IsWorkBranch(branch) {
			t.Errorf("%q read as issue work", branch)
		}
	}
	for _, branch := range []string{"1234", "1234-fix", "3603341-drupal-12"} {
		if !IsWorkBranch(branch) {
			t.Errorf("%q did not read as issue work", branch)
		}
	}
}

// The boundary is what keeps "<nid>" and "<nid>-<slug>" the only shapes that
// read as issue work. Without it a branch that merely *starts* with a long
// number would be refused the disposable-branch machinery for no reason, and —
// worse in the other direction — the managed prefixes rely on the two kinds
// being disjoint.
func TestIssueWorkIsTheNidOrTheNidAndASlugAndNothingElse(t *testing.T) {
	for _, branch := range []string{"3603341", "3603341-fix", "3603341-drupal-12-compatibility"} {
		if !IsWorkBranch(branch) {
			t.Errorf("%q is not recognised as issue work", branch)
		}
	}

	for _, branch := range []string{
		"3603341abc",
		"3603341x-fix",
		"3603341.0",
		"3603341/fix",
		"3603341_fix",
	} {
		if IsWorkBranch(branch) {
			t.Errorf("%q read as issue work — the digits run into something else", branch)
		}
	}
}

// Truncated on a word boundary, exactly: the cut lands on the hyphen before
// the word it would otherwise split, not in the middle of it.
func TestTheTruncationLandsOnTheBoundaryNotMidWord(t *testing.T) {
	// 40 characters falls inside "fixation", so the cut retreats to the hyphen
	// before it.
	got := Slug("Automated Drupal 12 compatibility fixation for everything")

	if got != "automated-drupal-12-compatibility" {
		t.Errorf("got %q, want the cut on the word boundary", got)
	}
}

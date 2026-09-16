package drupal

import "testing"

func TestOnlyPatchesAndDiffsCountAsPatches(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"3597808-9.patch", true},
		{"3597808-9.diff", true},
		{"3597808-9.PATCH", true},
		{"screenshot.png", false},
		{"notes.txt", false},
	} {
		if got := (IssueFile{Name: tc.name}).IsPatch(); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.name, got, tc.want)
		}
	}
}

// Higher comment number is a newer revision, which beats the timestamp: a
// re-roll posted out of order still sorts correctly.
func TestTheLatestPatchIsTheHighestCommentNumber(t *testing.T) {
	issue := Issue{Files: []IssueFile{
		{Name: "3597808-4.patch", Timestamp: 300},
		{Name: "3597808-12.patch", Timestamp: 100},
		{Name: "screenshot.png", Timestamp: 999},
	}}

	got, ok := issue.LatestPatch()
	if !ok {
		t.Fatal("no patch found")
	}
	if got.Name != "3597808-12.patch" {
		t.Errorf("got %q, want the higher comment number even though it is older", got.Name)
	}
	if issue.PatchCount() != 2 {
		t.Errorf("PatchCount = %d, want 2 — the image is not a patch", issue.PatchCount())
	}
}

// Without comment numbers there is nothing but the timestamp.
func TestPatchesWithoutCommentNumbersFallBackToTheTimestamp(t *testing.T) {
	issue := Issue{Files: []IssueFile{
		{Name: "fix.patch", Timestamp: 100},
		{Name: "better-fix.patch", Timestamp: 500},
	}}

	got, _ := issue.LatestPatch()
	if got.Name != "better-fix.patch" {
		t.Errorf("got %q, want the newer one", got.Name)
	}
}

func TestAnIssueWithNoPatchesSaysSoRatherThanGuessing(t *testing.T) {
	issue := Issue{Files: []IssueFile{{Name: "screenshot.png"}}}

	if _, ok := issue.LatestPatch(); ok {
		t.Error("found a patch where there is none")
	}
	if issue.PatchCount() != 0 {
		t.Errorf("PatchCount = %d, want 0", issue.PatchCount())
	}
}

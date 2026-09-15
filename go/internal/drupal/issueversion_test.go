package drupal

import (
	"reflect"
	"testing"
)

// The version field holds whatever anyone typed, so candidates are proposed
// most-specific-first and the caller intersects them with real branches.
func TestBranchCandidatesForTheShapesSeenLive(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    []string
	}{
		{"2.0.0", []string{"2.0.0", "2.0.x", "2.x"}},
		{"2.0.x-dev", []string{"2.0.x", "2.0.x", "2.x"}[1:]},
		{"8.0.x-dev", []string{"8.0.x", "8.x"}},
		{"8.x-1.4", []string{"8.x-1.4", "8.x-1.x"}},
		{"5.1", []string{"5.1", "5.1.x", "5.x"}},
		{"6.14", []string{"6.14", "6.14.x", "6.x"}},
		{"x.y.z", []string{"x.y.z"}},
		{"", nil},
		{"   ", nil},
	} {
		if got := BranchCandidates(tc.version); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("BranchCandidates(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

func TestResolveBranchTakesTheMostSpecificBranchThatExists(t *testing.T) {
	existing := []string{"1.0.x", "2.0.x", "2.x"}

	for _, tc := range []struct{ version, want string }{
		{"2.0.0", "2.0.x"},
		{"2.0.x-dev", "2.0.x"},
		{"3.0.0", ""},
		{"", ""},
	} {
		if got := ResolveBranch(tc.version, existing); got != tc.want {
			t.Errorf("ResolveBranch(%q) = %q, want %q", tc.version, got, tc.want)
		}
	}
}

// A version naming no real branch matches nothing and the caller falls back;
// it is never an error.
func TestAVersionNamingNoRealBranchSimplyMatchesNothing(t *testing.T) {
	if got := ResolveBranch("x.y.z", []string{"1.0.x"}); got != "" {
		t.Errorf("got %q, want no match", got)
	}
}

func TestOpenStatusesCoverTheWholeLiveQueueNotJustContributions(t *testing.T) {
	open := map[IssueStatus]bool{}
	for _, status := range OpenStatuses() {
		open[status] = true
	}

	// The 55% the tool used to be blind to: work that has not started yet.
	for _, status := range []IssueStatus{StatusActive, StatusNeedsWork, StatusPostponed, StatusPostponedNeedsInfo} {
		if !open[status] {
			t.Errorf("%v is missing from the open queue", status)
		}
	}
	for _, status := range []IssueStatus{StatusFixed, StatusClosedFixed, StatusClosedDuplicate, StatusClosedOutdated} {
		if open[status] {
			t.Errorf("%v is closed but counted as open", status)
		}
	}
}

func TestAwaitsMaintainerIsTheVerdictStatusesOnly(t *testing.T) {
	for _, status := range []IssueStatus{StatusNeedsReview, StatusRtbc} {
		if !status.AwaitsMaintainer() {
			t.Errorf("%v should await a maintainer", status)
		}
	}
	for _, status := range []IssueStatus{StatusActive, StatusNeedsWork, StatusPostponed} {
		if status.AwaitsMaintainer() {
			t.Errorf("%v is waiting on somebody else, not the maintainer", status)
		}
	}
}

func TestEveryKnownStatusHasALabel(t *testing.T) {
	for _, status := range allStatuses {
		if !status.Known() || status.String() == "unknown status" {
			t.Errorf("status %d has no label", status)
		}
	}
	if IssueStatus(999).Known() {
		t.Error("an unknown status reported as known")
	}
}

package drupal

import "testing"

func TestTheTitleConventionAndBranchNameBothClaimAuthorship(t *testing.T) {
	for _, tc := range []struct {
		name, title, branch, description string
		want                             int
	}{
		{"title convention", "Issue #3597808: Fix the widget", "some-branch", "", 3597808},
		{"issue fork branch", "Whatever", "3597808-fix-the-widget", "", 3597808},
		{"issue url in description", "Whatever", "b", "see https://www.drupal.org/project/x/issues/3597808", 3597808},
		{"node url in description", "Whatever", "b", "see https://www.drupal.org/node/3597808", 3597808},
	} {
		got, ok := ExtractOwningIssue(tc.title, tc.branch, tc.description)
		if !ok || got != tc.want {
			t.Errorf("%s: got %d (%v), want %d", tc.name, got, ok, tc.want)
		}
	}
}

// The rule that stops a bot suppressing an issue's patches by mentioning it.
func TestARelatesToMentionIsNotAClaimOfAuthorship(t *testing.T) {
	title, branch, description := "Automated Project Update Bot fixes", "project-update-bot-only", "Relates to #3597808"

	if _, ok := ExtractOwningIssue(title, branch, description); ok {
		t.Error("a bare mention was treated as authorship")
	}

	got, ok := ExtractIssue(title, branch, description)
	if !ok || got != 3597808 {
		t.Errorf("the loose reading should still find it: got %d (%v)", got, ok)
	}
}

// A fact about how the repository came to exist, not a string somebody typed —
// and the only thing that pairs a bot merge request to its issue.
func TestTheForkPathIsTheStrongestClaimThereIs(t *testing.T) {
	for _, tc := range []struct {
		path string
		want int
		ok   bool
	}{
		{"issue/pathauto-3616056", 3616056, true},
		{"issue/static_setting_contexts-3603341", 3603341, true},
		{"project/pathauto", 0, false},
		{"issue/pathauto-12", 0, false}, // too short to be a nid
		{"  issue/pathauto-3616056  ", 3616056, true},
	} {
		got, ok := IssueFromForkPath(tc.path)
		if ok != tc.ok || got != tc.want {
			t.Errorf("IssueFromForkPath(%q) = %d,%v want %d,%v", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNothingToFindIsNotAnError(t *testing.T) {
	if _, ok := ExtractIssue("Fix a thing", "fix-a-thing", "no references here"); ok {
		t.Error("found an issue where there is none")
	}
}

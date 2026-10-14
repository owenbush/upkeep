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

// GitLab's closing vocabulary pairs a merge request to its issue, which is how
// a project whose queue is GitLab's declares what a branch fixes.
//
// Demonstrated on helper, whose issues have moved: MR !94 is on branch
// `url-helper` with description "Closes #3619171". No "Issue #" in the title,
// no leading digits in the branch, no drupal.org URL — so it paired with
// nothing, while GitLab's own /closes_issues endpoint knew the answer.
func TestGitlabsClosingVocabularyPairsAMergeRequest(t *testing.T) {
	for _, declaration := range []string{
		"Closes #3619171", "closes #3619171", "Closed #3619171", "closing #3619171",
		"Fix #3619171", "Fixes #3619171", "fixed #3619171", "Fixing #3619171",
		"Resolve #3619171", "Resolves #3619171", "resolved #3619171", "Resolving #3619171",
		"Implement #3619171", "Implements #3619171", "implemented #3619171",
		// GitLab allows a colon, and prose either side.
		"Closes: #3619171", "This one closes #3619171 at last.",
	} {
		nid, found := ExtractOwningIssue("Added URL helper", "url-helper", declaration)
		if !found || nid != 3619171 {
			t.Errorf("%q gave %d,%v", declaration, nid, found)
		}
	}
}

// A mention that does not claim to fix the issue still cannot claim it.
//
// The owning rule exists so nobody can suppress an issue's patches by
// referring to it, and adding a second vocabulary must not open that door.
func TestAMentionStillCannotClaimAnIssue(t *testing.T) {
	for _, mention := range []string{
		"Relates to #3619171",
		"See #3619171",
		"Follow-up to #3619171",
		"Blocked by #3619171",
	} {
		if nid, found := ExtractOwningIssue("x", "b", mention); found {
			t.Errorf("%q claimed issue %d", mention, nid)
		}
	}
}

// The verb has to be the whole word: "prefixes #12" is not "fixes #12", and a
// word boundary is the only thing standing between the two.
func TestAVerbInsideAnotherWordDoesNotPair(t *testing.T) {
	for _, text := range []string{
		"prefixes #123", "unclosed #123", "affixes #123", "irresolvable #123",
	} {
		if nid, found := ExtractOwningIssue("x", "b", text); found {
			t.Errorf("%q paired to %d", text, nid)
		}
	}
}

// Any number of digits, unlike a bare number in a branch name: "#12" after a
// closing verb is unambiguous, and a GitLab-native queue starts at #1.
func TestAShortIssueNumberPairsWhenDeclared(t *testing.T) {
	nid, found := ExtractOwningIssue("x", "some-branch", "Fixes #7")
	if !found || nid != 7 {
		t.Errorf("got %d,%v, want 7,true", nid, found)
	}
	// Where a bare short number in a branch name is still ignored, because
	// there it could be anything.
	if nid, found := ExtractOwningIssue("x", "7-whatever", ""); found {
		t.Errorf("a two-digit branch prefix paired to %d", nid)
	}
}

// The drupal.org signals still win where they exist, so nothing about a
// project whose queue has not moved changes.
func TestTheDrupalOrgSignalsStillTakePrecedence(t *testing.T) {
	// Title convention beats a closing declaration in the description.
	nid, found := ExtractOwningIssue("Issue #1111111: fix it", "b", "Closes #2222222")
	if !found || nid != 1111111 {
		t.Errorf("title lost: got %d,%v", nid, found)
	}
	// Branch convention likewise.
	nid, found = ExtractOwningIssue("x", "3333333-fix-it", "Closes #2222222")
	if !found || nid != 3333333 {
		t.Errorf("branch lost: got %d,%v", nid, found)
	}
}

// A closing declaration outranks a mention in the same description.
//
// Which is not hypothetical: a Project Update Bot merge request says only
// "Relates to #NNN", and somebody taking it over adds what it actually fixes.
// Reading the mention first would pair the merge request to the issue it
// merely refers to and ignore the one it claims.
func TestAClosingDeclarationOutranksAMentionBesideIt(t *testing.T) {
	both := "Relates to #1111111\n\nCloses #2222222"

	nid, found := ExtractIssue("Automated Project Update Bot fixes", "project-update-bot-only", both)
	if !found || nid != 2222222 {
		t.Errorf("got %d,%v — the mention won over the declaration", nid, found)
	}

	// And in owning mode the mention is not even a candidate.
	nid, found = ExtractOwningIssue("Automated Project Update Bot fixes", "project-update-bot-only", both)
	if !found || nid != 2222222 {
		t.Errorf("owning: got %d,%v", nid, found)
	}
}

// drupal.org's short link is a reference like any other.
//
// `drupal.org/i/<nid>` 302s to the full issue URL and is what the issue page
// offers you, so people paste it into merge request descriptions. Not knowing
// the shape meant losing the pairing on an ordinary reference somebody else
// wrote — found when a published description built from it paired with
// nothing.
func TestTheShortIssueLinkPairs(t *testing.T) {
	for _, text := range []string{
		"Fixes https://www.drupal.org/i/3223746",
		"see https://drupal.org/i/3223746 for the details",
	} {
		nid, found := ExtractOwningIssue("", "", text)
		if !found || nid != 3223746 {
			t.Errorf("%q -> %d, %t", text, nid, found)
		}
	}
}

// A host that merely contains "drupal.org" is not drupal.org.
//
// Every one of the three URL patterns needed a word boundary and none had one,
// so `notdrupal.org/node/123` paired as though it were an issue reference.
// Tested across all three rather than just the one that exposed it, because
// the omission was identical in each.
func TestALookalikeHostDoesNotPair(t *testing.T) {
	for _, text := range []string{
		"notdrupal.org/i/3223746 is somewhere else",
		"notdrupal.org/node/3223746 is somewhere else",
		"notdrupal.org/project/pathauto/issues/3223746 is somewhere else",
		"mydrupal.org/node/3223746",
	} {
		if nid, found := ExtractOwningIssue("", "", text); found {
			t.Errorf("%q paired with %d", text, nid)
		}
	}
}

// And a reference needs an actual number.
func TestAShortLinkNeedsANumber(t *testing.T) {
	for _, text := range []string{
		"drupal.org/i/not-a-number",
		"drupal.org/node/",
	} {
		if nid, found := ExtractOwningIssue("", "", text); found {
			t.Errorf("%q paired with %d", text, nid)
		}
	}
}

// The real host still pairs on every shape, including with a subdomain.
func TestTheRealHostPairsWithOrWithoutWww(t *testing.T) {
	for _, text := range []string{
		"https://www.drupal.org/node/3223746",
		"https://drupal.org/node/3223746",
		"drupal.org/i/3223746",
	} {
		nid, found := ExtractOwningIssue("", "", text)
		if !found || nid != 3223746 {
			t.Errorf("%q -> %d, %t", text, nid, found)
		}
	}
}

// Every URL shape drupal.org serves for an issue reaches the same nid.
func TestEveryIssueUrlShapeAgrees(t *testing.T) {
	for _, text := range []string{
		"https://www.drupal.org/node/3223746",
		"https://www.drupal.org/i/3223746",
		"https://www.drupal.org/project/pathauto/issues/3223746",
	} {
		nid, found := ExtractOwningIssue("", "", text)
		if !found || nid != 3223746 {
			t.Errorf("%q -> %d, %t", text, nid, found)
		}
	}
}

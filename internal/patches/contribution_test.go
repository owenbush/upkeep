package patches

import (
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
)

func realMr(iid int, title, branch string) gitlab.MergeRequest {
	return gitlab.MergeRequest{
		IID: iid, State: "opened", Title: title, SourceBranch: branch,
		DiffBaseSHA: "base", DiffHeadSHA: "head",
	}
}

func emptyMr(iid int, title, branch string) gitlab.MergeRequest {
	mr := realMr(iid, title, branch)
	mr.DiffBaseSHA, mr.DiffHeadSHA = "same", "same"

	return mr
}

// The fork is a fact about how the repository exists, rather than a string in
// a title — and it is the only thing that pairs a Project Update Bot merge
// request at all.
func TestTheForkOutranksEverySignalInTheMetadata(t *testing.T) {
	bot := realMr(1, "Automated Project Update Bot fixes", "project-update-bot-only")
	bot.Description = "Relates to #9999999"
	bot.SourceProjectID = 501

	issue := drupal.Issue{Nid: 3603341, Title: "Drupal 12 compatibility"}
	paired := Pair("pathauto", []drupal.Issue{issue}, []gitlab.MergeRequest{bot}, map[int]int{501: 3603341})

	if len(paired) != 1 || len(paired[0].MergeRequests) != 1 {
		t.Fatalf("got %+v — the bot merge request paired to nothing", paired)
	}
}

// "Relates to #NNN" is rejected by design, so a bot cannot suppress an issue's
// patches by mentioning it.
func TestAMereMentionDoesNotClaimAnIssue(t *testing.T) {
	mentioning := realMr(1, "Something else entirely", "some-branch")
	mentioning.Description = "Relates to #3603341"

	issue := drupal.Issue{Nid: 3603341}
	paired := Pair("pathauto", []drupal.Issue{issue}, []gitlab.MergeRequest{mentioning}, nil)

	if len(paired[0].MergeRequests) != 0 {
		t.Errorf("a mention claimed the issue: %+v", paired[0].MergeRequests)
	}
}

func TestATitleClaimingAnIssuePairsWithoutAFork(t *testing.T) {
	claiming := realMr(1, "Issue #3603341 by owenbush: Drupal 12 compatibility", "3603341-fix")

	issue := drupal.Issue{Nid: 3603341}
	paired := Pair("pathauto", []drupal.Issue{issue}, []gitlab.MergeRequest{claiming}, nil)

	if len(paired[0].MergeRequests) != 1 {
		t.Errorf("got %+v", paired[0].MergeRequests)
	}
}

// Only the issues asked about get a contribution, so a merge request claiming
// one outside the set cannot reach any row.
func TestMergeRequestsClaimingAnIssueOutsideTheSetAreDropped(t *testing.T) {
	elsewhere := realMr(1, "Issue #9999999 by somebody: Another thing", "9999999-fix")

	issue := drupal.Issue{Nid: 3603341}
	paired := Pair("pathauto", []drupal.Issue{issue}, []gitlab.MergeRequest{elsewhere}, nil)

	if len(paired) != 1 {
		t.Fatalf("got %d contributions", len(paired))
	}
	if len(paired[0].MergeRequests) != 0 {
		t.Errorf("got %+v", paired[0].MergeRequests)
	}
}

// Every issue gets a contribution, whether or not anything claims it.
func TestEveryIssueGetsAContribution(t *testing.T) {
	issues := []drupal.Issue{{Nid: 1}, {Nid: 2}, {Nid: 3}}

	paired := Pair("pathauto", issues, nil, nil)
	if len(paired) != 3 {
		t.Fatalf("got %d contributions for %d issues", len(paired), len(issues))
	}
	for _, contribution := range paired {
		if contribution.Module != "pathauto" {
			t.Errorf("module %q", contribution.Module)
		}
	}
}

// Unknown emptiness counts as real work: that is the reading a list payload
// gives, and inventing empty merge requests out of missing data is the wrong
// direction.
func TestUnknownEmptinessCountsAsSubstantive(t *testing.T) {
	unknown := realMr(1, "t", "b")
	unknown.DiffBaseSHA, unknown.DiffHeadSHA = "", ""

	substantive := Substantive([]gitlab.MergeRequest{unknown, emptyMr(2, "t", "b")})
	if len(substantive) != 1 || substantive[0].IID != 1 {
		t.Errorf("got %+v", substantive)
	}
}

func TestTheKindFollowsWhatActuallyArrived(t *testing.T) {
	withPatch := drupal.Issue{Nid: 1, Files: []drupal.IssueFile{patch("a.patch", 100, 10)}}
	withoutPatch := drupal.Issue{Nid: 1}

	for _, tc := range []struct {
		name          string
		issue         drupal.Issue
		mergeRequests []gitlab.MergeRequest
		want          Kind
	}{
		{"patch alone", withPatch, nil, PatchOnly},
		{"patch and a real branch", withPatch, []gitlab.MergeRequest{realMr(1, "t", "b")}, PatchAndMergeRequest},
		{"patch and an empty draft", withPatch, []gitlab.MergeRequest{emptyMr(1, "t", "b")}, PatchWithEmptyMergeRequest},
		{"branch alone", withoutPatch, []gitlab.MergeRequest{realMr(1, "t", "b")}, MergeRequestOnly},
		{"nothing at all", withoutPatch, nil, Nothing},
		{"an empty draft and nothing else", withoutPatch, []gitlab.MergeRequest{emptyMr(1, "t", "b")}, Nothing},
	} {
		contribution := Contribution{Module: "pathauto", Issue: tc.issue, MergeRequests: tc.mergeRequests}
		if got := contribution.Kind(); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Only the MR-only kind is reachable from the dashboard, and so has no
// business on a patch report.
func TestOnlyMergeRequestOnlyIsCoveredElsewhere(t *testing.T) {
	if !MergeRequestOnly.IsCoveredByMergeRequest() {
		t.Error("an MR-only issue is not covered by the dashboard")
	}
	for _, kind := range []Kind{PatchOnly, PatchAndMergeRequest, PatchWithEmptyMergeRequest, Nothing} {
		if kind.IsCoveredByMergeRequest() {
			t.Errorf("%v was treated as covered", kind)
		}
		if kind.SummaryLabel() == "" {
			t.Errorf("%v has no summary label", kind)
		}
	}
	if MergeRequestOnly.SummaryLabel() != "" {
		t.Error("an MR-only issue is summarised, and it should not be")
	}
}

// A landing outranks everything else the cell could say: it is the one thing a
// maintainer cannot read off the issue at all.
func TestALandingOutranksEverythingElseInTheCell(t *testing.T) {
	landed := realMr(7, "t", "b")
	landed.State = "merged"
	landed.MergedAt = "2026-06-12T10:00:00Z"

	open := realMr(9, "t", "b")

	cell := RenderMergeRequestCell([]gitlab.MergeRequest{open, landed}, &landed, false)
	if !strings.Contains(cell, "!7 merged 2026-06-12") {
		t.Errorf("cell %q", cell)
	}
	if strings.Contains(cell, "!9") {
		t.Errorf("cell %q buries the landing behind the open one", cell)
	}
}

func TestNewerWorkSinceALandingIsSaid(t *testing.T) {
	landed := realMr(7, "t", "b")
	landed.State = "merged"
	landed.MergedAt = "2026-06-12T10:00:00Z"

	cell := RenderMergeRequestCell([]gitlab.MergeRequest{landed}, &landed, true)
	if !strings.Contains(cell, "newer work since") {
		t.Errorf("cell %q", cell)
	}
}

// A bot that posts again after its earlier work merged has raised new work.
func TestAPatchNewerThanTheMergeIsNewerWork(t *testing.T) {
	landed := realMr(7, "t", "b")
	landed.State = "merged"
	landed.MergedAt = "2026-06-12T10:00:00Z"

	// 2026-06-13, the day after.
	after := drupal.Issue{Nid: 1, Files: []drupal.IssueFile{patch("re-roll.patch", 1781308800, 10)}}
	// 2026-01-01, months before.
	before := drupal.Issue{Nid: 1, Files: []drupal.IssueFile{patch("original.patch", 1767225600, 10)}}

	newer := Contribution{Issue: after, MergeRequests: []gitlab.MergeRequest{landed}}
	if !newer.HasWorkNewerThanLanding() {
		t.Error("a patch posted after the merge did not read as newer work")
	}

	older := Contribution{Issue: before, MergeRequests: []gitlab.MergeRequest{landed}}
	if older.HasWorkNewerThanLanding() {
		t.Error("a patch posted before the merge read as newer work")
	}
}

// An open merge request touched after the landing is newer work too; the
// merged one itself is not.
func TestAnOpenMergeRequestTouchedAfterTheLandingCounts(t *testing.T) {
	landed := realMr(7, "t", "b")
	landed.State = "merged"
	landed.MergedAt = "2026-06-12T10:00:00Z"
	// Later than the merge, which is ordinary: a comment or a label after
	// merging bumps updated_at again. Without the state check the merged merge
	// request would report itself as work newer than its own landing.
	landed.UpdatedAt = "2026-07-01T09:00:00Z"

	open := realMr(9, "t", "b")
	open.UpdatedAt = "2026-09-01T10:00:00Z"

	withOpen := Contribution{MergeRequests: []gitlab.MergeRequest{landed, open}}
	if !withOpen.HasWorkNewerThanLanding() {
		t.Error("an open merge request updated after the merge did not count")
	}

	// The merged one alone must not count itself: merging bumps updated_at.
	aloneCase := Contribution{MergeRequests: []gitlab.MergeRequest{landed}}
	if aloneCase.HasWorkNewerThanLanding() {
		t.Error("the merged merge request counted itself as newer work")
	}
}

func TestNothingLandedIsNotNewerWork(t *testing.T) {
	contribution := Contribution{MergeRequests: []gitlab.MergeRequest{realMr(1, "t", "b")}}

	if contribution.Landed() != nil {
		t.Error("an open merge request read as landed")
	}
	if contribution.HasWorkNewerThanLanding() {
		t.Error("newer work than a landing that did not happen")
	}
}

// An issue can carry both a real branch and a bot's empty draft, and the real
// branch is the answer to "is this already in git?".
func TestARealBranchRepresentsTheIssueAheadOfAnEmptyDraft(t *testing.T) {
	cell := RenderMergeRequestCell([]gitlab.MergeRequest{emptyMr(1, "t", "b"), realMr(2, "t", "b")}, nil, false)

	if !strings.HasPrefix(cell, "!2") {
		t.Errorf("cell %q, want the real branch first", cell)
	}
	if strings.Contains(cell, "empty") {
		t.Errorf("cell %q flags the representative as empty when it is not", cell)
	}
	if !strings.Contains(cell, "+1") {
		t.Errorf("cell %q does not count the other", cell)
	}
}

// The bug this closed: a row showing an empty merge request and a patch used
// to suggest a check that would do nothing.
func TestAnEmptyRepresentativeIsFlagged(t *testing.T) {
	cell := RenderMergeRequestCell([]gitlab.MergeRequest{emptyMr(1, "t", "b")}, nil, false)

	if cell != "!1 empty" {
		t.Errorf("cell %q", cell)
	}
}

func TestNoMergeRequestsIsADash(t *testing.T) {
	if got := RenderMergeRequestCell(nil, nil, false); got != "–" {
		t.Errorf("got %q", got)
	}
}

// Nothing here is mergeable, and a cell that looked like a gate verdict would
// invite the wrong action.
func TestTheDashboardStatusSaysWhatArrived(t *testing.T) {
	one := drupal.Issue{Nid: 1, Files: []drupal.IssueFile{patch("a.patch", 100, 10)}}
	two := drupal.Issue{Nid: 1, Files: []drupal.IssueFile{
		patch("a.patch", 100, 10), patch("b.patch", 200, 10),
	}}

	if got := (Contribution{Issue: one}).DashboardStatus(); got != "PATCH 1 patch" {
		t.Errorf("got %q", got)
	}
	if got := (Contribution{Issue: two}).DashboardStatus(); got != "PATCH 2 patches" {
		t.Errorf("got %q", got)
	}
	if got := (Contribution{Issue: drupal.Issue{Nid: 1}}).DashboardStatus(); got != "PATCH nothing attached" {
		t.Errorf("got %q", got)
	}

	covered := Contribution{Issue: drupal.Issue{Nid: 1}, MergeRequests: []gitlab.MergeRequest{realMr(4, "t", "b")}}
	if got := covered.DashboardStatus(); !strings.HasPrefix(got, "PATCH covered by !4") {
		t.Errorf("got %q", got)
	}
}

// Nothing a cached result could be about.
func TestAnIssueWithNoPatchHasNoCurrentRevision(t *testing.T) {
	if got := (Contribution{Issue: drupal.Issue{Nid: 1}}).CurrentRevision(); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestTheCurrentRevisionIsTheNewestPatchs(t *testing.T) {
	issue := drupal.Issue{Nid: 1, Files: []drupal.IssueFile{
		patch("old.patch", 100, 10),
		patch("new.patch", 200, 10),
	}}

	contribution := Contribution{Issue: issue}
	if got := contribution.CurrentRevision(); got != Revision("https://www.drupal.org/files/issues/new.patch") {
		t.Errorf("got %q, want the newest patch's", got)
	}
}

package report

import (
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/dashboard"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/patches"
)

// Every kind maps to a token that can be compared with ==.
//
// Not patches.Kind.SummaryLabel, which is prose for a summary line and would
// make a consumer parse spaces. An unrecognised kind is named rather than
// silently becoming one of the real ones.
func TestEveryKindHasAToken(t *testing.T) {
	for kind, want := range map[patches.Kind]string{
		patches.PatchOnly:                  "patch-only",
		patches.PatchAndMergeRequest:       "patch-and-merge-request",
		patches.PatchWithEmptyMergeRequest: "patch-with-empty-merge-request",
		patches.MergeRequestOnly:           "merge-request-only",
		patches.Nothing:                    "nothing",
		patches.Kind(99):                   "unknown",
	} {
		if got := kindName(kind); got != want {
			t.Errorf("kind %d -> %q, want %q", kind, got, want)
		}
	}
}

// A row with no issue, no merge request and no verdict still produces a line.
//
// 33 of pathauto's 162 rows carry a merge request claiming no listed issue,
// and a patch-only row carries no merge request at all. Both must come back as
// nulls rather than zero values a consumer would read as real.
func TestARowWithNothingAttachedIsAllNulls(t *testing.T) {
	payload := Rows([]dashboard.Row{{Module: "pathauto", Branch: "8.x-1.x"}})

	if len(payload.Rows) != 1 {
		t.Fatalf("got %d rows", len(payload.Rows))
	}

	line := payload.Rows[0]
	if line.Issue != nil {
		t.Errorf("it invented an issue: %+v", line.Issue)
	}
	if line.MergeRequest != nil {
		t.Errorf("it invented a merge request: %+v", line.MergeRequest)
	}
	if line.Verdict != nil {
		t.Errorf("it invented a verdict: %+v", line.Verdict)
	}
	if line.Landed != nil {
		t.Errorf("it invented a landing: %+v", line.Landed)
	}
	if line.CI != nil {
		t.Errorf("it invented a pipeline: %+v", line.CI)
	}
	// Never nil, so a consumer can iterate without a check.
	if line.Local == nil {
		t.Error("local is null; it should be an empty object")
	}
	// And the row still says what to do, because every row does.
	if line.Next == "" {
		t.Error("the row carries no next command")
	}
}

// A row that knows only the issue's number says only that.
//
// The nid without a contribution is a real state: an issue reference upkeep
// could not pair to issue data. Reporting nid 0 would be worse than reporting
// the number alone.
func TestARowWithOnlyANidSaysOnlyTheNid(t *testing.T) {
	payload := Rows([]dashboard.Row{{Module: "pathauto", IssueNid: 3597857}})

	issue := payload.Rows[0].Issue
	if issue == nil {
		t.Fatal("the nid was dropped")
	}
	if issue.Nid != 3597857 {
		t.Errorf("nid %d", issue.Nid)
	}
	if issue.Title != "" {
		t.Errorf("it invented a title: %q", issue.Title)
	}
}

// Failures that stopped a read are named, and kept apart from absence.
//
// "no pipeline" and "could not ask about the pipeline" want different
// reactions, which is why they are different fields rather than one null.
func TestUnreadableCIAndModuleAreNamedSeparately(t *testing.T) {
	payload := Rows([]dashboard.Row{{
		Module:        "pathauto",
		CIFailure:     &gitlab.Failure{Kind: gitlab.RateLimited},
		ModuleFailure: &gitlab.Failure{Kind: gitlab.NotFound},
	}})

	line := payload.Rows[0]
	if line.CI != nil {
		t.Error("an unreadable pipeline was reported as a pipeline")
	}
	if line.CIUnavailable == "" {
		t.Error("the CI failure is not named")
	}
	if line.ModuleUnavailable == "" {
		t.Error("the module failure is not named")
	}
}

// A pipeline with no raw status still reports one, from the parsed status.
func TestAPipelineWithoutARawStatusStillReportsOne(t *testing.T) {
	if ref := pipelineRef(nil); ref != nil {
		t.Errorf("nil became %+v", ref)
	}

	ref := pipelineRef(&gitlab.Pipeline{Status: gitlab.StatusSuccess})
	if ref == nil {
		t.Fatal("a pipeline came back nil")
	}
	if ref.Status == "" {
		t.Error("the status is empty")
	}
	if !ref.Green {
		t.Error("a successful pipeline is not green")
	}
}

// A module with no snapshot reports no cache time rather than the zero time,
// which would read as 1970.
func TestAModuleWithNoSnapshotHasNoCacheTime(t *testing.T) {
	rows := []dashboard.Row{{Module: "pathauto", Branch: "8.x-1.x"}}

	withNone := Overview(rows, nil)
	if withNone.Modules[0].CachedAt != nil {
		t.Errorf("it invented a cache time: %v", withNone.Modules[0].CachedAt)
	}

	// A snapshot whose time is zero is the same answer: unknown, not 1970.
	withZero := Overview(rows, map[string]dashboard.ModuleSnapshot{"pathauto": {}})
	if withZero.Modules[0].CachedAt != nil {
		t.Errorf("a zero snapshot time became %v", withZero.Modules[0].CachedAt)
	}

	at := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	withOne := Overview(rows, map[string]dashboard.ModuleSnapshot{"pathauto": {FetchedAt: at}})
	if withOne.Modules[0].CachedAt == nil || !withOne.Modules[0].CachedAt.Equal(at) {
		t.Errorf("the cache time did not survive: %v", withOne.Modules[0].CachedAt)
	}
}

// Branches are an array even when a module has none.
func TestBranchesAreAlwaysAnArray(t *testing.T) {
	payload := Overview([]dashboard.Row{{Module: "pathauto"}}, nil)

	if payload.Modules[0].Branches == nil {
		t.Error("branches is null")
	}
}

// Contributions works without a next-command function, because `patches` has
// no NEXT column to agree with.
func TestContributionsWithoutANextCommand(t *testing.T) {
	payload := Contributions([]patches.Contribution{{
		Module: "pathauto",
		Issue:  drupal.Issue{Nid: 1, Title: "t", Status: drupal.StatusActive},
	}}, nil)

	if len(payload.Issues) != 1 {
		t.Fatalf("got %d", len(payload.Issues))
	}
	if payload.Issues[0].Next != "" {
		t.Errorf("it invented a next command: %q", payload.Issues[0].Next)
	}
	if payload.Issues[0].MergeRequests == nil {
		t.Error("merge_requests is null; it should be an empty array")
	}
}

// An envelope always carries an array of warnings, so a consumer need not
// check for null before iterating.
func TestTheEnvelopeNeverCarriesNullWarnings(t *testing.T) {
	envelope := NewEnvelope("1.0.0", "issues", ContributionsPayload{}, nil)

	if envelope.Warnings == nil {
		t.Error("warnings is null")
	}
	if envelope.Schema != Schema {
		t.Errorf("schema %d", envelope.Schema)
	}
	if envelope.Upkeep != "1.0.0" || envelope.Command != "issues" {
		t.Errorf("got %+v", envelope)
	}
}

// A landing comes through on a row, with whether anything has happened since.
//
// The fact an open row can carry: bot compatibility issues are kept open by
// convention so the bot can post again, so an open issue may have had its real
// work merge months ago. NewerWork is what separates "this is done" from "this
// landed and then moved on", and neither is a statement upkeep turns into a
// status change.
func TestALandingAndWhetherAnythingFollowedIt(t *testing.T) {
	landed := gitlab.MergeRequest{
		IID:      99,
		MergedAt: "2026-06-12T09:00:00Z",
		WebURL:   "https://git.drupalcode.org/project/pathauto/-/merge_requests/99",
	}

	payload := Rows([]dashboard.Row{{
		Module:                "pathauto",
		Landed:                &landed,
		NewerWorkSinceLanding: true,
	}})

	line := payload.Rows[0].Landed
	if line == nil {
		t.Fatal("the landing was dropped")
	}
	if line.IID != 99 || line.MergedAt != landed.MergedAt || line.WebURL != landed.WebURL {
		t.Errorf("got %+v", line)
	}
	if !line.NewerWork {
		t.Error("newer work was not reported")
	}
}

// And on a contribution, where the landing is derived from the merged merge
// requests rather than handed over.
func TestAContributionReportsItsLanding(t *testing.T) {
	payload := Contributions([]patches.Contribution{{
		Module: "pathauto",
		Issue:  drupal.Issue{Nid: 3596502, Title: "t", Status: drupal.StatusActive},
		MergeRequests: []gitlab.MergeRequest{{
			IID: 99, State: "merged", MergedAt: "2026-06-12T09:00:00Z",
		}},
	}}, nil)

	landing := payload.Issues[0].Landed
	if landing == nil {
		t.Fatal("the landing was not derived")
	}
	if landing.IID != 99 {
		t.Errorf("got %+v", landing)
	}
}

// Rows from several modules are grouped, and the module order is the order
// they first appeared rather than anything sorted behind the caller's back.
func TestOverviewGroupsByModuleInFirstSeenOrder(t *testing.T) {
	payload := Overview([]dashboard.Row{
		{Module: "pathauto", Branch: "8.x-1.x"},
		{Module: "helper", Branch: "2.x"},
		{Module: "pathauto", Branch: "2.0.x"},
	}, nil)

	if len(payload.Modules) != 2 {
		t.Fatalf("got %d modules", len(payload.Modules))
	}
	if payload.Modules[0].Module != "pathauto" || payload.Modules[1].Module != "helper" {
		t.Errorf("order: %s, %s", payload.Modules[0].Module, payload.Modules[1].Module)
	}
	if len(payload.Modules[0].Branches) != 2 {
		t.Errorf("pathauto's branches: %v", payload.Modules[0].Branches)
	}
}

package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gate"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/patches"
	"github.com/owenbush/upkeep/internal/results"
)

// The column order the dashboard renders, so a cell moving is a test failure
// rather than a surprise in the terminal.
const (
	colModule = iota
	colIssue
	colVersion
	colTitle
	colMR
	colPatch
	colCI
	colLocal
	colStatus
	colNext
)

func TestTheTableHasTenColumnsInOrder(t *testing.T) {
	row := rowWith(gate.Verdict{Status: gate.ReadyAuto}, openMr(12), greenEvidence("11"))

	cells := row.TableCells(false)
	if len(cells) != 10 {
		t.Fatalf("got %d cells: %v", len(cells), cells)
	}
	if cells[colModule] != "pathauto" {
		t.Errorf("module cell %q", cells[colModule])
	}
	if cells[colVersion] != "2.0.x" {
		t.Errorf("version cell %q", cells[colVersion])
	}
	if cells[colNext] != "upkeep merge --fast-lane" {
		t.Errorf("next cell %q", cells[colNext])
	}
}

// "#3598272 review" and "#3598272 RTBC" call for different things from a
// maintainer, and the dashboard used to print the same cell for both.
func TestTheIssueCellCarriesDrupalOrgsOwnStatusWord(t *testing.T) {
	for status, want := range map[drupal.IssueStatus]string{
		drupal.StatusNeedsReview:     "3603341 review",
		drupal.StatusRtbc:            "3603341 RTBC",
		drupal.StatusNeedsWork:       "3603341 needs work",
		drupal.StatusActive:          "3603341 active",
		drupal.StatusClosedFixed:     "3603341 closed",
		drupal.StatusPostponed:       "3603341 postponed",
		drupal.StatusPatchToBePorted: "3603341 to port",
	} {
		issue := issueWithPatches(3603341, 0)
		issue.Status = status
		contribution := patches.Contribution{Module: "pathauto", Issue: issue}
		row := RowForIssue("pathauto", "2.0.x", contribution, nil, nil, results.NoEvidence(), nil, nil)

		if got := row.IssueCell(); got != want {
			t.Errorf("status %d: got %q, want %q", status, got, want)
		}
	}
}

// 20% of pathauto's merge requests claim no issue at all, and one that claims
// one outside the open queue is shown but not paired.
func TestAnUnlinkedMergeRequestKeepsARowOfItsOwn(t *testing.T) {
	mr := openMr(42)
	mr.Title = "Something nobody filed an issue for"

	row := RowForUnlinkedMergeRequest("pathauto", "2.0.x", nil, mr,
		results.NoEvidence(), gate.Verdict{Status: gate.Review, Reasons: []string{"local-missing"}}, nil, 0)

	if row.IssueCell() != "–" {
		t.Errorf("issue cell %q", row.IssueCell())
	}
	if row.TitleCell() != mr.Title {
		t.Errorf("title cell %q, want the merge request's", row.TitleCell())
	}
	if row.PatchCell() != "–" {
		t.Errorf("patch cell %q — an unpaired row has no patch count", row.PatchCell())
	}
	if GuidanceFor(row).Command == "" {
		t.Error("no command")
	}
}

// Shown, but not paired: without the issue there is no status, no patch count
// and nothing to group by.
func TestAClaimedButUnpairedIssueIsShownWithoutAStatus(t *testing.T) {
	row := RowForUnlinkedMergeRequest("pathauto", "2.0.x", nil, openMr(42),
		results.NoEvidence(), gate.Verdict{Status: gate.Review}, nil, 3597808)

	if row.IssueCell() != "3597808" {
		t.Errorf("issue cell %q", row.IssueCell())
	}
	if strings.Contains(row.IssueCell(), "review") {
		t.Errorf("issue cell %q claims a status it has no issue to read", row.IssueCell())
	}
}

// A module whose merge requests cannot be listed still gets a visible row,
// rather than vanishing from the dashboard.
func TestAModuleFailureRowIsVisibleAndSaysWhy(t *testing.T) {
	row := RowForModuleFailure("pathauto", &gitlab.Failure{
		Kind: gitlab.EndpointClosed, Status: 403, Message: "Endpoint closed to API access (HTTP 403).",
	})

	cells := row.TableCells(false)
	if len(cells) != 10 {
		t.Fatalf("got %d cells", len(cells))
	}
	if cells[colCI] != "n/a (403)" || cells[colStatus] != "n/a (403)" {
		t.Errorf("CI %q, status %q", cells[colCI], cells[colStatus])
	}
	if cells[colTitle] != "(merge requests unavailable)" {
		t.Errorf("title cell %q", cells[colTitle])
	}
	if row.IsReadyAuto() {
		t.Error("a failure row read as ready to merge")
	}
}

// A row with no open merge request carries no verdict, which is what makes
// this false by construction rather than by a check someone has to remember.
func TestAPatchOnlyRowIsNotReadyByConstruction(t *testing.T) {
	contribution := patches.Contribution{Module: "pathauto", Issue: issueWithPatches(3603341, 2)}
	row := RowForIssue("pathauto", "2.0.x", contribution, nil, nil, greenEvidence("11"), nil, nil)

	if row.IsReadyAuto() {
		t.Error("a patch-only row read as ready to merge")
	}
	if row.Verdict != nil {
		t.Error("a patch-only row carries a gate verdict")
	}
}

// The merge command feeds the result straight into a merge call, so this is a
// real guard rather than an assertion.
func TestRequiringAMergeRequestOnARowThatHasNoneRefuses(t *testing.T) {
	contribution := patches.Contribution{Module: "pathauto", Issue: issueWithPatches(3603341, 1)}
	row := RowForIssue("pathauto", "2.0.x", contribution, nil, nil, results.NoEvidence(), nil, nil)

	if _, err := row.RequireMergeRequest(); err == nil {
		t.Error("a patch-only row handed back a merge request")
	}
	if _, err := row.RequireProject(); err == nil {
		t.Error("a patch-only row handed back a project")
	}
	if _, err := row.RequireVerdict(); err == nil {
		t.Error("a patch-only row handed back a verdict")
	}
}

func TestRequiringAMergeRequestOnARowThatHasOneWorks(t *testing.T) {
	project := gitlab.Project{ID: 11, PathWithNamespace: "project/pathauto"}
	mr := openMr(12)
	contribution := patches.Contribution{
		Module: "pathauto", Issue: issueWithPatches(3603341, 0),
		MergeRequests: []gitlab.MergeRequest{mr},
	}
	row := RowForIssue("pathauto", "2.0.x", contribution, &project, &mr,
		greenEvidence("11"), &gate.Verdict{Status: gate.ReadyAuto}, nil)

	got, err := row.RequireMergeRequest()
	if err != nil || got.IID != 12 {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := row.RequireProject(); err != nil {
		t.Errorf("project: %v", err)
	}
	if _, err := row.RequireVerdict(); err != nil {
		t.Errorf("verdict: %v", err)
	}
}

// patch↑ used to be a cross-reference between two rows that no glossary
// explained. Here the patch and the merge request are the same row.
func TestThePatchCellFlagsARerollTheBranchDoesNotCarry(t *testing.T) {
	mr := openMr(12)
	mr.UpdatedAt = "2026-01-01T00:00:00Z"

	issue := issueWithPatches(3603341, 1)
	issue.Files[0].Timestamp = 1800000000 // well after the merge request moved

	contribution := patches.Contribution{
		Module: "pathauto", Issue: issue, MergeRequests: []gitlab.MergeRequest{mr},
	}
	row := RowForIssue("pathauto", "2.0.x", contribution, nil, &mr, results.NoEvidence(),
		&gate.Verdict{Status: gate.Review}, nil)

	if row.PatchCell() != "1 ↑" {
		t.Errorf("patch cell %q", row.PatchCell())
	}
}

func TestAPatchOlderThanTheBranchIsNotFlagged(t *testing.T) {
	mr := openMr(12)
	mr.UpdatedAt = "2026-09-01T00:00:00Z"

	issue := issueWithPatches(3603341, 1)
	issue.Files[0].Timestamp = 1000

	contribution := patches.Contribution{
		Module: "pathauto", Issue: issue, MergeRequests: []gitlab.MergeRequest{mr},
	}
	row := RowForIssue("pathauto", "2.0.x", contribution, nil, &mr, results.NoEvidence(),
		&gate.Verdict{Status: gate.Review}, nil)

	if row.PatchCell() != "1" {
		t.Errorf("patch cell %q", row.PatchCell())
	}
}

func TestTheCiCellReportsWhatThePipelineSaid(t *testing.T) {
	for raw, want := range map[string]string{
		"success": "pass",
		"failed":  "fail",
		"running": "running",
		"manual":  "manual",
	} {
		mr := openMr(12)
		mr.HeadPipeline = &gitlab.Pipeline{Status: gitlab.PipelineStatusFrom(raw), RawStatus: raw}
		row := rowWith(gate.Verdict{Status: gate.Review}, mr, results.NoEvidence())

		if got := row.CICell(); got != want {
			t.Errorf("%s: got %q, want %q", raw, got, want)
		}
	}
}

func TestNoPipelineIsADashAndAFailureIsSaidExplicitly(t *testing.T) {
	none := rowWith(gate.Verdict{Status: gate.Review}, openMr(12), results.NoEvidence())
	if got := none.CICell(); got != "–" {
		t.Errorf("got %q", got)
	}

	mr := openMr(12)
	contribution := patches.Contribution{
		Module: "pathauto", Issue: issueWithPatches(3603341, 0),
		MergeRequests: []gitlab.MergeRequest{mr},
	}
	failed := RowForIssue("pathauto", "2.0.x", contribution, nil, &mr, results.NoEvidence(),
		&gate.Verdict{Status: gate.Review},
		&gitlab.Failure{Kind: gitlab.RateLimited, Status: 429})

	if got := failed.CICell(); got != "n/a (rate-limited)" {
		t.Errorf("got %q", got)
	}
}

// Verbose swaps the phrase for the gate's tokens and the LOCAL cell for every
// core's own state, so anything scripted against them still has them.
func TestVerboseSwapsThePhraseForTheGatesOwnVocabulary(t *testing.T) {
	verdict := gate.Verdict{Status: gate.Review, Reasons: []string{"not-bot-author", "local-missing"}}
	row := rowWith(verdict, openMr(12), results.Evidence(map[string]*results.CachedResult{
		"10": nil,
		"11": {SHA: currentSHA, RecordedAt: time.Now(), Result: check.RunResult{Results: []check.Result{
			{Type: check.PhpUnit, Status: check.Passed},
		}}},
	}, currentSHA))

	plain := row.TableCells(false)
	verbose := row.TableCells(true)

	if plain[colStatus] == verbose[colStatus] {
		t.Errorf("verbose status is the same phrase: %q", plain[colStatus])
	}
	if verbose[colStatus] != "REVIEW not-bot-author, local-missing" {
		t.Errorf("verbose status %q", verbose[colStatus])
	}
	if verbose[colLocal] != "10:unchecked 11:pass" {
		t.Errorf("verbose local %q", verbose[colLocal])
	}
	if plain[colLocal] != "pass 11 · ? 10" {
		t.Errorf("plain local %q", plain[colLocal])
	}
	// The command is the same either way — it is not vocabulary.
	if plain[colNext] != verbose[colNext] {
		t.Errorf("command differs under -v: %q vs %q", plain[colNext], verbose[colNext])
	}
}

// Counted in runes, not bytes: a title with an accented character must not
// lose a byte and become invalid UTF-8 in the middle of a table.
func TestATitleIsTruncatedByRunes(t *testing.T) {
	long := strings.Repeat("é", 60)

	got := Truncate(long, 0)
	if len([]rune(got)) != 44 {
		t.Errorf("got %d runes, want 44", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("got %q", got)
	}
	if !utf8Valid(got) {
		t.Errorf("truncation produced invalid UTF-8: %q", got)
	}

	short := "Drupal 12 compatibility"
	if Truncate(short, 0) != short {
		t.Errorf("a short title was truncated: %q", Truncate(short, 0))
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}

	return true
}

// A status GitLab adds later is still reportable as what the server said,
// rather than flattened to "unknown" in the one column a maintainer would
// look at to find out.
func TestAnUnrecognisedPipelineStatusIsPrintedAsTheServerSaidIt(t *testing.T) {
	mr := openMr(12)
	mr.HeadPipeline = &gitlab.Pipeline{Status: gitlab.StatusUnknown, RawStatus: "quantum_pending"}
	row := rowWith(gate.Verdict{Status: gate.Review}, mr, results.NoEvidence())

	if got := row.CICell(); got != "quantum_pending" {
		t.Errorf("got %q, want what the server said", got)
	}
}

// And with nothing to quote, the mapped status is the fallback rather than an
// empty cell.
func TestAPipelineWithNoStatusAtAllStillFillsTheCell(t *testing.T) {
	mr := openMr(12)
	mr.HeadPipeline = &gitlab.Pipeline{Status: gitlab.StatusUnknown}
	row := rowWith(gate.Verdict{Status: gate.Review}, mr, results.NoEvidence())

	if got := row.CICell(); got != "unknown" {
		t.Errorf("got %q", got)
	}
}

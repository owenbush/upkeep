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

const currentSHA = "abc1234"

func openMr(iid int) gitlab.MergeRequest {
	return gitlab.MergeRequest{
		IID: iid, State: "opened", Title: "Automated Drupal 12 compatibility fixes",
		SourceBranch: "project-update-bot-only", TargetBranch: "2.0.x",
		DiffBaseSHA: "base", DiffHeadSHA: "head",
	}
}

func issueWithPatches(nid, count int) drupal.Issue {
	issue := drupal.Issue{Nid: nid, Title: "Drupal 12 compatibility", Status: drupal.StatusNeedsReview}
	for i := range count {
		issue.Files = append(issue.Files, drupal.IssueFile{
			Name:      "3603341-" + string(rune('1'+i)) + "-d12.patch",
			URL:       "https://www.drupal.org/files/issues/p" + string(rune('1'+i)) + ".patch",
			Timestamp: int64(100 + i),
		})
	}

	return issue
}

func greenEvidence(cores ...string) results.LocalEvidence {
	byCore := map[string]*results.CachedResult{}
	for _, core := range cores {
		byCore[core] = &results.CachedResult{
			SHA: currentSHA, RecordedAt: time.Now(),
			Result: check.RunResult{Results: []check.Result{{Type: check.PhpUnit, Status: check.Passed}}},
		}
	}

	return results.Evidence(byCore, currentSHA)
}

func rowWith(verdict gate.Verdict, mr gitlab.MergeRequest, local results.LocalEvidence) Row {
	contribution := patches.Contribution{
		Module: "pathauto", Issue: issueWithPatches(3603341, 0),
		MergeRequests: []gitlab.MergeRequest{mr},
	}

	return RowForIssue("pathauto", "2.0.x", contribution, nil, &mr, local, &verdict, nil)
}

// A maintainer looking at a hundred rows needs to know which one to touch and
// what to type. There is no row this tool has nothing to say about — and the
// two that briefly did, red CI and drafts, were the ones a maintainer most
// wanted a way into.
//
// This is the property, over every subset of the gate's reasons.
func TestEveryRowYieldsACommand(t *testing.T) {
	allReasons := []string{
		"not-bot-author", "no-changes", "draft", "ci-missing", "ci-red",
		"ci-not-green:running", "local-missing", "local-stale",
		"local-failed:phpunit", "local-failed:phpcs",
	}

	for subset := range 1 << len(allReasons) {
		reasons := []string{}
		for i, reason := range allReasons {
			if subset&(1<<i) != 0 {
				reasons = append(reasons, reason)
			}
		}

		for _, status := range []gate.Status{gate.ReadyAuto, gate.Review, gate.Blocked} {
			row := rowWith(gate.Verdict{Status: status, Reasons: reasons}, openMr(12), greenEvidence("11"))
			guidance := GuidanceFor(row)

			if guidance.Command == "" {
				t.Fatalf("no command for %s %v", status, reasons)
			}
			if !strings.HasPrefix(guidance.Command, "upkeep ") {
				t.Fatalf("command %q for %s %v is not a command", guidance.Command, status, reasons)
			}
			if guidance.Status == "" {
				t.Fatalf("no status for %s %v", status, reasons)
			}
		}
	}
}

// And the same for a row with no merge request at all.
func TestAPatchOnlyRowAlwaysYieldsACommand(t *testing.T) {
	for _, patchCount := range []int{0, 1, 3} {
		for _, local := range []results.LocalEvidence{
			results.NoEvidence(),
			greenEvidence("11"),
			results.Evidence(map[string]*results.CachedResult{"11": nil}, currentSHA),
			results.Evidence(map[string]*results.CachedResult{
				"11": {SHA: "older", RecordedAt: time.Now(), Result: check.RunResult{}},
			}, currentSHA),
		} {
			contribution := patches.Contribution{
				Module: "pathauto", Issue: issueWithPatches(3603341, patchCount),
			}
			row := RowForIssue("pathauto", "2.0.x", contribution, nil, nil, local, nil, nil)

			guidance := GuidanceFor(row)
			if guidance.Command == "" || guidance.Status == "" {
				t.Fatalf("patch count %d, evidence %v: got %+v", patchCount, local.Cell(), guidance)
			}
		}
	}
}

// Applying an empty merge request changes nothing, so the checks would run
// against the branch as it already stands and the verdict would be reported as
// though it were about the contribution.
//
// Reported from a real dashboard, where a row showed "!1 empty" and "1" patch
// side by side and then said to check the merge request.
func TestAnEmptyMergeRequestWithAPatchSendsYouToThePatch(t *testing.T) {
	empty := openMr(1)
	empty.DiffBaseSHA, empty.DiffHeadSHA = "same", "same"

	contribution := patches.Contribution{
		Module: "pathauto", Issue: issueWithPatches(3603341, 1),
		MergeRequests: []gitlab.MergeRequest{empty},
	}
	row := RowForIssue("pathauto", "2.0.x", contribution, nil, &empty,
		results.NoEvidence(), &gate.Verdict{Status: gate.Review, Reasons: []string{"no-changes"}}, nil)

	guidance := GuidanceFor(row)
	if !strings.HasPrefix(guidance.Command, "upkeep patch:check pathauto 3603341") {
		t.Errorf("command %q, want the patch", guidance.Command)
	}
	if strings.Contains(guidance.Command, "upkeep check ") {
		t.Errorf("command %q would check a merge request that carries nothing", guidance.Command)
	}
	if !strings.Contains(guidance.Status, "empty MR") {
		t.Errorf("status %q does not flag that the row looks covered and is not", guidance.Status)
	}
}

// With no patch there is simply nothing to check yet, and the honest move is
// to go and look at the issue rather than to name a command that would do
// nothing.
func TestAnEmptyMergeRequestWithNoPatchSendsYouToTheIssue(t *testing.T) {
	empty := openMr(1)
	empty.DiffBaseSHA, empty.DiffHeadSHA = "same", "same"

	row := rowWith(gate.Verdict{Status: gate.Review, Reasons: []string{"no-changes"}}, empty, results.NoEvidence())

	guidance := GuidanceFor(row)
	if guidance.Command != "upkeep issue pathauto 1" {
		t.Errorf("command %q", guidance.Command)
	}
	if guidance.Status != "empty MR, nothing to check" {
		t.Errorf("status %q", guidance.Status)
	}
}

// Unknown emptiness is the reading a list payload gives, and no caller may
// read it as empty.
func TestUnsettledEmptinessIsNotTreatedAsEmpty(t *testing.T) {
	unknown := openMr(1)
	unknown.DiffBaseSHA, unknown.DiffHeadSHA = "", ""

	row := rowWith(gate.Verdict{Status: gate.ReadyAuto}, unknown, greenEvidence("11"))

	if got := GuidanceFor(row).Status; got != "ready to merge" {
		t.Errorf("status %q — an unknown was read as empty", got)
	}
}

func TestAReadyRowSaysSoAndNamesTheFastLane(t *testing.T) {
	guidance := GuidanceFor(rowWith(gate.Verdict{Status: gate.ReadyAuto}, openMr(12), greenEvidence("11")))

	if guidance.Status != "ready to merge" || guidance.Command != "upkeep merge --fast-lane" {
		t.Errorf("got %+v", guidance)
	}
}

// "phpcs failed" is actionable in a way "1 check failed" is not, and it is
// *your* evidence — worth telling the contributor about.
func TestAFailedCheckIsNamedAndRoutesToNeedsWork(t *testing.T) {
	one := GuidanceFor(rowWith(
		gate.Verdict{Status: gate.Review, Reasons: []string{"local-failed:phpcs"}},
		openMr(12), greenEvidence("11"),
	))
	if one.Status != "phpcs failed" {
		t.Errorf("status %q", one.Status)
	}
	if one.Command != "upkeep needs-work pathauto 12" {
		t.Errorf("command %q", one.Command)
	}

	two := GuidanceFor(rowWith(
		gate.Verdict{Status: gate.Review, Reasons: []string{"local-failed:phpcs", "local-failed:phpstan"}},
		openMr(12), greenEvidence("11"),
	))
	if two.Status != "2 checks failed" {
		t.Errorf("status %q", two.Status)
	}
}

// Red CI and draft are modifiers: they change what the row is, not what to do
// about it. A red pipeline is when you most want the branch on your own
// machine.
func TestRedCiAndDraftStillNameSomethingToRun(t *testing.T) {
	red := GuidanceFor(rowWith(
		gate.Verdict{Status: gate.Blocked, Reasons: []string{"ci-red", "local-missing"}},
		openMr(12), results.NoEvidence(),
	))
	if red.Status != "CI failed" {
		t.Errorf("status %q", red.Status)
	}
	if !strings.HasPrefix(red.Command, "upkeep check pathauto 12") {
		t.Errorf("command %q", red.Command)
	}

	draft := GuidanceFor(rowWith(
		gate.Verdict{Status: gate.Review, Reasons: []string{"draft", "local-missing"}},
		openMr(12), results.NoEvidence(),
	))
	if !strings.HasPrefix(draft.Status, "draft, ") {
		t.Errorf("status %q", draft.Status)
	}
	if draft.Command == "" {
		t.Error("a draft named nothing to run")
	}
}

// Checked and green, and the fast lane will not take it: your checks disagree
// with drupal.org's, or it is not a bot merge request. Either way it is a
// thing to go and look at.
func TestGreenButNotReadyRoutesToReview(t *testing.T) {
	guidance := GuidanceFor(rowWith(
		gate.Verdict{Status: gate.Review, Reasons: []string{"not-bot-author"}},
		openMr(12), greenEvidence("11"),
	))

	if guidance.Status != "needs your review" {
		t.Errorf("status %q", guidance.Status)
	}
	if guidance.Command != "upkeep review pathauto 12" {
		t.Errorf("command %q", guidance.Command)
	}
}

func TestRedCiWithGreenLocalSaysTheDisagreement(t *testing.T) {
	guidance := GuidanceFor(rowWith(
		gate.Verdict{Status: gate.Blocked, Reasons: []string{"ci-red", "not-bot-author"}},
		openMr(12), greenEvidence("11"),
	))

	if guidance.Status != "CI failed, local green" {
		t.Errorf("status %q", guidance.Status)
	}
}

// The table and the command cannot disagree about which core is the problem.
func TestTheCommandNamesTheCoreTheCellNames(t *testing.T) {
	failing := results.Evidence(map[string]*results.CachedResult{
		"10": {SHA: currentSHA, RecordedAt: time.Now(), Result: check.RunResult{Results: []check.Result{
			{Type: check.PhpCs, Status: check.Failed},
		}}},
		"11": {SHA: currentSHA, RecordedAt: time.Now(), Result: check.RunResult{Results: []check.Result{
			{Type: check.PhpUnit, Status: check.Passed},
		}}},
	}, currentSHA)

	row := rowWith(gate.Verdict{Status: gate.Review, Reasons: []string{"local-missing"}}, openMr(12), failing)

	if !strings.HasSuffix(GuidanceFor(row).Command, "--version=10") {
		t.Errorf("command %q does not name the core the cell does (%q)", GuidanceFor(row).Command, row.LocalCell())
	}
}

// A row whose evidence is green everywhere has nothing to re-run, so no core
// is named.
func TestAGreenRowNamesNoCore(t *testing.T) {
	row := rowWith(gate.Verdict{Status: gate.Review, Reasons: []string{"not-bot-author"}},
		openMr(12), greenEvidence("10", "11"))

	if strings.Contains(GuidanceFor(row).Command, "--version") {
		t.Errorf("command %q names a core with nothing wrong with it", GuidanceFor(row).Command)
	}
}

// An open issue whose work is already merged is the one thing a maintainer
// cannot read off the row at all.
func TestALandingOutranksEverythingElseTheRowCouldSay(t *testing.T) {
	landed := openMr(7)
	landed.State = "merged"
	landed.MergedAt = "2026-06-12T10:00:00Z"
	open := openMr(12)

	contribution := patches.Contribution{
		Module: "pathauto", Issue: issueWithPatches(3603341, 0),
		MergeRequests: []gitlab.MergeRequest{open, landed},
	}
	row := RowForIssue("pathauto", "2.0.x", contribution, nil, &open, results.NoEvidence(),
		&gate.Verdict{Status: gate.Review, Reasons: []string{"local-missing", "draft"}}, nil)

	guidance := GuidanceFor(row)
	if guidance.Status != "merged 2026-06-12" {
		t.Errorf("status %q", guidance.Status)
	}
	if guidance.Command != "upkeep issue pathauto 7" {
		t.Errorf("command %q", guidance.Command)
	}
}

// A bot that posts again after its work merged has raised new work to check.
func TestNewerWorkSinceALandingRoutesToTheCheck(t *testing.T) {
	landed := openMr(7)
	landed.State = "merged"
	landed.MergedAt = "2026-01-01T00:00:00Z"

	issue := issueWithPatches(3603341, 1)
	issue.Files[0].Timestamp = 1800000000 // long after the merge

	contribution := patches.Contribution{
		Module: "pathauto", Issue: issue, MergeRequests: []gitlab.MergeRequest{landed},
	}
	row := RowForIssue("pathauto", "2.0.x", contribution, nil, nil, results.NoEvidence(), nil, nil)

	guidance := GuidanceFor(row)
	if guidance.Status != "merged 2026-01-01, newer work since" {
		t.Errorf("status %q", guidance.Status)
	}
	if !strings.HasPrefix(guidance.Command, "upkeep patch:check pathauto 3603341") {
		t.Errorf("command %q", guidance.Command)
	}
}

// A failing patch is work to pick up rather than a verdict to deliver.
func TestAFailingPatchRoutesToStart(t *testing.T) {
	failing := results.Evidence(map[string]*results.CachedResult{
		"11": {SHA: currentSHA, RecordedAt: time.Now(), Result: check.RunResult{Results: []check.Result{
			{Type: check.PhpUnit, Status: check.Failed},
		}}},
	}, currentSHA)

	contribution := patches.Contribution{Module: "pathauto", Issue: issueWithPatches(3603341, 1)}
	row := RowForIssue("pathauto", "2.0.x", contribution, nil, nil, failing, nil, nil)

	guidance := GuidanceFor(row)
	if guidance.Status != "1 patch, failed" {
		t.Errorf("status %q", guidance.Status)
	}
	if guidance.Command != "upkeep start pathauto 3603341" {
		t.Errorf("command %q", guidance.Command)
	}
}

// Stricter than it reads: a patch checked on 11 and never checked on 10 is not
// "checked".
func TestAPatchGreenOnlyWhereCheckedIsNotChecked(t *testing.T) {
	partial := results.Evidence(map[string]*results.CachedResult{
		"10": nil,
		"11": {SHA: currentSHA, RecordedAt: time.Now(), Result: check.RunResult{Results: []check.Result{
			{Type: check.PhpUnit, Status: check.Passed},
		}}},
	}, currentSHA)

	contribution := patches.Contribution{Module: "pathauto", Issue: issueWithPatches(3603341, 1)}
	row := RowForIssue("pathauto", "2.0.x", contribution, nil, nil, partial, nil, nil)

	if got := GuidanceFor(row).Status; strings.Contains(got, "checked") {
		t.Errorf("status %q reads as checked on evidence covering half the cores", got)
	}
}

func TestACheckedPatchRoutesToApply(t *testing.T) {
	contribution := patches.Contribution{Module: "pathauto", Issue: issueWithPatches(3603341, 2)}
	row := RowForIssue("pathauto", "2.0.x", contribution, nil, nil, greenEvidence("11"), nil, nil)

	guidance := GuidanceFor(row)
	if guidance.Status != "2 patches, checked" {
		t.Errorf("status %q", guidance.Status)
	}
	if guidance.Command != "upkeep patch:apply pathauto 3603341" {
		t.Errorf("command %q", guidance.Command)
	}
}

// A module whose merge requests cannot be listed still gets a way back in.
func TestAModuleFailureRoutesToARefresh(t *testing.T) {
	row := RowForModuleFailure("pathauto", &gitlab.Failure{
		Kind: gitlab.EndpointClosed, Status: 403, Message: "Endpoint closed to API access (HTTP 403).",
	})

	guidance := GuidanceFor(row)
	if guidance.Status != "unavailable" {
		t.Errorf("status %q", guidance.Status)
	}
	if guidance.Command != "upkeep dashboard --refresh=pathauto" {
		t.Errorf("command %q", guidance.Command)
	}
}

// BLOCKED is what the gate sets from red CI and from nothing else, so the
// guidance reads the status as well as the reason token rather than depending
// on the gate always recording both.
func TestBlockedAloneStillReadsAsRedCi(t *testing.T) {
	row := rowWith(
		gate.Verdict{Status: gate.Blocked, Reasons: []string{"local-missing"}},
		openMr(12), results.NoEvidence(),
	)

	if got := GuidanceFor(row).Status; got != "CI failed" {
		t.Errorf("status %q — BLOCKED without the reason token was not read as red CI", got)
	}
}

// Evidence about an older revision is evidence about another tree, and saying
// so is different from saying there is none.
func TestStaleEvidenceAloneSaysTheChecksAreStale(t *testing.T) {
	stale := results.Evidence(map[string]*results.CachedResult{
		"11": {SHA: "older11", RecordedAt: time.Now(), Result: check.RunResult{}},
	}, currentSHA)

	row := rowWith(
		gate.Verdict{Status: gate.Review, Reasons: []string{"not-bot-author", "local-stale"}},
		openMr(12), stale,
	)

	guidance := GuidanceFor(row)
	if guidance.Status != "checks are stale" {
		t.Errorf("status %q", guidance.Status)
	}
	if !strings.HasPrefix(guidance.Command, "upkeep check pathauto 12") {
		t.Errorf("command %q", guidance.Command)
	}
	// And it names the stale core, because that is the one to re-run.
	if !strings.HasSuffix(guidance.Command, "--version=11") {
		t.Errorf("command %q does not name the stale core", guidance.Command)
	}
}

// Nothing checked anywhere means no core needs attention more than another, so
// the command carries no --version at all rather than an empty one.
func TestACheckCommandWithNothingToReRunNamesNoCore(t *testing.T) {
	contribution := patches.Contribution{Module: "pathauto", Issue: issueWithPatches(3603341, 1)}
	row := RowForIssue("pathauto", "2.0.x", contribution, nil, nil, results.NoEvidence(), nil, nil)

	if got := GuidanceFor(row).Command; got != "upkeep patch:check pathauto 3603341" {
		t.Errorf("command %q", got)
	}
}

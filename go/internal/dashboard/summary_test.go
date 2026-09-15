package dashboard

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gate"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
)

// The overview column order, so a cell moving is a failure rather than a
// surprise.
const (
	sumModule = iota
	sumBranches
	sumMRs
	sumPatchIssues
	sumReady
	sumCIFailed
	sumUnchecked
	sumCached
)

// A module whose overview says three READY-AUTO shows three READY-AUTO when
// you drill into it. Two counts of the same thing that can disagree are worse
// than one count.
func TestTheSummaryIsAggregatedFromExactlyTheRowsTheDrilldownPrints(t *testing.T) {
	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{openMr(12), openMr(13), openMr(14)},
	})

	summary := SummaryFromRows("pathauto", rows)
	if summary.MergeRequests != 3 {
		t.Errorf("counted %d merge requests over %d rows", summary.MergeRequests, len(rows))
	}
	if summary.ReadyAuto+summary.Review+summary.Blocked != len(rows) {
		t.Errorf("verdicts sum to %d over %d rows",
			summary.ReadyAuto+summary.Review+summary.Blocked, len(rows))
	}
}

// Counted per distinct subject, not per row: an issue backported to two
// branches is two rows over one set of merge requests.
func TestMergeRequestsAreCountedOncePerSubject(t *testing.T) {
	shared := openMr(12)
	rows := []Row{
		{Branch: "1.0.x", MergeRequests: []gitlab.MergeRequest{shared}, Local: results.NoEvidence()},
		{Branch: "2.0.x", MergeRequests: []gitlab.MergeRequest{shared}, Local: results.NoEvidence()},
	}

	if got := SummaryFromRows("pathauto", rows).MergeRequests; got != 1 {
		t.Errorf("counted %d, want the one merge request", got)
	}
}

// And a merged one is not an open merge request.
func TestAMergedMergeRequestIsNotCounted(t *testing.T) {
	merged := openMr(7)
	merged.State = "merged"

	rows := []Row{{
		Branch:        "2.0.x",
		MergeRequests: []gitlab.MergeRequest{merged, openMr(12)},
		Local:         results.NoEvidence(),
	}}

	if got := SummaryFromRows("pathauto", rows).MergeRequests; got != 1 {
		t.Errorf("counted %d, want only the open one", got)
	}
}

func TestPatchIssuesAreCountedOncePerIssue(t *testing.T) {
	rows := []Row{
		{Branch: "1.0.x", IssueNid: 3603341, PatchCount: 2, Local: results.NoEvidence()},
		{Branch: "2.0.x", IssueNid: 3603341, PatchCount: 1, Local: results.NoEvidence()},
		{Branch: "2.0.x", IssueNid: 3597808, PatchCount: 1, Local: results.NoEvidence()},
		// No patches: not a patch issue.
		{Branch: "2.0.x", IssueNid: 3590000, PatchCount: 0, Local: results.NoEvidence()},
	}

	if got := SummaryFromRows("pathauto", rows).PatchIssues; got != 2 {
		t.Errorf("counted %d, want 2 issues carrying patches", got)
	}
}

// Stricter than the cell it summarises, deliberately: the number exists to say
// how much work stands between the queue and a verdict.
func TestUncheckedCountsAnyCoreWithoutFreshEvidence(t *testing.T) {
	partial := results.Evidence(map[string]*results.CachedResult{
		"10": nil,
		"11": passed(currentSHA),
	}, currentSHA)
	stale := results.Evidence(map[string]*results.CachedResult{"11": passed("older")}, currentSHA)
	green := results.Evidence(map[string]*results.CachedResult{"11": passed(currentSHA)}, currentSHA)

	rows := []Row{
		{Branch: "2.0.x", Local: partial},
		{Branch: "2.0.x", Local: stale},
		{Branch: "2.0.x", Local: green},
	}

	if got := SummaryFromRows("pathauto", rows).Unchecked; got != 2 {
		t.Errorf("counted %d, want the gap and the stale one", got)
	}
}

// A failing module reports as failed and counts nothing, rather than reporting
// zeroes that read like a healthy empty queue.
func TestAFailedModuleIsSaidRatherThanCountedAsZero(t *testing.T) {
	rows := []Row{RowForModuleFailure("pathauto", &gitlab.Failure{Kind: gitlab.EndpointClosed, Status: 403})}

	summary := SummaryFromRows("pathauto", rows)
	if !summary.Failed {
		t.Fatal("not reported as failed")
	}

	cells := summary.TableCells("5m ago")
	for i, cell := range cells {
		if i == sumModule || i == sumCached {
			continue
		}
		if cell != "–" {
			t.Errorf("cell %d is %q, want a dash rather than a count", i, cell)
		}
	}
	if cells[sumCached] != "5m ago" {
		t.Errorf("cache age %q", cells[sumCached])
	}
}

// Zero reads better as a dash: the eye should catch the non-zero cells.
func TestZeroCountsRenderAsDashesAndRealOnesAsNumbers(t *testing.T) {
	rows := []Row{{
		Branch:        "2.0.x",
		MergeRequests: []gitlab.MergeRequest{openMr(12)},
		Local:         results.NoEvidence(),
		Verdict:       &gate.Verdict{Status: gate.ReadyAuto},
	}}

	cells := SummaryFromRows("pathauto", rows).TableCells("just now")
	if len(cells) != 8 {
		t.Fatalf("got %d cells: %v", len(cells), cells)
	}
	if cells[sumReady] != "1" {
		t.Errorf("ready cell %q", cells[sumReady])
	}
	if cells[sumCIFailed] != "–" {
		t.Errorf("CI-failed cell %q, want a dash for nought", cells[sumCIFailed])
	}
	// The counting columns are numbers even at zero, because "0 merge
	// requests" is a fact about the queue rather than an absence.
	if cells[sumMRs] != "1" || cells[sumPatchIssues] != "0" {
		t.Errorf("MRs %q, patch issues %q", cells[sumMRs], cells[sumPatchIssues])
	}
	if cells[sumBranches] != "2.0.x" {
		t.Errorf("branches cell %q", cells[sumBranches])
	}
}

func TestBranchesAreListedOnceEachAndInRowOrder(t *testing.T) {
	rows := []Row{
		{Branch: "2.0.x", Local: results.NoEvidence()},
		{Branch: "1.0.x", Local: results.NoEvidence()},
		{Branch: "2.0.x", Local: results.NoEvidence()},
		// A failure row's placeholder branch is not a branch.
		{Branch: "-", Local: results.NoEvidence()},
	}

	summary := SummaryFromRows("pathauto", rows)
	if !slices.Equal(summary.Branches, []string{"2.0.x", "1.0.x"}) {
		t.Errorf("branches %v", summary.Branches)
	}
	if strings.Contains(summary.TableCells("")[sumBranches], "-,") {
		t.Errorf("branches cell %q carries the placeholder", summary.TableCells("")[sumBranches])
	}
}

func TestAModuleWithNoRowsSummarisesToNothing(t *testing.T) {
	summary := SummaryFromRows("pathauto", nil)

	if summary.MergeRequests != 0 || summary.PatchIssues != 0 || summary.Failed {
		t.Errorf("got %+v", summary)
	}
	if got := summary.TableCells("never")[sumBranches]; got != "–" {
		t.Errorf("branches cell %q", got)
	}
}

// A snapshot serialises upstream payloads fetched with the maintainer's PAT,
// which for any limited-visibility project is token-scoped data.
func TestTheDashboardCacheIsOwnerOnly(t *testing.T) {
	// Under a directory that does not exist yet, which is the real case:
	// <cockpit>/cache/dashboard is created by this. An existing directory is
	// deliberately left alone by both implementations — the mode is applied
	// when the directory is made, not imposed on somebody else's.
	root := filepath.Join(t.TempDir(), "cache", "dashboard")
	cache := NewCache(root)

	if err := cache.Save("pathauto", fixtureSnapshot(t)); err != nil {
		t.Fatalf("save: %v", err)
	}

	info, err := os.Stat(filepath.Join(root, "pathauto.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cache file is %04o, want 0600", info.Mode().Perm())
	}
	dir, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("cache directory is %04o, want 0700", dir.Mode().Perm())
	}
}

func TestASavedSnapshotLoadsBackWhole(t *testing.T) {
	cache := NewCache(t.TempDir())
	original := fixtureSnapshot(t)

	if err := cache.Save("pathauto", original); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, ok := cache.Load("pathauto")
	if !ok {
		t.Fatal("not loaded")
	}
	if !loaded.FetchedAt.Equal(original.FetchedAt) {
		t.Errorf("fetched at %v, want %v", loaded.FetchedAt, original.FetchedAt)
	}
	if len(loaded.MergeRequests()) != len(original.MRData) {
		t.Errorf("%d merge requests", len(loaded.MergeRequests()))
	}
	if len(loaded.PatchIssues()) != len(original.PatchIssueData) {
		t.Errorf("%d issues", len(loaded.PatchIssues()))
	}
}

// A truncated or malformed cache file is a miss the dashboard re-fetches over,
// never a failure demanding a manual rm.
func TestAnUnusableCacheFileIsAMiss(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)

	for name, body := range map[string]string{
		"pathauto.json": "{ truncated",
		"token.json":    "",
		"webform.json":  "[]",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	for _, module := range []string{"pathauto", "token", "webform", "never_cached"} {
		if _, ok := cache.Load(module); ok {
			t.Errorf("%s loaded from an unusable file", module)
		}
	}
}

// The cache is keyed by module machine name, and that name becomes a filename.
func TestTheCacheRefusesANameThatIsNotAMachineName(t *testing.T) {
	cache := NewCache(t.TempDir())

	for _, module := range []string{"../escape", "Path-Auto", "", "a/b"} {
		if err := cache.Save(module, fixtureSnapshot(t)); err == nil {
			t.Errorf("%q was accepted as a cache key", module)
		}
		if _, ok := cache.Load(module); ok {
			t.Errorf("%q loaded something", module)
		}
	}
}

func TestTheOldestFetchIsTheHeadlineAge(t *testing.T) {
	cache := NewCache(t.TempDir())

	older := fixtureSnapshot(t)
	older.FetchedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := fixtureSnapshot(t)
	newer.FetchedAt = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	if err := cache.Save("pathauto", newer); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := cache.Save("token", older); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := cache.OldestFetchedAt([]string{"pathauto", "token", "never_cached"})
	if !got.Equal(older.FetchedAt) {
		t.Errorf("got %v, want the oldest", got)
	}

	if uncached := cache.OldestFetchedAt([]string{"never_cached"}); !uncached.IsZero() {
		t.Errorf("got %v for nothing cached", uncached)
	}
}

// fakeReader answers the GitLab surface the assembler needs, and records what
// it was asked.
type fakeReader struct {
	project        *gitlab.Project
	projectFailure *gitlab.Failure
	listed         []gitlab.MergeRequest
	listFailure    *gitlab.Failure
	detailFailure  map[int]*gitlab.Failure
	detailCalls    int
}

func (r *fakeReader) Project(string) (*gitlab.Project, *gitlab.Failure) {
	return r.project, r.projectFailure
}

func (r *fakeReader) OpenMergeRequests(gitlab.Project) ([]gitlab.MergeRequest, *gitlab.Failure) {
	return r.listed, r.listFailure
}

func (r *fakeReader) MergeRequest(_ gitlab.Project, iid int) (*gitlab.MergeRequest, *gitlab.Failure) {
	r.detailCalls++
	if failure, present := r.detailFailure[iid]; present {
		return nil, failure
	}
	for _, one := range r.listed {
		if one.IID == iid {
			detailed := one
			detailed.HeadPipeline = &gitlab.Pipeline{Status: gitlab.StatusSuccess, RawStatus: "success"}

			return &detailed, nil
		}
	}

	return nil, &gitlab.Failure{Kind: gitlab.NotFound, Status: 404}
}

func modules(names ...string) map[string]cockpit.Module {
	out := map[string]cockpit.Module{}
	for _, name := range names {
		out[name] = cockpit.Module{
			Name: name, Project: "project/" + name, CoreVersions: []string{"11"}, Watched: true,
		}
	}

	return out
}

// The list payload lacks head_pipeline, which is why every row costs a second
// call.
func TestTheAssemblerFetchesTheDetailForItsPipeline(t *testing.T) {
	project := pathautoProject()
	reader := &fakeReader{project: &project, listed: []gitlab.MergeRequest{openMr(12), openMr(13)}}

	rows := NewAssembler(reader, &fakeStore{}, nil).Assemble(modules("pathauto"), "")

	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
	if reader.detailCalls != 2 {
		t.Errorf("made %d detail calls for %d merge requests", reader.detailCalls, len(rows))
	}
	if rows[0].CICell() != "pass" {
		t.Errorf("CI cell %q — the detail was not used", rows[0].CICell())
	}
}

// If that fetch fails the row still renders, the CI cell carries the failure
// state, and the gate — seeing no pipeline — conservatively denies.
func TestAFailedDetailFetchDegradesTheRowRatherThanLosingIt(t *testing.T) {
	project := pathautoProject()
	reader := &fakeReader{
		project: &project,
		listed:  []gitlab.MergeRequest{openMr(12)},
		detailFailure: map[int]*gitlab.Failure{
			12: {Kind: gitlab.RateLimited, Status: 429},
		},
	}

	rows := NewAssembler(reader, &fakeStore{}, nil).Assemble(modules("pathauto"), "")

	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].CICell() != "n/a (rate-limited)" {
		t.Errorf("CI cell %q", rows[0].CICell())
	}
	if rows[0].IsReadyAuto() {
		t.Error("a row with no pipeline was ready to merge")
	}
}

// A module whose project or merge requests cannot be read gets one visible
// failure row, not silence.
func TestAModuleThatCannotBeReadGetsOneFailureRow(t *testing.T) {
	project := pathautoProject()

	for name, reader := range map[string]*fakeReader{
		"no project": {projectFailure: &gitlab.Failure{Kind: gitlab.NotFound, Status: 404}},
		"no listing": {project: &project, listFailure: &gitlab.Failure{Kind: gitlab.EndpointClosed, Status: 403}},
	} {
		rows := NewAssembler(reader, &fakeStore{}, nil).Assemble(modules("pathauto"), "")

		if len(rows) != 1 {
			t.Fatalf("%s: got %d rows", name, len(rows))
		}
		if rows[0].ModuleFailure == nil {
			t.Errorf("%s: the row carries no failure", name)
		}
		if GuidanceFor(rows[0]).Command == "" {
			t.Errorf("%s: no way back in", name)
		}
	}
}

func TestModulesAreAssembledInNameOrder(t *testing.T) {
	project := pathautoProject()
	reader := &fakeReader{project: &project, listed: []gitlab.MergeRequest{openMr(12)}}

	rows := NewAssembler(reader, &fakeStore{}, nil).Assemble(modules("token", "pathauto", "webform"), "")

	names := []string{}
	for _, row := range rows {
		names = append(names, row.Module)
	}
	if !slices.Equal(names, []string{"pathauto", "token", "webform"}) {
		t.Errorf("got %v", names)
	}
}

// Without a snapshot there is nothing to group by, so every merge request is
// its own row — which is what the fast lane wants, since it prompts per merge
// request.
func TestTheAssemblerMakesOneRowPerMergeRequest(t *testing.T) {
	project := pathautoProject()
	both := []gitlab.MergeRequest{openMr(12), openMr(13)}
	both[0].Title = "Issue #3603341 by somebody: Drupal 12 compatibility"
	both[1].Title = "Issue #3603341 by somebody else: Drupal 12 compatibility"

	reader := &fakeReader{project: &project, listed: both}
	rows := NewAssembler(reader, &fakeStore{}, nil).Assemble(modules("pathauto"), "")

	if len(rows) != 2 {
		t.Fatalf("got %d rows for two merge requests on one issue", len(rows))
	}
}

// A core the module does not track is not something to report on.
func TestAModuleTrackingNoneOfTheFilteredCoreIsSkipped(t *testing.T) {
	project := pathautoProject()
	reader := &fakeReader{project: &project, listed: []gitlab.MergeRequest{openMr(12)}}

	rows := NewAssembler(reader, &fakeStore{}, nil).Assemble(modules("pathauto"), "13")

	if len(rows) != 0 {
		t.Errorf("got %d rows", len(rows))
	}
	if reader.detailCalls != 0 {
		t.Errorf("made %d calls for a module it had nothing to ask about", reader.detailCalls)
	}
}

// A failure row contributes nothing to the counts, whatever else it happens to
// carry.
//
// RowForModuleFailure builds an empty shape, so today this is belt and braces
// — but the counts are the thing the overview promises match the drill-down,
// and a row that could not be read has no business adding to them.
func TestAFailureRowContributesNothingWhateverElseItCarries(t *testing.T) {
	clean := []Row{{
		Branch:        "2.0.x",
		MergeRequests: []gitlab.MergeRequest{openMr(12)},
		IssueNid:      3603341,
		PatchCount:    1,
		Local:         results.NoEvidence(),
		Verdict:       &gate.Verdict{Status: gate.ReadyAuto},
	}}

	// The same row again, but unreadable.
	withFailure := append(slices.Clone(clean), Row{
		Branch:        "1.0.x",
		MergeRequests: []gitlab.MergeRequest{openMr(99)},
		IssueNid:      3597808,
		PatchCount:    3,
		Local:         results.Evidence(map[string]*results.CachedResult{"11": nil}, currentSHA),
		Verdict:       &gate.Verdict{Status: gate.Blocked},
		ModuleFailure: &gitlab.Failure{Kind: gitlab.EndpointClosed, Status: 403},
	})

	before := SummaryFromRows("pathauto", clean)
	after := SummaryFromRows("pathauto", withFailure)

	if !after.Failed {
		t.Fatal("the failure was not reported")
	}
	if after.MergeRequests != before.MergeRequests {
		t.Errorf("merge requests went from %d to %d", before.MergeRequests, after.MergeRequests)
	}
	if after.PatchIssues != before.PatchIssues {
		t.Errorf("patch issues went from %d to %d", before.PatchIssues, after.PatchIssues)
	}
	if after.Blocked != before.Blocked {
		t.Errorf("CI-failed went from %d to %d", before.Blocked, after.Blocked)
	}
	if after.Unchecked != before.Unchecked {
		t.Errorf("unchecked went from %d to %d", before.Unchecked, after.Unchecked)
	}
	if !slices.Equal(after.Branches, before.Branches) {
		t.Errorf("branches went from %v to %v", before.Branches, after.Branches)
	}
}

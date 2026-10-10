package dashboard

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/config"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gate"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
)

// fakeStore answers what a recorded result would be, and records what it was
// asked, so "which cores did the row gather evidence on" is testable.
type fakeStore struct {
	byKey map[string]*results.CachedResult
	asked []string
	err   error
}

func (s *fakeStore) Latest(module string, key results.Key, coreMajor string) (*results.CachedResult, error) {
	s.asked = append(s.asked, module+"/"+key.Segment+"/"+coreMajor)
	if s.err != nil {
		return nil, s.err
	}

	return s.byKey[key.Segment+"/"+coreMajor], nil
}

func passed(sha string) *results.CachedResult {
	return &results.CachedResult{
		SHA: sha, RecordedAt: time.Now(),
		Result: check.RunResult{Results: []check.Result{{Type: check.PhpUnit, Status: check.Passed}}},
	}
}

func pathautoModule(cores ...string) cockpit.Module {
	return cockpit.Module{
		Name: "pathauto", Project: "project/pathauto", CoreVersions: cores, Watched: true,
	}
}

func pathautoProject() gitlab.Project {
	return gitlab.Project{
		ID: 11, Path: "pathauto", PathWithNamespace: "project/pathauto",
		WebURL: "https://git.drupalcode.org/project/pathauto", DefaultBranch: "2.0.x",
	}
}

func snapshotWith(issues []drupal.Issue, merged []gitlab.MergeRequest) *ModuleSnapshot {
	issueData := []map[string]any{}
	for _, issue := range issues {
		issueData = append(issueData, issue.ToAPIMap())
	}
	mergedData := []map[string]any{}
	for _, mr := range merged {
		mergedData = append(mergedData, mr.ToAPIMap())
	}

	return &ModuleSnapshot{
		FetchedAt:       time.Now(),
		ProjectData:     pathautoProject().ToAPIMap(),
		MRData:          []map[string]any{},
		PatchIssueData:  issueData,
		MergedMRData:    mergedData,
		ForkNids:        map[int]int{},
		CoreConstraints: map[string]string{},
		MergeRefSHAs:    map[int]string{},
	}
}

func factory(store EvidenceStore) *RowFactory { return NewRowFactory(store, nil) }

// Core stopped being a row multiplier: a branch supports several at once, so a
// row per (subject x core) described one piece of work several times.
func TestCoresAreEvidenceNotRows(t *testing.T) {
	store := &fakeStore{}
	mr := openMr(12)

	rows := factory(store).Rows(RowsInput{
		Module:        pathautoModule("10", "11", "12"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{mr},
	})

	if len(rows) != 1 {
		t.Fatalf("got %d rows for one merge request on three cores", len(rows))
	}
	if !slices.Equal(rows[0].Local.Cores(), []string{"10", "11", "12"}) {
		t.Errorf("evidence covers %v", rows[0].Local.Cores())
	}
}

// An issue with work on 1.0.x and on 2.0.x is a backport: two pieces of work,
// not one seen twice.
func TestABackportIsTwoRows(t *testing.T) {
	onTwo := openMr(12)
	onTwo.TargetBranch = "2.0.x"
	onOne := openMr(13)
	onOne.TargetBranch = "1.0.x"
	onTwo.Description = "Issue #3603341 by somebody: Drupal 12 compatibility"
	onOne.Description = "Issue #3603341 by somebody: Drupal 12 compatibility"

	issue := issueWithPatches(3603341, 0)
	snapshot := snapshotWith([]drupal.Issue{issue}, nil)

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{onTwo, onOne},
		Snapshot:      snapshot,
	})

	if len(rows) != 2 {
		t.Fatalf("got %d rows: %+v", len(rows), rows)
	}
	// Sorted by branch, so the order is stable between runs.
	if rows[0].Branch != "1.0.x" || rows[1].Branch != "2.0.x" {
		t.Errorf("branches %q and %q", rows[0].Branch, rows[1].Branch)
	}
}

// Checking a branch on a core it does not declare produces a failure that says
// nothing about the module — and would deny a merge on the strength of a core
// the branch never claimed.
func TestEvidenceIsNarrowedToWhatTheBranchDeclares(t *testing.T) {
	store := &fakeStore{}
	mr := openMr(12)
	mr.TargetBranch = "2.0.x"

	snapshot := snapshotWith(nil, nil)
	snapshot.CoreConstraints = map[string]string{"2.0.x": "^11 || ^12"}

	rows := factory(store).Rows(RowsInput{
		Module:        pathautoModule("9", "10", "11", "12"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{mr},
		Snapshot:      snapshot,
	})

	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if !slices.Equal(rows[0].Local.Cores(), []string{"11", "12"}) {
		t.Errorf("evidence covers %v, want only what the branch declares", rows[0].Local.Cores())
	}
}

// Nothing here ever narrows to empty: a branch that appears to support none of
// the cores you track is far likelier to be a constraint misread than a real
// state, and returning nothing would make the module vanish.
func TestNarrowingNeverMakesAModuleVanish(t *testing.T) {
	for name, constraint := range map[string]string{
		"unparseable":     "not a constraint at all",
		"declares none":   "^7",
		"empty":           "",
		"nothing tracked": "^99",
	} {
		snapshot := snapshotWith(nil, nil)
		snapshot.CoreConstraints = map[string]string{"2.0.x": constraint}

		rows := factory(&fakeStore{}).Rows(RowsInput{
			Module:        pathautoModule("10", "11"),
			Project:       pathautoProject(),
			MergeRequests: []gitlab.MergeRequest{openMr(12)},
			Snapshot:      snapshot,
		})

		if len(rows) != 1 {
			t.Fatalf("%s: got %d rows", name, len(rows))
		}
		if !slices.Equal(rows[0].Local.Cores(), []string{"10", "11"}) {
			t.Errorf("%s: evidence covers %v, want the tracked set whole", name, rows[0].Local.Cores())
		}
	}
}

// A branch with no recorded constraint keeps the tracked set whole — the
// behaviour before any of this existed.
func TestAnUnrecordedBranchKeepsTheTrackedSet(t *testing.T) {
	snapshot := snapshotWith(nil, nil)
	snapshot.CoreConstraints = map[string]string{"1.0.x": "^10"}

	mr := openMr(12)
	mr.TargetBranch = "2.0.x"

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:        pathautoModule("10", "11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{mr},
		Snapshot:      snapshot,
	})

	if !slices.Equal(rows[0].Local.Cores(), []string{"10", "11"}) {
		t.Errorf("evidence covers %v", rows[0].Local.Cores())
	}
}

// 33 of pathauto's 162 merge requests claim no issue at all.
func TestAMergeRequestClaimingNoIssueKeepsARow(t *testing.T) {
	unlinked := openMr(42)
	unlinked.Title = "Some refactor"
	unlinked.Description = ""
	unlinked.SourceBranch = "refactor"

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{unlinked},
		Snapshot:      snapshotWith(nil, nil),
	})

	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].Contribution != nil {
		t.Error("an unlinked merge request was paired to an issue")
	}
	if rows[0].IssueCell() != "–" {
		t.Errorf("issue cell %q", rows[0].IssueCell())
	}
}

// An unpaired merge request is not an anonymous one: the nid it claims is
// still shown.
func TestAnUnpairedMergeRequestStillShowsTheNidItClaims(t *testing.T) {
	claiming := openMr(42)
	claiming.Title = "Issue #9999999 by somebody: An issue nobody has open"

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{claiming},
		Snapshot:      snapshotWith(nil, nil),
	})

	if rows[0].IssueCell() != "9999999" {
		t.Errorf("issue cell %q", rows[0].IssueCell())
	}
}

// Being unclaimed is the issue queue's subject; here it would be 29 of
// pathauto's 93 issues saying nothing.
func TestAnIssueWithNothingOpenAndNoPatchYieldsNoRow(t *testing.T) {
	bare := drupal.Issue{Nid: 3603341, Title: "Nobody has started this", Status: drupal.StatusActive}

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:   pathautoModule("11"),
		Project:  pathautoProject(),
		Snapshot: snapshotWith([]drupal.Issue{bare}, nil),
	})

	if len(rows) != 0 {
		t.Errorf("got %d rows: %+v", len(rows), rows)
	}
}

// An issue carrying only patches is a row, keyed by the patch rather than by a
// merge request.
func TestAPatchOnlyIssueIsARowKeyedByThePatch(t *testing.T) {
	store := &fakeStore{}
	issue := issueWithPatches(3603341, 2)

	rows := factory(store).Rows(RowsInput{
		Module:   pathautoModule("11"),
		Project:  pathautoProject(),
		Snapshot: snapshotWith([]drupal.Issue{issue}, nil),
	})

	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].MergeRequest != nil {
		t.Error("a patch-only row carries a merge request")
	}
	if rows[0].Verdict != nil {
		t.Error("a patch-only row carries a gate verdict")
	}
	if !slices.Contains(store.asked, "pathauto/patch-3603341/11") {
		t.Errorf("evidence was looked up as %v, want the patch namespace", store.asked)
	}
}

// Both subjects are identified by a number, and a patch verdict read as a
// merge-request verdict would put unmergeable evidence in front of the gate.
func TestPatchAndMergeRequestEvidenceLiveInSeparateNamespaces(t *testing.T) {
	store := &fakeStore{}
	issue := issueWithPatches(3603341, 1)
	mr := openMr(3603341)
	mr.Title = "Issue #3603341 by somebody: Drupal 12 compatibility"

	factory(store).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{mr},
		Snapshot:      snapshotWith([]drupal.Issue{issue}, nil),
	})

	if !slices.Contains(store.asked, "pathauto/3603341/11") {
		t.Errorf("asked %v, want the merge-request namespace", store.asked)
	}
	if slices.Contains(store.asked, "pathauto/patch-3603341/11") {
		t.Errorf("asked %v — the open merge request should own the row", store.asked)
	}
}

// Evidence is keyed on the merge ref, because CI checks the branch merged into
// the current tip of its target and that tree moves when either side does.
func TestEvidenceIsKeyedOnTheMergeRefWhenThereIsOne(t *testing.T) {
	mr := openMr(12)
	mr.HeadSHA = "head111"

	snapshot := snapshotWith(nil, nil)
	snapshot.MergeRefSHAs = map[int]string{12: "merge222"}

	store := &fakeStore{byKey: map[string]*results.CachedResult{"12/11": passed("merge222")}}

	rows := factory(store).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{mr},
		Snapshot:      snapshot,
	})

	if !rows[0].Local.AllGreen() {
		t.Errorf("evidence recorded against the merge ref read as %q", rows[0].Local.Cell())
	}
}

// No merge ref means the head SHA is the revision — the adapter checks the
// branch there too, so both halves fall back together.
func TestWithNoMergeRefTheHeadShaIsTheRevision(t *testing.T) {
	mr := openMr(12)
	mr.HeadSHA = "head111"

	store := &fakeStore{byKey: map[string]*results.CachedResult{"12/11": passed("head111")}}

	rows := factory(store).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{mr},
	})

	if !rows[0].Local.AllGreen() {
		t.Errorf("evidence read as %q", rows[0].Local.Cell())
	}
}

// An issue can carry both a real branch and the bot's empty draft, and the
// real branch is what gets merged.
func TestASubstantiveMergeRequestRepresentsTheBranch(t *testing.T) {
	empty := openMr(11)
	empty.DiffBaseSHA, empty.DiffHeadSHA = "same", "same"
	empty.Title = "Issue #3603341 by bot: Drupal 12 compatibility"
	carrying := openMr(12)
	carrying.Title = "Issue #3603341 by somebody: Drupal 12 compatibility"

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{empty, carrying},
		Snapshot:      snapshotWith([]drupal.Issue{issueWithPatches(3603341, 0)}, nil),
	})

	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	mr, err := rows[0].RequireMergeRequest()
	if err != nil {
		t.Fatalf("no merge request: %v", err)
	}
	if mr.IID != 12 {
		t.Errorf("row acts on !%d, want the one that carries changes", mr.IID)
	}
}

// Without a snapshot there is nothing to group merge requests by, so every one
// of them is its own row.
func TestWithoutASnapshotEveryMergeRequestIsItsOwnRow(t *testing.T) {
	first := openMr(12)
	first.Title = "Issue #3603341 by somebody: Drupal 12 compatibility"
	second := openMr(13)
	second.Title = "Issue #3603341 by somebody else: Drupal 12 compatibility"

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{first, second},
	})

	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
}

// A version filter that names a core the module does not track yields no rows,
// rather than silently reporting on a core nobody asked about.
func TestAVersionFilterNarrowsToOneTrackedCore(t *testing.T) {
	if got := Cores(pathautoModule("10", "11", "12"), "11"); !slices.Equal(got, []string{"11"}) {
		t.Errorf("got %v", got)
	}
	if got := Cores(pathautoModule("10", "11"), "12"); len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
	if got := Cores(pathautoModule("10", "11"), ""); !slices.Equal(got, []string{"10", "11"}) {
		t.Errorf("got %v", got)
	}

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:        pathautoModule("10", "11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{openMr(12)},
		VersionFilter: "12",
	})
	if len(rows) != 0 {
		t.Errorf("got %d rows for an untracked core", len(rows))
	}
}

// The branch a dormant row belongs to is resolved against branches that exist,
// because the version field holds whatever anyone has ever typed into it.
func TestADormantRowsBranchIsResolvedNotParsed(t *testing.T) {
	issue := issueWithPatches(3603341, 1)
	issue.Version = "2.0.0"

	// The project's merge requests name 2.0.x, so that is a branch that exists.
	known := openMr(99)
	known.State = "merged"
	known.TargetBranch = "2.0.x"
	known.Title = "Issue #9999999 by somebody: Something else"

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:   pathautoModule("11"),
		Project:  pathautoProject(),
		Snapshot: snapshotWith([]drupal.Issue{issue}, []gitlab.MergeRequest{known}),
	})

	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].Branch != "2.0.x" {
		t.Errorf("branch %q, want the one the version resolves to", rows[0].Branch)
	}
}

// A version nothing resolves falls back to the project's default rather than
// inventing a branch.
func TestAnUnresolvableVersionFallsBackToTheDefaultBranch(t *testing.T) {
	issue := issueWithPatches(3603341, 1)
	issue.Version = "x.y.z"

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:   pathautoModule("11"),
		Project:  pathautoProject(),
		Snapshot: snapshotWith([]drupal.Issue{issue}, nil),
	})

	if rows[0].Branch != "2.0.x" {
		t.Errorf("branch %q, want the project default", rows[0].Branch)
	}
}

// A landing decides the branch, because that is where the work actually went.
func TestALandingDecidesADormantRowsBranch(t *testing.T) {
	issue := issueWithPatches(3603341, 1)
	issue.Version = "2.0.0"

	landed := openMr(7)
	landed.State = "merged"
	landed.MergedAt = "2026-06-12T10:00:00Z"
	landed.TargetBranch = "1.0.x"
	landed.Title = "Issue #3603341 by somebody: Drupal 12 compatibility"

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:   pathautoModule("11"),
		Project:  pathautoProject(),
		Snapshot: snapshotWith([]drupal.Issue{issue}, []gitlab.MergeRequest{landed}),
	})

	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].Branch != "1.0.x" {
		t.Errorf("branch %q, want where the work landed", rows[0].Branch)
	}
	if rows[0].Landed == nil {
		t.Error("the landing was not carried onto the row")
	}
}

// A merged merge request makes no row of its own: it answers "has this already
// landed?" about an issue that is still open.
func TestAMergedMergeRequestMakesNoRowOfItsOwn(t *testing.T) {
	landed := openMr(7)
	landed.State = "merged"
	landed.MergedAt = "2026-06-12T10:00:00Z"
	landed.Title = "Issue #3603341 by somebody: Drupal 12 compatibility"

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:   pathautoModule("11"),
		Project:  pathautoProject(),
		Snapshot: snapshotWith([]drupal.Issue{issueWithPatches(3603341, 1)}, []gitlab.MergeRequest{landed}),
	})

	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].MergeRequest != nil {
		t.Error("a merged merge request became the row's open one")
	}
}

// A row with no evidence denies the fast lane, which is the safe direction —
// the cache's own complaint is what tells the operator why.
func TestAnUnreadableResultsStoreDeniesRatherThanCrashes(t *testing.T) {
	store := &fakeStore{err: errors.New("directory is not readable")}

	rows := factory(store).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{openMr(12)},
	})

	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].IsReadyAuto() {
		t.Error("a row with unreadable evidence was ready to merge")
	}
	if !slices.Contains(rows[0].Verdict.Reasons, "local-missing") {
		t.Errorf("reasons %v", rows[0].Verdict.Reasons)
	}
}

// The gate the factory was given is the one that classifies, so a caller can
// substitute a per-core bot pattern without the factory knowing.
func TestTheInjectedGateIsTheOneThatClassifies(t *testing.T) {
	asked := 0
	custom := gate.NewFastLane(func(core string) config.BotPattern {
		asked++

		return config.BotPattern{}
	})

	NewRowFactory(&fakeStore{}, custom).Rows(RowsInput{
		Module:        pathautoModule("10", "11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{openMr(12)},
	})

	if asked == 0 {
		t.Error("the injected gate was never consulted")
	}
}

// The same keying applies to a row that belongs to an issue, not only to an
// unlinked one: evidence is about the tree CI analysed.
func TestAnIssueRowIsAlsoKeyedOnTheMergeRef(t *testing.T) {
	mr := openMr(12)
	mr.HeadSHA = "head111"
	mr.Title = "Issue #3603341 by somebody: Drupal 12 compatibility"

	snapshot := snapshotWith([]drupal.Issue{issueWithPatches(3603341, 0)}, nil)
	snapshot.MergeRefSHAs = map[int]string{12: "merge222"}

	store := &fakeStore{byKey: map[string]*results.CachedResult{"12/11": passed("merge222")}}

	rows := factory(store).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{mr},
		Snapshot:      snapshot,
	})

	if len(rows) != 1 || rows[0].Contribution == nil {
		t.Fatalf("got %d rows, paired=%v", len(rows), rows[0].Contribution != nil)
	}
	if !rows[0].Local.AllGreen() {
		t.Errorf("evidence recorded against the merge ref read as %q", rows[0].Local.Cell())
	}

	// And evidence recorded against the head alone is stale, because the merge
	// tree moves when either side does.
	staleStore := &fakeStore{byKey: map[string]*results.CachedResult{"12/11": passed("head111")}}
	staleRows := factory(staleStore).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{mr},
		Snapshot:      snapshot,
	})
	if !staleRows[0].Local.AnyStale() {
		t.Errorf("evidence about the head read as current: %q", staleRows[0].Local.Cell())
	}
}

// The order is the contract: issue rows by nid then branch, then the merge
// requests belonging to no listed issue, by iid. Two runs of the same
// dashboard must not shuffle.
func TestTheRowOrderIsStable(t *testing.T) {
	later := openMr(99)
	later.Title = "One thing"
	later.SourceBranch = "a"
	earlier := openMr(7)
	earlier.Title = "Another thing"
	earlier.SourceBranch = "b"
	middle := openMr(42)
	middle.Title = "A third thing"
	middle.SourceBranch = "c"

	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:        pathautoModule("11"),
		Project:       pathautoProject(),
		MergeRequests: []gitlab.MergeRequest{later, earlier, middle},
		Snapshot:      snapshotWith(nil, nil),
	})

	iids := []int{}
	for _, row := range rows {
		iids = append(iids, row.MergeRequest.IID)
	}
	if !slices.Equal(iids, []int{7, 42, 99}) {
		t.Errorf("unlinked rows came out as %v, want them by iid", iids)
	}
}

func TestIssueRowsComeOutByNid(t *testing.T) {
	higher := issueWithPatches(3603399, 1)
	lower := issueWithPatches(3603311, 1)

	// Given in the wrong order on purpose.
	rows := factory(&fakeStore{}).Rows(RowsInput{
		Module:   pathautoModule("11"),
		Project:  pathautoProject(),
		Snapshot: snapshotWith([]drupal.Issue{higher, lower}, nil),
	})

	nids := []int{}
	for _, row := range rows {
		nids = append(nids, row.IssueNid)
	}
	if !slices.Equal(nids, []int{3603311, 3603399}) {
		t.Errorf("issue rows came out as %v, want them by nid", nids)
	}
}

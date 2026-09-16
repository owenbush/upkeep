package command

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/dashboard"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/workflow"
)

// aPatchQueue is a module's Needs Review / RTBC queue in the shapes the report
// classifies: a patch nobody has branched, a patch beside an empty draft, and
// a patch beside a merge request that carries the work.
func aPatchQueue() []drupal.Issue {
	return []drupal.Issue{
		{
			Nid: 3200001, Title: "Token replacement escapes twice",
			Status: drupal.StatusNeedsReview,
			Files:  []drupal.IssueFile{{Name: "3200001-2.patch", URL: "https://drupal.org/files/a.patch"}},
		},
		{
			Nid: 3200002, Title: "Bulk generate times out",
			Status: drupal.StatusRtbc,
			Files:  []drupal.IssueFile{{Name: "3200002-7-d11.patch", URL: "https://drupal.org/files/b.patch"}},
		},
		{
			Nid: 3200003, Title: "Drupal 12 compatibility",
			Status: drupal.StatusNeedsReview,
			Files:  []drupal.IssueFile{{Name: "3200003-1.patch", URL: "https://drupal.org/files/c.patch"}},
		},
		// No patch at all: work that arrived as a branch, which is the
		// dashboard's to report and has no business here.
		{
			Nid: 3200004, Title: "Add a settings form",
			Status: drupal.StatusNeedsReview,
		},
	}
}

// patchGitlab serves the merge requests the report cross-references against.
type patchGitlab struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []string

	// open is the list payload, which GitLab publishes without diff refs — so
	// emptiness is unknown until each is fetched singly.
	open   []map[string]any
	detail map[int]map[string]any
	merged []map[string]any

	openStatus   int
	mergedStatus int
	forkStatus   int
	detailStatus int
}

func aPatchGitlab(t *testing.T) *patchGitlab {
	t.Helper()

	scene := &patchGitlab{
		open: []map[string]any{}, detail: map[int]map[string]any{}, merged: []map[string]any{},
		openStatus: http.StatusOK, mergedStatus: http.StatusOK,
		forkStatus: http.StatusOK, detailStatus: http.StatusOK,
	}

	scene.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scene.mu.Lock()
		scene.requests = append(scene.requests, r.Method+" "+r.URL.String())
		scene.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		path, query := r.URL.EscapedPath(), r.URL.Query()

		switch {
		case strings.Contains(path, "/projects/project%2F"):
			_, _ = w.Write([]byte(
				`{"id":1,"path":"pathauto","path_with_namespace":"project/pathauto",` +
					`"web_url":"https://git.drupalcode.org/project/pathauto"}`))

		case strings.HasSuffix(path, "/forks"):
			if scene.forkStatus != http.StatusOK {
				w.WriteHeader(scene.forkStatus)
				_, _ = w.Write([]byte(`{"message":"500"}`))

				return
			}
			_, _ = w.Write([]byte(`[]`))

		case strings.HasSuffix(path, "/merge_requests") && query.Get("state") == "opened":
			scene.answer(w, scene.openStatus, scene.open)

		case strings.HasSuffix(path, "/merge_requests") && query.Get("state") == "merged":
			scene.answer(w, scene.mergedStatus, scene.merged)

		case strings.Contains(path, "/merge_requests/"):
			if scene.detailStatus != http.StatusOK {
				w.WriteHeader(scene.detailStatus)
				_, _ = w.Write([]byte(`{"message":"500"}`))

				return
			}
			iid := 0
			_, _ = fmt.Sscanf(path[strings.LastIndex(path, "/")+1:], "%d", &iid)
			body, known := scene.detail[iid]
			if !known {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"404"}`))

				return
			}
			_ = json.NewEncoder(w).Encode(body)

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))
		}
	}))
	t.Cleanup(scene.server.Close)

	return scene
}

func (s *patchGitlab) answer(w http.ResponseWriter, status int, rows []map[string]any) {
	if status != http.StatusOK {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"500"}`))

		return
	}
	_ = json.NewEncoder(w).Encode(rows)
}

// detailFetches counts the single-merge-request reads, which is what the
// bounding rule is about.
func (s *patchGitlab) detailFetches() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, seen := range s.requests {
		if strings.Contains(seen, "/merge_requests/") {
			count++
		}
	}

	return count
}

// aListedMergeRequest is the shape GitLab's list endpoint publishes: no diff
// refs, so emptiness is unknown.
func aListedMergeRequest(iid, nid int) map[string]any {
	return map[string]any{
		"iid": iid, "state": "opened",
		"title":         fmt.Sprintf("Issue #%d: work", nid),
		"source_branch": fmt.Sprintf("%d-work", nid), "target_branch": "2.0.x",
		"web_url": fmt.Sprintf("https://git.drupalcode.org/project/pathauto/-/merge_requests/%d", iid),
	}
}

// withDiff settles a merge request's emptiness.
func withDiff(row map[string]any, base, head string) map[string]any {
	settled := map[string]any{}
	for key, value := range row {
		settled[key] = value
	}
	settled["diff_refs"] = map[string]any{"base_sha": base, "head_sha": head}

	return settled
}

// runPatchesCommand invokes the tree against a scripted issue source and
// GitLab.
func runPatchesCommand(
	t *testing.T, issues IssueClients, clients cli.GitlabClients, args ...string,
) (int, string, string) {
	t.Helper()

	return runIssueLoop(t, aWorkingEngine(), issues, clients, cli.NoBrowser{}, args...)
}

// The report exists for the contributions the merge-request dashboard cannot
// see: a patch that never became a branch, and a patch beside an empty draft.
func TestPatchesShowsWhatTheDashboardCannotSee(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aPatchGitlab(t)
	// 3200002 has an empty draft beside its patch; 3200003 has a merge request
	// that carries the work, so it is the dashboard's to report.
	scene.open = []map[string]any{
		aListedMergeRequest(11, 3200002),
		aListedMergeRequest(12, 3200003),
		aListedMergeRequest(13, 3200004),
	}
	scene.detail = map[int]map[string]any{
		11: withDiff(aListedMergeRequest(11, 3200002), "same", "same"),
		12: withDiff(aListedMergeRequest(12, 3200003), "base", "head"),
		13: withDiff(aListedMergeRequest(13, 3200004), "base", "head"),
	}

	code, stdout, stderr := runPatchesCommand(t,
		scriptedIssues{issues: aPatchQueue()}, scriptedClients{client: gitlabClientFor(scene.server)},
		"patches", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// The patch nobody branched, the patch beside the empty draft, and the
	// patch sitting alongside a real merge request — all three are patch files
	// somebody posted, which is what this report is about.
	for _, expected := range []string{"#3200001", "#3200002", "#3200003"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the report is missing %q:\n%s", expected, stdout)
		}
	}
	// An issue whose work arrived only as a branch is the dashboard's row.
	// Printing it here would be the same work reported twice.
	if strings.Contains(stdout, "#3200004") {
		t.Errorf("an issue reachable from the dashboard was reported:\n%s", stdout)
	}
	// The empty draft is named as such: it is why the patch beside it has gone
	// unreviewed, and it is the actionable cell on the row.
	if !strings.Contains(rowFor(t, stdout, "#3200002"), "empty") {
		t.Errorf("the empty merge request is not marked:\n%s", stdout)
	}
	// The newest patch is named: "there is a patch" and "the patch is
	// 3200001-2.patch" are different amounts of help.
	if !strings.Contains(rowFor(t, stdout, "#3200001"), "3200001-2.patch") {
		t.Errorf("the latest patch is not named:\n%s", stdout)
	}
	if !strings.Contains(stdout, "3 issues") ||
		!strings.Contains(stdout, "1 patch-only") ||
		!strings.Contains(stdout, "1 patch, empty MR") ||
		!strings.Contains(stdout, "1 patch + MR") {
		t.Errorf("footer:\n%s", stdout)
	}
}

// Emptiness is unknown from a list payload, so the merge requests attached to
// a scanned issue are fetched singly — and only those.
func TestPatchesResolvesEmptinessOnlyForScannedIssues(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aPatchGitlab(t)
	scene.open = []map[string]any{
		aListedMergeRequest(11, 3200002),
		// On an issue outside the scan: it cannot change a row, so it must
		// never be looked at twice.
		aListedMergeRequest(99, 3999999),
		// Already settled by the listing, so there is nothing left to ask.
		withDiff(aListedMergeRequest(12, 3200003), "base", "head"),
	}
	scene.detail = map[int]map[string]any{
		11: withDiff(aListedMergeRequest(11, 3200002), "same", "same"),
	}

	code, stdout, stderr := runPatchesCommand(t,
		scriptedIssues{issues: aPatchQueue()}, scriptedClients{client: gitlabClientFor(scene.server)},
		"patches", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "empty") {
		t.Errorf("emptiness was never resolved:\n%s", stdout)
	}
	// One fetch: the merge request on a scanned issue whose emptiness the
	// listing could not answer. Not the one on an unscanned issue, which
	// cannot change a row, and not the one the listing already settled.
	if fetched := scene.detailFetches(); fetched != 1 {
		t.Errorf("it made %d detail fetches, want 1: %v", fetched, scene.requests)
	}
}

// A detail fetch that fails leaves emptiness unknown, which counts as real
// work: guessing "empty" would hide the patch beside it.
func TestPatchesTreatsAnUnresolvableMergeRequestAsRealWork(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aPatchGitlab(t)
	scene.open = []map[string]any{aListedMergeRequest(11, 3200002)}
	scene.detailStatus = http.StatusInternalServerError

	code, stdout, stderr := runPatchesCommand(t,
		scriptedIssues{issues: aPatchQueue()}, scriptedClients{client: gitlabClientFor(scene.server)},
		"patches", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// Unknown counts as real work, so the row reads as a patch beside a merge
	// request rather than a patch beside an empty one. Guessing "empty" would
	// claim the branch carries nothing on no evidence at all.
	if strings.Contains(rowFor(t, stdout, "#3200002"), "empty") {
		t.Errorf("an unknown emptiness was read as empty:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 patch + MR") || strings.Contains(stdout, "empty MR") {
		t.Errorf("footer:\n%s", stdout)
	}
}

// A detail payload answering with some other merge request must not be written
// into this row under the listed one's identity.
func TestPatchesRefusesADetailPayloadForAnotherMergeRequest(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aPatchGitlab(t)
	scene.open = []map[string]any{aListedMergeRequest(11, 3200002)}
	// Answers 11 with 12's body, marked empty. Taking it would make the row
	// claim an emptiness that belongs to a different merge request.
	scene.detail = map[int]map[string]any{
		11: withDiff(aListedMergeRequest(12, 3200002), "same", "same"),
	}

	_, stdout, _ := runPatchesCommand(t,
		scriptedIssues{issues: aPatchQueue()}, scriptedClients{client: gitlabClientFor(scene.server)},
		"patches", "--cockpit="+root)

	if strings.Contains(stdout, "empty") {
		t.Errorf("a payload for another merge request was believed:\n%s", stdout)
	}
}

// --without-mr is only what no branch carries.
func TestPatchesNarrowsToWhatNoBranchCarries(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aPatchGitlab(t)
	scene.open = []map[string]any{aListedMergeRequest(11, 3200002)}
	scene.detail = map[int]map[string]any{
		11: withDiff(aListedMergeRequest(11, 3200002), "base", "head"),
	}

	code, stdout, stderr := runPatchesCommand(t,
		scriptedIssues{issues: aPatchQueue()}, scriptedClients{client: gitlabClientFor(scene.server)},
		"patches", "--without-mr", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if strings.Contains(stdout, "#3200002") {
		t.Errorf("an issue a merge request carries survived --without-mr:\n%s", stdout)
	}
	if !strings.Contains(stdout, "#3200001") {
		t.Errorf("a patch nobody branched was filtered out:\n%s", stdout)
	}
}

// A merged merge request is fetched too: an open issue whose work has already
// landed reads exactly like an untouched one otherwise.
func TestPatchesSeesWorkThatHasAlreadyLanded(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aPatchGitlab(t)
	landed := withDiff(aListedMergeRequest(7, 3200001), "base", "head")
	landed["state"] = "merged"
	landed["merged_at"] = "2026-06-12T10:00:00Z"
	scene.merged = []map[string]any{landed}

	code, stdout, stderr := runPatchesCommand(t,
		scriptedIssues{issues: aPatchQueue()}, scriptedClients{client: gitlabClientFor(scene.server)},
		"patches", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "!7 merged 2026-06-12") {
		t.Errorf("a landing was not reported:\n%s", stdout)
	}
}

// The snapshot is used when there is one: it already holds detail payloads, so
// emptiness is settled and no request is made at all.
func TestPatchesReadsTheSnapshotWithoutTouchingGitlab(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aPatchGitlab(t)
	where, _ := cockpit.New(root)

	snapshot := dashboard.ModuleSnapshot{
		FetchedAt:   time.Now(),
		ProjectData: map[string]any{"id": 1, "path": "pathauto"},
		MRData: []map[string]any{
			withDiff(aListedMergeRequest(11, 3200002), "same", "same"),
		},
	}
	if err := dashboard.NewCache(where.DashboardCachePath()).Save("pathauto", snapshot); err != nil {
		t.Fatalf("save: %v", err)
	}

	code, stdout, stderr := runPatchesCommand(t,
		scriptedIssues{issues: aPatchQueue()}, scriptedClients{client: gitlabClientFor(scene.server)},
		"patches", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "empty") {
		t.Errorf("the snapshot's settled emptiness was not used:\n%s", stdout)
	}
	if len(scene.requests) != 0 {
		t.Errorf("it went to GitLab with a snapshot in hand: %v", scene.requests)
	}
}

// GitLab being unreadable costs the cross-reference, never the list: every
// matching issue is still shown, uncorrelated.
func TestPatchesStillReportsWhenGitlabCannotBeRead(t *testing.T) {
	root := aDashboardCockpit(t)

	code, stdout, stderr := runPatchesCommand(t,
		scriptedIssues{issues: aPatchQueue()}, noClients{}, "patches", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, nid := range []string{"#3200001", "#3200002", "#3200003"} {
		if !strings.Contains(stdout, nid) {
			t.Errorf("%s was lost with the cross-reference:\n%s", nid, stdout)
		}
	}
}

// Nothing to report says so, rather than printing headers over nothing.
func TestPatchesSaysWhenEverythingIsCovered(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aPatchGitlab(t)

	code, stdout, stderr := runPatchesCommand(t, scriptedIssues{},
		scriptedClients{client: gitlabClientFor(scene.server)}, "patches", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if strings.Contains(stdout, "LATEST PATCH") {
		t.Errorf("it printed a table with no rows:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Nothing to report") {
		t.Errorf("stderr: %q", stderr)
	}
}

// A survey command narrows *into* the watchlist: a report of "every module"
// has to be a report of a set somebody declared.
func TestPatchesRefusesAModuleTheRegistryDoesNotCarry(t *testing.T) {
	root := aDashboardCockpit(t)

	code, _, stderr := runPatchesCommand(t, scriptedIssues{issues: aPatchQueue()},
		noClients{}, "patches", "--module=token", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "not registered") {
		t.Errorf("stderr: %q", stderr)
	}
}

// --module narrows to one, and modules are scanned in name order so two runs
// agree with each other.
func TestPatchesNarrowsToOneModuleAndScansInOrder(t *testing.T) {
	root := aDashboardCockpit(t)
	where, _ := cockpit.New(root)
	if err := os.WriteFile(where.RegistryPath(), []byte(
		"modules:\n"+
			"  token:\n    project: project/token\n    core_versions: [\"11\"]\n"+
			"  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\"]\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	scanned := &orderedIssues{scriptedIssues: scriptedIssues{issues: aPatchQueue()}}

	runPatchesCommand(t, scanned, noClients{}, "patches", "--cockpit="+root)
	if len(scanned.modules) != 2 || scanned.modules[0] != "pathauto" {
		t.Errorf("scanned %v", scanned.modules)
	}

	narrowed := &orderedIssues{scriptedIssues: scriptedIssues{issues: aPatchQueue()}}
	runPatchesCommand(t, narrowed, noClients{}, "patches", "--module=token", "--cockpit="+root)
	if len(narrowed.modules) != 1 || narrowed.modules[0] != "token" {
		t.Errorf("scanned %v", narrowed.modules)
	}
}

// Only the two statuses a contribution sits in: "what could I work on?" is a
// different question, and `issues` answers it.
func TestPatchesScansOnlyWhatAwaitsReview(t *testing.T) {
	root := aDashboardCockpit(t)
	scanned := &recordingIssues{scriptedIssues: scriptedIssues{issues: aPatchQueue()}}

	runPatchesCommand(t, scanned, noClients{}, "patches", "--cockpit="+root)

	if len(scanned.scanned) != 2 {
		t.Fatalf("it scanned %v", scanned.scanned)
	}
	for _, status := range scanned.scanned {
		if !status.NeedsMaintainer() {
			t.Errorf("it scanned %s, which is not waiting on a maintainer", status.ShortLabel())
		}
	}
}

// What drupal.org could not read is reported.
func TestPatchesReportsWhatTheScanCouldNotRead(t *testing.T) {
	root := aDashboardCockpit(t)

	_, _, stderr := runPatchesCommand(t,
		scriptedIssues{issues: aPatchQueue(), warnings: []string{"a request did not answer"}},
		noClients{}, "patches", "--cockpit="+root)

	if !strings.Contains(stderr, "a request did not answer") {
		t.Errorf("the warning was swallowed: %q", stderr)
	}
}

// --module completes, like the module argument everywhere else.
func TestThePatchesModuleFlagCompletes(t *testing.T) {
	root := aDashboardCockpit(t)

	code, stdout, stderr := invokeWith(t, NewRootFor(noVolumes{}, noSizer),
		cobra.ShellCompRequestCmd, "patches", "--cockpit="+root, "--module=")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "pathauto") {
		t.Errorf("it suggests no module:\n%s", stdout)
	}
}

// orderedIssues remembers which modules were scanned, in order.
type orderedIssues struct {
	scriptedIssues
	modules []string
}

func (o *orderedIssues) Issues() IssueSource { return o }

func (o *orderedIssues) ProjectIssues(name string, _ []drupal.IssueStatus) []drupal.Issue {
	o.modules = append(o.modules, name)

	return o.issues
}

// A landing with work posted after it is an ordinary open contribution again,
// and the cell says so rather than reading as finished.
func TestPatchesSaysWhenThereIsWorkNewerThanALanding(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aPatchGitlab(t)
	landed := withDiff(aListedMergeRequest(7, 3200001), "base", "head")
	landed["state"] = "merged"
	landed["merged_at"] = "2026-06-12T10:00:00Z"
	scene.merged = []map[string]any{landed}

	// A patch posted a day after the merge.
	queue := aPatchQueue()
	queue[0].Files[0].Timestamp = time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC).Unix()

	code, stdout, stderr := runPatchesCommand(t, scriptedIssues{issues: queue},
		scriptedClients{client: gitlabClientFor(scene.server)}, "patches", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(rowFor(t, stdout, "#3200001"), "newer work since") {
		t.Errorf("a landing with newer work reads as finished:\n%s", stdout)
	}
}

// Half of GitLab answering costs the cross-reference and not the list — the
// same degradation as none of it answering.
func TestPatchesDegradesWhenGitlabAnswersPartially(t *testing.T) {
	root := aDashboardCockpit(t)

	for name, breaks := range map[string]func(*patchGitlab){
		"the open merge requests do not list": func(s *patchGitlab) {
			s.openStatus = http.StatusInternalServerError
		},
		"the fork map does not answer": func(s *patchGitlab) {
			s.forkStatus = http.StatusInternalServerError
		},
	} {
		scene := aPatchGitlab(t)
		breaks(scene)

		code, stdout, stderr := runPatchesCommand(t, scriptedIssues{issues: aPatchQueue()},
			scriptedClients{client: gitlabClientFor(scene.server)}, "patches", "--cockpit="+root)

		if code != workflow.OK {
			t.Fatalf("%s: exit %d (%s)", name, code, stderr)
		}
		if !strings.Contains(stdout, "#3200001") {
			t.Errorf("%s: the list was lost with the cross-reference:\n%s", name, stdout)
		}
	}
}

// One issue is an issue, not "1 issues", and a module a module.
func TestPatchesCountsOneOfEachInTheSingular(t *testing.T) {
	root := aDashboardCockpit(t)

	_, stdout, _ := runPatchesCommand(t,
		scriptedIssues{issues: aPatchQueue()[:1]}, noClients{}, "patches", "--cockpit="+root)

	if !strings.Contains(stdout, "1 issue ·") || !strings.Contains(stdout, "1 module ·") {
		t.Errorf("footer:\n%s", stdout)
	}
}

// An issue carrying no patch at all prints the placeholder rather than a zero:
// "0" reads as a measurement, and there was nothing to measure.
func TestPatchesPrintsAPlaceholderRatherThanAZeroCount(t *testing.T) {
	root := aDashboardCockpit(t)

	_, stdout, _ := runPatchesCommand(t,
		scriptedIssues{issues: aPatchQueue()}, noClients{}, "patches", "--cockpit="+root)

	row := rowFor(t, stdout, "#3200004")
	if strings.Contains(row, " 0 ") {
		t.Errorf("a zero patch count was printed: %q", row)
	}
	if !strings.Contains(row, "–") {
		t.Errorf("no placeholder for an issue with nothing attached: %q", row)
	}
}

// The colouring, which is invisible in every other test here because a test's
// output is not a terminal — and which must never change a cell's visible
// length, since the table measures the raw cells.
func TestThePatchTableColoursWhatMatters(t *testing.T) {
	palette := cli.NewPalette(true)

	for name, run := range map[string]struct {
		cells  []string
		column int
		colour cli.Colour
	}{
		// An empty MR is why the patch beside it has gone unreviewed, so it is
		// the actionable cell on the row and gets the warning colour.
		"an empty merge request": {
			cells:  []string{"pathauto", "#1", "review", "1", "a.patch", "!11 empty", "Title"},
			column: patchMRColumn, colour: cli.Yellow,
		},
		// Merged with nothing since needs no work, and the colour says so
		// before the words are read.
		"a landing": {
			cells:  []string{"pathauto", "#1", "review", "1", "a.patch", "!7 merged 2026-06-12", "Title"},
			column: patchMRColumn, colour: cli.Green,
		},
		// With newer work since, it is an ordinary open contribution again.
		"a landing with newer work": {
			cells: []string{
				"pathauto", "#1", "review", "1", "a.patch", "!7 merged 2026-06-12, newer work since", "Title",
			},
			column: patchMRColumn, colour: cli.Yellow,
		},
		"no merge request at all": {
			cells:  []string{"pathauto", "#1", "review", "1", "a.patch", "–", "Title"},
			column: patchMRColumn, colour: cli.Grey,
		},
		"RTBC is somebody waiting on you": {
			cells:  []string{"pathauto", "#1", "RTBC", "1", "a.patch", "–", "Title"},
			column: patchStatusColumn, colour: cli.Green,
		},
		"review": {
			cells:  []string{"pathauto", "#1", "review", "1", "a.patch", "–", "Title"},
			column: patchStatusColumn, colour: cli.Yellow,
		},
		"a missing patch count": {
			cells:  []string{"pathauto", "#1", "review", "–", "–", "–", "Title"},
			column: patchCountColumn, colour: cli.Grey,
		},
	} {
		painted := colourPatchRow(palette, run.cells)

		if !strings.HasPrefix(painted[run.column], string(run.colour)) {
			t.Errorf("%s: column %d is %q", name, run.column, painted[run.column])
		}
		// The raw cell is still in there whole: the table measures raw widths
		// and a decorated cell of a different visible length would push every
		// column after it out of line.
		if !strings.Contains(painted[run.column], run.cells[run.column]) {
			t.Errorf("%s: the cell was rewritten: %q", name, painted[run.column])
		}
		if len(painted) != len(run.cells) {
			t.Errorf("%s: the row changed length", name)
		}
	}
}

// A row with nothing worth colouring comes back exactly as it went in.
func TestThePatchTableLeavesAnOrdinaryRowAlone(t *testing.T) {
	cells := []string{"pathauto", "#1", "needs work", "2", "a.patch", "!11", "Title"}

	painted := colourPatchRow(cli.NewPalette(true), cells)

	for i := range cells {
		if painted[i] != cells[i] {
			t.Errorf("column %d was decorated: %q", i, painted[i])
		}
	}
}

// Modules are counted once each however many rows they contribute, and the
// patch filename is truncated to its own width rather than the title's — a
// drupal.org patch name routinely runs past thirty characters, and letting it
// take the title's width would push the title off the line.
func TestPatchesCountsModulesOnceAndKeepsThePatchColumnNarrow(t *testing.T) {
	root := aDashboardCockpit(t)
	where, _ := cockpit.New(root)
	if err := os.WriteFile(where.RegistryPath(), []byte(
		"modules:\n"+
			"  token:\n    project: project/token\n    core_versions: [\"11\"]\n"+
			"  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\"]\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// 34 characters: past the patch column's width, inside the title's.
	long := "3200001-17-d12-compat-reroll.patch"
	// Two issues per module, so the row count and the module count differ —
	// otherwise counting rows would give the right answer by accident.
	queue := aPatchQueue()[:2]
	queue[0].Files = []drupal.IssueFile{{Name: long, URL: "https://drupal.org/files/a.patch"}}

	_, stdout, _ := runPatchesCommand(t,
		scriptedIssues{issues: queue}, noClients{}, "patches", "--cockpit="+root)

	if !strings.Contains(stdout, "4 issues") || !strings.Contains(stdout, "2 modules") {
		t.Errorf("footer:\n%s", stdout)
	}
	if strings.Contains(stdout, long) {
		t.Errorf("a long patch name was not truncated:\n%s", stdout)
	}
	if !strings.Contains(stdout, "3200001-17-d12-compat") {
		t.Errorf("the patch name is unrecognisable after truncation:\n%s", stdout)
	}
}

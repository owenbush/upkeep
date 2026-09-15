package command

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/dashboard"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
	"github.com/owenbush/upkeep/internal/workflow"
)

// scriptedIssues answers a drupal.org scan without a network, and records how
// often it was asked.
type scriptedIssues struct {
	issues   []drupal.Issue
	warnings []string
	scans    *int
}

func (s scriptedIssues) Issues() dashboard.IssueReader { return s }

func (s scriptedIssues) Warnings() []string { return s.warnings }

func (s scriptedIssues) ProjectIssues(string, []drupal.IssueStatus) []drupal.Issue {
	if s.scans != nil {
		*s.scans++
	}

	return s.issues
}

// gitlabScript is what the stub answers beyond the open merge requests.
type gitlabScript struct {
	merged []map[string]any
	// forks is the fork listing, from which a merge request is paired to its
	// issue: the path carries the nid, and the id is what a merge request's
	// source project points at.
	forks     []map[string]any
	forksFail bool
	// detailPipeline is added to the single-merge-request payload only, so a
	// command that skipped the detail fetch is visibly missing it.
	detailPipeline map[string]any
}

// aDashboardGitlab answers the requests a refresh makes, and counts them.
func aDashboardGitlab(t *testing.T, mergeRequests []map[string]any) (*httptest.Server, *int) {
	return aScriptedGitlab(t, mergeRequests, gitlabScript{})
}

func aScriptedGitlab(
	t *testing.T, mergeRequests []map[string]any, script gitlabScript,
) (*httptest.Server, *int) {
	t.Helper()

	if mergeRequests == nil {
		// An empty list, not a null: "no merge requests" and "the endpoint
		// answered with nothing usable" are different, and a stub that
		// conflated them would test the second while claiming the first.
		mergeRequests = []map[string]any{}
	}

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.EscapedPath()

		switch {
		// Any watched module, so a test about *which* modules were refreshed
		// is not quietly a test about one of them failing to resolve.
		case strings.HasPrefix(path, "/api/v4/projects/project%2F") &&
			!strings.Contains(strings.TrimPrefix(path, "/api/v4/projects/project%2F"), "/"):
			name := strings.TrimPrefix(path, "/api/v4/projects/project%2F")
			_, _ = w.Write([]byte(`{"id":1,"path":"` + name + `","path_with_namespace":"project/` + name + `",` +
				`"web_url":"https://git.drupalcode.org/project/` + name + `","default_branch":"2.0.x"}`))

		case path == "/api/v4/projects/1/merge_requests" && r.URL.Query().Get("state") != "merged":
			body, _ := json.Marshal(mergeRequests)
			_, _ = w.Write(body)

		case path == "/api/v4/projects/1/merge_requests" && r.URL.Query().Get("state") == "merged":
			body, _ := json.Marshal(orEmpty(script.merged))
			_, _ = w.Write(body)

		case strings.HasSuffix(path, "/forks"):
			if script.forksFail {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"403 Forbidden"}`))

				return
			}
			body, _ := json.Marshal(orEmpty(script.forks))
			_, _ = w.Write(body)

		case strings.HasPrefix(path, "/api/v4/projects/1/merge_requests/"):
			iid := strings.TrimPrefix(path, "/api/v4/projects/1/merge_requests/")
			if strings.HasSuffix(iid, "/merge_ref") {
				_, _ = w.Write([]byte(`{"commit_id":"0f1e2d3c4b5a0f1e2d3c4b5a0f1e2d3c4b5a0f1e"}`))

				return
			}
			for _, one := range mergeRequests {
				if fmt.Sprint(one["iid"]) != iid {
					continue
				}
				detailed := map[string]any{}
				for key, value := range one {
					detailed[key] = value
				}
				if script.detailPipeline != nil {
					detailed["head_pipeline"] = script.detailPipeline
				}
				body, _ := json.Marshal(detailed)
				_, _ = w.Write(body)

				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404"}`))

		default:
			// Everything else — forks, merged listings, raw files — answers
			// empty. Each is a documented degradation, and a refresh that
			// depended on one would be a refresh that fails on a private
			// project.
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(server.Close)

	return server, &calls
}

// orEmpty renders nil as an empty JSON value rather than null: "there are
// none" and "the endpoint answered with nothing usable" are different, and a
// stub that conflated them would test the second while claiming the first.
func orEmpty(value any) any {
	switch typed := value.(type) {
	case []map[string]any:
		if typed == nil {
			return []map[string]any{}
		}
	case map[string]any:
		if typed == nil {
			return map[string]any{}
		}
	}

	return value
}

// aDashboardCockpit is a cockpit watching one module.
func aDashboardCockpit(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	root := filepath.Join(home, "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}
	where, _ := cockpit.New(root)
	if err := os.WriteFile(where.RegistryPath(), []byte(
		"modules:\n  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\"]\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return root
}

// aBotMergeRequest is the shape the fast lane exists for.
func aBotMergeRequest(iid int) map[string]any {
	return map[string]any{
		"iid": iid, "title": botTitle, "state": "opened",
		"source_branch": botBranch, "target_branch": "2.0.x",
		"sha": greenHead, "draft": false,
		"author":        map[string]any{"username": botUser, "id": botID},
		"web_url":       "https://git.drupalcode.org/project/pathauto/-/merge_requests/1",
		"head_pipeline": map[string]any{"status": "success", "web_url": "https://ci/1"},
	}
}

// runDashboardCommand invokes the tree against a stub GitLab and drupal.org.
func runDashboardCommand(
	t *testing.T, server *httptest.Server, issues IssueClients, args ...string,
) (int, string, string) {
	t.Helper()

	root := NewRoot(
		&fakeFactory{engine: &fakeEngine{}},
		scriptedClients{client: gitlabClientFor(server)},
		issues, noPrompts, someVolumes(nil), noSizer,
	)

	return invokeWith(t, root, args...)
}

// The overview: one line per module, and a hint about how to see more.
func TestTheDashboardSummarisesEveryModule(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, []map[string]any{aBotMergeRequest(1)})

	code, stdout, stderr := runDashboardCommand(t, server, noIssues{},
		"dashboard", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, expected := range []string{"MODULE", "READY", "UNCHECKED", "pathauto", "--refresh to update"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the overview is missing %q:\n%s", expected, stdout)
		}
	}
	if !strings.Contains(stdout, "dashboard <module>") {
		t.Errorf("it did not say how to see the rows:\n%s", stdout)
	}
	// The overview is a summary: no per-row columns.
	if strings.Contains(stdout, "NEXT") {
		t.Errorf("the overview printed the detailed table:\n%s", stdout)
	}
}

// Naming a module drills into its rows, each carrying what to run for it.
func TestNamingAModuleShowsItsRowsAndWhatToDo(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, []map[string]any{aBotMergeRequest(1)})

	code, stdout, stderr := runDashboardCommand(t, server, noIssues{},
		"dashboard", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, expected := range []string{"MODULE", "STATUS", "NEXT", "upkeep check"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the drill-down is missing %q:\n%s", expected, stdout)
		}
	}
	// And the hints under it, which the overview has always had and the
	// drill-down did not.
	if !strings.Contains(stdout, "upkeep explain") {
		t.Errorf("no hints under the table:\n%s", stdout)
	}
	if !strings.Contains(stdout, "-v shows the gate") {
		t.Errorf("it did not mention -v:\n%s", stdout)
	}
}

// A module the cockpit does not watch is a mistake rather than a wider
// question: drilling in is a request to see something the dashboard is
// showing.
func TestDrillingIntoAnUnwatchedModuleIsRefused(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, nil)

	code, stdout, stderr := runDashboardCommand(t, server, noIssues{},
		"dashboard", "webform", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "pathauto") {
		t.Errorf("the refusal does not list what is registered: %q", stderr)
	}
	if stdout != "" {
		t.Errorf("it produced a table anyway: %q", stdout)
	}
}

// Cached by default: a second run goes nowhere near the network.
func TestASecondRunUsesTheCache(t *testing.T) {
	root := aDashboardCockpit(t)
	server, calls := aDashboardGitlab(t, []map[string]any{aBotMergeRequest(1)})
	scans := 0
	issues := scriptedIssues{scans: &scans}

	if code, _, stderr := runDashboardCommand(t, server, issues,
		"dashboard", "--cockpit="+root); code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	first, firstScans := *calls, scans
	if first == 0 || firstScans == 0 {
		t.Fatalf("the first run fetched nothing: %d requests, %d scans", first, firstScans)
	}

	if code, _, _ := runDashboardCommand(t, server, issues,
		"dashboard", "--cockpit="+root); code != workflow.OK {
		t.Fatalf("second run exit %d", code)
	}

	if *calls != first {
		t.Errorf("a cached run made %d more requests", *calls-first)
	}
	if scans != firstScans {
		t.Errorf("a cached run scanned drupal.org %d more times", scans-firstScans)
	}
}

// --refresh goes to the network again; naming a module with it refreshes that
// one, because refreshing a whole cockpit to look at one module would be the
// expensive half of a command whose point was to be specific.
func TestRefreshTargetsWhatWasAskedFor(t *testing.T) {
	root := aDashboardCockpit(t)
	where, _ := cockpit.New(root)
	// A second watched module, so "all" and "one" differ.
	if err := os.WriteFile(where.RegistryPath(), []byte(
		"modules:\n"+
			"  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\"]\n"+
			"  token:\n    project: project/token\n    core_versions: [\"11\"]\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	server, _ := aDashboardGitlab(t, []map[string]any{aBotMergeRequest(1)})
	scans := 0
	issues := scriptedIssues{scans: &scans}

	// Prime both.
	runDashboardCommand(t, server, issues, "dashboard", "--cockpit="+root)
	primed := scans

	// A bare --refresh with no module: everything.
	runDashboardCommand(t, server, issues, "dashboard", "--refresh", "--cockpit="+root)
	if scans-primed != 2 {
		t.Errorf("a bare --refresh scanned %d modules, want 2", scans-primed)
	}

	// A bare --refresh alongside a module: just that one.
	atModule := scans
	runDashboardCommand(t, server, issues, "dashboard", "pathauto", "--refresh", "--cockpit="+root)
	if scans-atModule != 1 {
		t.Errorf("--refresh on one module scanned %d, want 1", scans-atModule)
	}

	// And naming one explicitly.
	atNamed := scans
	runDashboardCommand(t, server, issues, "dashboard", "--refresh=token", "--cockpit="+root)
	if scans-atNamed != 1 {
		t.Errorf("--refresh=token scanned %d, want 1", scans-atNamed)
	}
}

// Nothing open is a sentence, not an empty table — and it says what it was
// narrowed to.
func TestNothingOpenSaysSo(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, nil)

	_, stdout, _ := runDashboardCommand(t, server, noIssues{}, "dashboard", "--cockpit="+root)
	if !strings.Contains(stdout, "No open contributions across") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if strings.Contains(stdout, "MODULE") {
		t.Errorf("it printed an empty table:\n%s", stdout)
	}

	_, filtered, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "--version=9", "--cockpit="+root)
	if !strings.Contains(filtered, "targeting core 9") {
		t.Errorf("it did not say what it was narrowed to:\n%s", filtered)
	}
}

// A module GitLab will not answer for becomes a row saying so, rather than
// vanishing from the table — a module disappearing is the worst failure this
// tool has.
func TestAModuleThatCannotBeFetchedBecomesARowRatherThanVanishing(t *testing.T) {
	root := aDashboardCockpit(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Project Not Found"}`))
	}))
	t.Cleanup(server.Close)

	code, stdout, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "--all", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stdout, "pathauto") {
		t.Errorf("the module vanished:\n%s", stdout)
	}
}

// Colour is written only to a terminal: a table piped into a file or a pager
// should contain the table, not escape sequences around it.
func TestNoColourReachesARedirectedTable(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, []map[string]any{aBotMergeRequest(1)})

	_, stdout, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "pathauto", "--cockpit="+root)

	if strings.Contains(stdout, "\x1b[") {
		t.Errorf("escape sequences reached a redirected table:\n%q", stdout)
	}
}

// What the drupal.org scan could not read is said, because that client
// degrades by returning less data — which at the call site is
// indistinguishable from there being less data.
func TestAPartialScanIsReported(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, []map[string]any{aBotMergeRequest(1)})

	_, stdout, stderr := runDashboardCommand(t, server,
		scriptedIssues{warnings: []string{"a request did not answer"}},
		"dashboard", "--cockpit="+root)

	if !strings.Contains(stderr, "did not answer") {
		t.Errorf("the shortfall was silent: %q", stderr)
	}
	if !strings.Contains(stderr, "lower bounds") {
		t.Errorf("the counts were not named as lower bounds: %q", stderr)
	}
	if strings.Contains(stdout, "did not answer") {
		t.Errorf("a diagnostic reached the table:\n%s", stdout)
	}
}

// The overview is aggregated from exactly the rows the drill-down prints: a
// module whose overview says one ready must show one ready when drilled into.
func TestTheOverviewAgreesWithTheDrillDown(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, []map[string]any{
		aBotMergeRequest(1), aBotMergeRequest(2),
	})

	zero := 0
	where, _ := cockpit.New(root)
	for _, iid := range []int{1, 2} {
		key, _ := results.MergeRequestKey(iid)
		if err := results.NewCache(where.ResultsPath()).Store(
			"pathauto", key, "11", "0f1e2d3c4b5a0f1e2d3c4b5a0f1e2d3c4b5a0f1e",
			check.RunResult{Results: []check.Result{
				{Type: check.PhpCs, Status: check.Passed, ExitCode: &zero},
			}}, time.Now(),
		); err != nil {
			t.Fatalf("store: %v", err)
		}
	}

	_, overview, _ := runDashboardCommand(t, server, noIssues{}, "dashboard", "--cockpit="+root)
	_, detail, _ := runDashboardCommand(t, server, noIssues{}, "dashboard", "pathauto", "--cockpit="+root)

	// Counted over the table's own rows, not over the whole output: the hint
	// beneath it says "2 rows ready to merge", which a substring count would
	// add to the total it is describing.
	readyInDetail := 0
	for _, line := range strings.Split(detail, "\n") {
		if strings.HasPrefix(line, "pathauto") && strings.Contains(line, "ready to merge") {
			readyInDetail++
		}
	}
	if readyInDetail == 0 {
		t.Fatalf("nothing was ready in the drill-down:\n%s", detail)
	}

	if got := columnIn(overview, "pathauto", "READY"); got != fmt.Sprint(readyInDetail) {
		t.Errorf("the overview says %q ready, the drill-down shows %d:\n%s",
			got, readyInDetail, overview)
	}
	// And the hint under the drill-down says what to do with them.
	if !strings.Contains(detail, "upkeep merge --fast-lane") {
		t.Errorf("the drill-down did not offer the fast lane:\n%s", detail)
	}
}

// -v swaps the plain-English status for the gate's own reason tokens.
func TestVerboseShowsTheGatesOwnTokens(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, []map[string]any{aBotMergeRequest(1)})

	_, plain, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "pathauto", "--cockpit="+root)
	_, verbose, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "pathauto", "-v", "--cockpit="+root)

	if plain == verbose {
		t.Errorf("-v changed nothing:\n%s", plain)
	}
	if !strings.Contains(verbose, "REVIEW") && !strings.Contains(verbose, "READY-AUTO") {
		t.Errorf("-v did not show the gate's tokens:\n%s", verbose)
	}
	// And it stops offering itself.
	if strings.Contains(verbose, "-v shows the gate") {
		t.Errorf("-v still advertised -v:\n%s", verbose)
	}
}

// gitlabClientFor points a client at a stub.
func gitlabClientFor(server *httptest.Server) *gitlab.Client {
	return gitlab.NewClient(nil, "", server.URL+"/api/v4", server.URL)
}

// twoWatchedModules is a cockpit watching two, so "narrowed to one" is
// observable.
func twoWatchedModules(t *testing.T) string {
	t.Helper()

	root := aDashboardCockpit(t)
	where, _ := cockpit.New(root)
	if err := os.WriteFile(where.RegistryPath(), []byte(
		"modules:\n"+
			"  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\"]\n"+
			"  token:\n    project: project/token\n    core_versions: [\"11\"]\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return root
}

// Naming a module narrows the whole run to it: the other watched modules are
// not shown, and not fetched either.
func TestNamingAModuleNarrowsTheWholeRun(t *testing.T) {
	root := twoWatchedModules(t)
	server, _ := aDashboardGitlab(t, []map[string]any{aBotMergeRequest(1)})
	scans := 0

	_, stdout, _ := runDashboardCommand(t, server, scriptedIssues{scans: &scans},
		"dashboard", "pathauto", "--cockpit="+root)

	// Checked over the table's rows rather than the whole output: the hint
	// beneath it mentions "reason tokens", which a substring test reads as the
	// module named token.
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "token") {
			t.Errorf("a module nobody asked about was shown:\n%s", stdout)
		}
	}
	if scans != 1 {
		t.Errorf("it scanned %d modules for a run narrowed to one", scans)
	}
}

// --all is every row of every module, without naming one.
func TestAllShowsEveryRowOfEveryModule(t *testing.T) {
	root := twoWatchedModules(t)
	server, _ := aDashboardGitlab(t, []map[string]any{aBotMergeRequest(1)})

	_, overview, _ := runDashboardCommand(t, server, noIssues{}, "dashboard", "--cockpit="+root)
	_, all, _ := runDashboardCommand(t, server, noIssues{}, "dashboard", "--all", "--cockpit="+root)

	if !strings.Contains(all, "NEXT") {
		t.Errorf("--all did not show the detailed table:\n%s", all)
	}
	if strings.Contains(overview, "NEXT") {
		t.Errorf("the overview showed the detailed table:\n%s", overview)
	}
	for _, module := range []string{"pathauto", "token"} {
		if !strings.Contains(all, module) {
			t.Errorf("--all left out %s:\n%s", module, all)
		}
	}
}

// --refresh= names no module, which is a mistake worth saying rather than a
// silent no-op.
func TestAnEmptyRefreshIsAMistakeRatherThanANoOp(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, nil)

	code, stdout, stderr := runDashboardCommand(t, server, noIssues{},
		"dashboard", "--refresh=", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "--refresh=<module>") {
		t.Errorf("the refusal does not show the shapes: %q", stderr)
	}
	if stdout != "" {
		t.Errorf("it ran anyway: %q", stdout)
	}
}

// --no-patches keeps the merge-request-only view, as a filter over one row set
// rather than a second one left out.
func TestNoPatchesDropsPatchRowsAndKeepsFailures(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, nil)

	// An issue with a patch and no merge request: a patch row.
	issues := scriptedIssues{issues: []drupal.Issue{{
		Nid: 3601234, Title: "Fix the token cache", Status: drupal.StatusNeedsReview,
		Version: "2.0.x",
		Files: []drupal.IssueFile{{
			Name: "fix-3601234-2.patch",
			URL:  "https://www.drupal.org/files/issues/fix-3601234-2.patch",
		}},
	}}}

	_, withPatches, _ := runDashboardCommand(t, server, issues,
		"dashboard", "pathauto", "--cockpit="+root)
	if !strings.Contains(withPatches, "3601234") {
		t.Fatalf("the patch row is missing:\n%s", withPatches)
	}

	_, without, _ := runDashboardCommand(t, server, issues,
		"dashboard", "pathauto", "--no-patches", "--cockpit="+root)
	if strings.Contains(without, "3601234") {
		t.Errorf("--no-patches still showed a patch row:\n%s", without)
	}
}

// A module that could not be fetched is still reported under --no-patches: it
// has no merge request either, and dropping it would hide a failure.
func TestNoPatchesKeepsAModuleFailure(t *testing.T) {
	root := aDashboardCockpit(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Project Not Found"}`))
	}))
	t.Cleanup(server.Close)

	_, stdout, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "--all", "--no-patches", "--cockpit="+root)

	if !strings.Contains(stdout, "pathauto") {
		t.Errorf("--no-patches hid a module failure:\n%s", stdout)
	}
}

// --version narrows the rows to those targeting one core.
func TestTheVersionFilterNarrowsTheRows(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, []map[string]any{aBotMergeRequest(1)})

	_, unfiltered, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "pathauto", "--cockpit="+root)
	if !strings.Contains(unfiltered, "READY") && !strings.Contains(unfiltered, "check") {
		t.Fatalf("nothing to filter:\n%s", unfiltered)
	}

	_, filtered, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "pathauto", "--version=9", "--cockpit="+root)

	if !strings.Contains(filtered, "targeting core 9") {
		t.Errorf("a core nobody tracks still produced rows:\n%s", filtered)
	}
}

// The fast-lane hint appears only when something is actually ready: offering
// it over nothing sends somebody to a command that will say "no READY-AUTO
// rows".
func TestTheFastLaneHintOnlyAppearsWhenSomethingIsReady(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aDashboardGitlab(t, []map[string]any{aBotMergeRequest(1)})

	// Nothing checked, so nothing ready.
	_, stdout, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "pathauto", "--cockpit="+root)

	if strings.Contains(stdout, "merge --fast-lane") {
		t.Errorf("the fast lane was offered with nothing ready:\n%s", stdout)
	}
	if !strings.Contains(stdout, "upkeep explain") {
		t.Errorf("the other hints went missing too:\n%s", stdout)
	}
}

// The footer counts what is open, not what has already landed.
func TestTheFooterCountsOpenMergeRequestsOnly(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aScriptedGitlab(t, []map[string]any{aBotMergeRequest(1)}, gitlabScript{
		merged: []map[string]any{{
			"iid": 99, "title": "Already landed", "state": "merged",
			"source_branch": "issue/pathauto-3601234", "target_branch": "2.0.x",
			"author": map[string]any{"username": "someone", "id": 1},
		}},
	})

	_, stdout, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "pathauto", "--cockpit="+root)

	if !strings.Contains(stdout, "1 open MRs") {
		t.Errorf("the footer counted a merged merge request as open:\n%s", stdout)
	}
}

// A fork map that cannot be read costs the pairing and nothing else: it is the
// only thing that pairs a bot merge request to its issue, and a module
// vanishing over it would be the worst failure this tool has.
func TestAForkMapThatCannotBeReadDoesNotLoseTheModule(t *testing.T) {
	root := aDashboardCockpit(t)
	server, _ := aScriptedGitlab(t, []map[string]any{aBotMergeRequest(1)},
		gitlabScript{forksFail: true})

	code, stdout, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stdout, "pathauto") {
		t.Errorf("the module vanished when its forks could not be read:\n%s", stdout)
	}
}

// The single-merge-request endpoint is what carries the head pipeline; the
// listing does not. Without that fetch the CI column is empty on every row.
func TestTheDetailFetchIsWhatSuppliesTheCiColumn(t *testing.T) {
	root := aDashboardCockpit(t)

	listedWithoutPipeline := map[string]any{
		"iid": 1, "title": botTitle, "state": "opened",
		"source_branch": botBranch, "target_branch": "2.0.x", "sha": greenHead,
		"author": map[string]any{"username": botUser, "id": botID},
	}
	server, _ := aScriptedGitlab(t, []map[string]any{listedWithoutPipeline}, gitlabScript{
		detailPipeline: map[string]any{"status": "failed", "web_url": "https://ci/1"},
	})

	_, stdout, _ := runDashboardCommand(t, server, noIssues{},
		"dashboard", "pathauto", "--cockpit="+root)

	if got := columnIn(stdout, "pathauto", "CI"); got != "fail" {
		t.Errorf("the CI column says %q — the detail fetch was skipped:\n%s", got, stdout)
	}
}

// The footer counts what is open, not what has already landed — including
// when a landed merge request is attached to a row, which is the case that
// makes the distinction observable.
func TestTheFooterCountsOpenOnlyEvenOnALandedRow(t *testing.T) {
	root := aDashboardCockpit(t)

	// A merged merge request paired to an open issue by its fork: the shape a
	// bot compatibility issue is in after its work lands.
	server, _ := aScriptedGitlab(t, []map[string]any{aBotMergeRequest(1)}, gitlabScript{
		merged: []map[string]any{{
			"iid": 99, "title": "Fix the token cache (#3601234)", "state": "merged",
			"source_branch": "3601234-fix", "target_branch": "2.0.x",
			"author":            map[string]any{"username": "someone", "id": 1},
			"source_project_id": 77,
			"merged_at":         "2026-06-12T10:00:00Z",
		}},
		forks: []map[string]any{
			{"id": 77, "path_with_namespace": "issue/pathauto-3601234"},
		},
	})

	issues := scriptedIssues{issues: []drupal.Issue{{
		Nid: 3601234, Title: "Fix the token cache", Status: drupal.StatusNeedsReview,
		Version: "2.0.x",
	}}}

	_, stdout, _ := runDashboardCommand(t, server, issues,
		"dashboard", "pathauto", "--cockpit="+root)

	// One open merge request — the bot's — and never two.
	if !strings.Contains(stdout, "1 open MRs") {
		t.Errorf("the footer counted a landed merge request as open:\n%s", stdout)
	}
}

// The footer counts the work, not the table's shape.
func TestTheFooterCountsIdentitiesNotRows(t *testing.T) {
	merged := gitlab.MergeRequest{IID: 99, State: "merged"}
	open1 := gitlab.MergeRequest{IID: 1, State: "opened"}
	open2 := gitlab.MergeRequest{IID: 2, State: "opened"}

	rows := []dashboard.Row{
		// One issue's work on two branches: two rows, one merge request.
		{Module: "pathauto", IssueNid: 3601234, PatchCount: 2, MergeRequests: []gitlab.MergeRequest{open1}},
		{Module: "pathauto", IssueNid: 3601234, PatchCount: 2, MergeRequests: []gitlab.MergeRequest{open1}},
		// A landed one, attached because it landed.
		{Module: "pathauto", IssueNid: 3609999, MergeRequests: []gitlab.MergeRequest{merged}},
		// Another module.
		{Module: "token", MergeRequests: []gitlab.MergeRequest{open2}},
	}

	openMRs, patchIssues, modules := footerCounts(rows)

	if openMRs != 2 {
		t.Errorf("%d open merge requests, want 2 — a landed one is not open, and one "+
			"appearing twice is still one", openMRs)
	}
	if patchIssues != 1 {
		t.Errorf("%d patch issues, want 1", patchIssues)
	}
	if modules != 2 {
		t.Errorf("%d modules, want 2", modules)
	}
}

// The colouring rules, read off the cells rather than through a whole run: a
// row's colour is a statement about what the cell says, and the cases that
// matter — a landing, a partial pass, the patch arrow — are awkward to stage
// end to end and trivial to state here.
func TestTheRowColouringSaysWhatEachCellMeans(t *testing.T) {
	on := cli.NewPalette(true)

	// A row where every interesting cell is in an interesting state.
	cells := make([]string, colNext+1)
	cells[colMR] = "!12 merged 2026-06-12"
	cells[colPatch] = "2 ↑"
	cells[colCI] = "fail"
	cells[colLocal] = "pass 11 · ? 10"
	cells[colStatus] = "checks are stale"
	cells[colNext] = "upkeep check pathauto 12"

	painted := colourRow(on, cells)

	for name, check := range map[string]struct {
		cell   int
		colour cli.Colour
	}{
		"a landing is green":          {colMR, cli.Green},
		"red CI is red":               {colCI, cli.Red},
		"a partial pass is amber":     {colLocal, cli.Yellow},
		"a stale status is amber":     {colStatus, cli.Yellow},
		"the next command stands out": {colNext, cli.Cyan},
	} {
		if !strings.Contains(painted[check.cell], string(check.colour)) {
			t.Errorf("%s: got %q", name, painted[check.cell])
		}
	}

	// The patch arrow is marked, not the count around it: the number is
	// context, the arrow is the claim that the branch is behind the issue.
	if !strings.Contains(painted[colPatch], string(cli.Yellow)+"↑") {
		t.Errorf("the arrow was not marked: %q", painted[colPatch])
	}
	if strings.HasPrefix(painted[colPatch], string(cli.Yellow)) {
		t.Errorf("the count was painted too: %q", painted[colPatch])
	}

	// And nothing painted changes what the cell says.
	for i, cell := range cells {
		if stripEscapes(painted[i]) != cell {
			t.Errorf("cell %d painted to %q, want %q", i, stripEscapes(painted[i]), cell)
		}
	}
}

// Both status vocabularies read the same way: -v swaps the plain-English
// phrase for the gate's own tokens, and green still means go.
func TestBothStatusVocabulariesColourTheSame(t *testing.T) {
	for _, pair := range [][2]string{
		{"READY-AUTO", "ready to merge"},
		{"BLOCKED ci-red", "CI failed"},
		{"REVIEW local-stale", "checks are stale"},
	} {
		token, phrase := statusColour(pair[0]), statusColour(pair[1])
		if token != phrase {
			t.Errorf("%q is %q and %q is %q — the two vocabularies disagree",
				pair[0], token, pair[1], phrase)
		}
		if token == "" {
			t.Errorf("%q got no colour at all", pair[0])
		}
	}

	// A status nobody has a rule for is left alone rather than guessed at.
	if statusColour("something new") != "" {
		t.Error("an unknown status was coloured")
	}
}

// A green LOCAL, a red one, and nothing at all.
func TestTheLocalCellIsColouredByItsLeadingWord(t *testing.T) {
	on := cli.NewPalette(true)

	for cell, want := range map[string]cli.Colour{
		"pass 10,11":     cli.Green,
		"fail 10":        cli.Red,
		"pass 11 · ? 10": cli.Yellow,
		"–":              cli.Grey,
	} {
		cells := make([]string, colNext+1)
		cells[colLocal] = cell
		painted := colourRow(on, cells)

		if !strings.Contains(painted[colLocal], string(want)) {
			t.Errorf("%q painted as %q, want %s", cell, painted[colLocal], want)
		}
	}
}

// The overview's three verdict cells carry colour; a dash is muted whatever
// column it is in.
func TestTheOverviewColoursTheThreeCellsThatCarryAVerdict(t *testing.T) {
	on := cli.NewPalette(true)

	cells := []string{"pathauto", "2", "3", "1", "2", "1", "4", "2h ago"}
	painted := colourOverview(on, cells)

	for name, check := range map[string]struct {
		cell   int
		colour cli.Colour
	}{
		"ready is green":     {sumReady, cli.Green},
		"CI failed is red":   {sumCIFailed, cli.Red},
		"unchecked is amber": {sumUnchecked, cli.Yellow},
		"cached is muted":    {sumCached, cli.Grey},
	} {
		if !strings.Contains(painted[check.cell], string(check.colour)) {
			t.Errorf("%s: got %q", name, painted[check.cell])
		}
	}

	// A dash is muted rather than green: zero ready is not something to
	// celebrate.
	dashes := []string{"pathauto", "–", "–", "–", "–", "–", "–", "never"}
	mutedDashes := colourOverview(on, dashes)
	for _, column := range []int{sumBranches, sumMRs, sumPatchIssues, sumReady, sumCIFailed, sumUnchecked} {
		if !strings.Contains(mutedDashes[column], string(cli.Grey)) {
			t.Errorf("column %d: a dash was painted %q", column, mutedDashes[column])
		}
	}
}

// The production drupal.org source hands out a usable reader and starts with
// nothing to report.
func TestTheDrupalClientSourceIsUsable(t *testing.T) {
	clients := NewDrupalClients()

	if clients.Issues() == nil {
		t.Error("no reader")
	}
	if len(clients.Warnings()) != 0 {
		t.Errorf("a fresh client already has warnings: %v", clients.Warnings())
	}
}

// stripEscapes removes ANSI sequences, for asserting that painting changes
// only the colour and never what a cell says.
func stripEscapes(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\x1b' {
			for i < len(text) && text[i] != 'm' {
				i++
			}

			continue
		}
		out.WriteByte(text[i])
	}

	return out.String()
}

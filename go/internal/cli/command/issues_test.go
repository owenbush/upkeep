package command

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/workflow"
)

// drupal.org's own priority taxonomy ids, which is what the API publishes and
// what Issue.Priority holds.
const (
	priorityCritical = 400
	priorityMajor    = 300
	priorityNormal   = 200
)

// aQueue is a module's open issue queue in every shape the table renders: one
// waiting on the maintainer, one carrying a patch, and one nobody has touched.
//
// The RTBC issue is deliberately the *oldest*, so "what awaits you first" and
// "newest first" disagree — sorted by node id alone the order would be the
// same, and the test would pass with the attention rule deleted.
func aQueue() []drupal.Issue {
	return []drupal.Issue{
		{
			Nid: 3100001, Title: "Drupal 12 compatibility",
			Status: drupal.StatusRtbc, Priority: priorityCritical,
			URL: "https://www.drupal.org/i/3100001",
		},
		{
			Nid: 3100002, Title: "Re-roll for 2.0.x",
			Status: drupal.StatusNeedsReview, Priority: priorityNormal,
			URL:   "https://www.drupal.org/i/3100002",
			Files: []drupal.IssueFile{{Name: "fix-2.patch", URL: "https://drupal.org/files/fix-2.patch"}},
		},
		{
			Nid: 3100003, Title: "Alias generation breaks on long titles",
			Status: drupal.StatusActive, Priority: priorityMajor,
			URL: "https://www.drupal.org/i/3100003",
		},
	}
}

// aQuietGitlab answers the live merge-request read with nothing, without
// reaching the network.
func aQuietGitlab(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.EscapedPath(), "/projects/project%2Fpathauto") {
			_, _ = w.Write([]byte(
				`{"id":1,"path":"pathauto","path_with_namespace":"project/pathauto",` +
					`"web_url":"https://git.drupalcode.org/project/pathauto"}`))

			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(server.Close)

	return server
}

// runIssuesCommand invokes the tree against a scripted issue source.
func runIssuesCommand(
	t *testing.T, issues IssueClients, clients cli.GitlabClients, args ...string,
) (int, string, string) {
	t.Helper()

	return runIssueLoop(t, aWorkingEngine(), issues, clients, cli.NoBrowser{}, args...)
}

// Every open issue, whether or not anyone has contributed — which is the
// difference from dashboard and patches, and the reason this exists.
func TestIssuesListsTheWholeOpenQueue(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	code, stdout, stderr := runIssuesCommand(t,
		scriptedIssues{issues: aQueue()}, scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, expected := range []string{"#3100001", "#3100002", "#3100003", "CONTRIBUTION", "NEXT"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the table is missing %q:\n%s", expected, stdout)
		}
	}
	// The word, not a dash, and in the row rather than only in the footer: an
	// issue with nothing on it is what this command exists to surface.
	if !strings.Contains(rowFor(t, stdout, "#3100003"), unclaimedCell) {
		t.Errorf("the unclaimed row is not marked as such:\n%s", stdout)
	}
	// And the footer counts what the table shows: three open, of which the one
	// carrying a patch is claimed.
	if !strings.Contains(stdout, "3 open issues") || !strings.Contains(stdout, "2 unclaimed") {
		t.Errorf("footer:\n%s", stdout)
	}
}

// What awaits a maintainer's verdict sorts first: an RTBC issue is somebody
// waiting on you, while an Active one is waiting on nobody.
func TestIssuesPutsWhatAwaitsYouFirst(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	_, stdout, _ := runIssuesCommand(t,
		scriptedIssues{issues: aQueue()}, scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	// What awaits you first — review and RTBC — then everything else, newest
	// first within each group.
	order := []string{"#3100002", "#3100001", "#3100003"}
	at := -1
	for _, nid := range order {
		found := strings.Index(stdout, nid)
		if found <= at {
			t.Fatalf("%s is out of order (want %v):\n%s", nid, order, stdout)
		}
		at = found
	}
	if !strings.Contains(stdout, "2 awaiting you") {
		t.Errorf("footer does not count what awaits: %s", stdout)
	}
}

// --unclaimed is the whole point: the work nobody has started.
func TestIssuesNarrowsToUnclaimedWork(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	code, stdout, stderr := runIssuesCommand(t,
		scriptedIssues{issues: aQueue()}, scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--unclaimed", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// The one carrying a patch is gone; the two with nothing remain.
	if strings.Contains(stdout, "#3100002") {
		t.Errorf("an issue with a patch survived --unclaimed:\n%s", stdout)
	}
	for _, kept := range []string{"#3100001", "#3100003"} {
		if !strings.Contains(stdout, kept) {
			t.Errorf("%s was filtered out:\n%s", kept, stdout)
		}
	}
	if !strings.Contains(stdout, "2 open issues") {
		t.Errorf("the footer counts the unfiltered list:\n%s", stdout)
	}
}

// NEXT names the command that evaluates whatever the row carries — the same
// commands the dashboard names, so the two views never suggest different
// things about the same issue.
func TestIssuesNamesTheCommandForEachRow(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	_, stdout, _ := runIssuesCommand(t,
		scriptedIssues{issues: aQueue()}, scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	// Nothing on it: start the work.
	if !strings.Contains(stdout, "upkeep start pathauto 3100001") {
		t.Errorf("an unclaimed issue does not say how to start:\n%s", stdout)
	}
	// A patch on it: check the patch.
	if !strings.Contains(stdout, "upkeep patch:check pathauto 3100002") {
		t.Errorf("an issue with a patch does not point at patch:check:\n%s", stdout)
	}
}

// --status narrows the scan itself, so the request to drupal.org is the
// narrow one rather than a wide read filtered afterwards.
func TestIssuesNarrowsTheScanByStatus(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)
	asked := &recordingIssues{scriptedIssues: scriptedIssues{issues: aQueue()}}

	runIssuesCommand(t, asked, scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--status=rtbc", "--cockpit="+root)

	if len(asked.scanned) != 1 || asked.scanned[0] != drupal.StatusRtbc {
		t.Errorf("it scanned %v", asked.scanned)
	}
}

// A status nobody recognises falls back to every open status rather than
// refusing: answering a typo with an empty table would read as "this module
// has no active issues".
func TestIssuesFallsBackToEveryOpenStatus(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)
	asked := &recordingIssues{scriptedIssues: scriptedIssues{issues: aQueue()}}

	code, _, _ := runIssuesCommand(t, asked, scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--status=not-a-status", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if len(asked.scanned) != len(drupal.OpenStatuses()) {
		t.Errorf("it scanned %v", asked.scanned)
	}
}

// Every open status is spelled on the command line exactly as the table
// prints it, so the word somebody reads is the word they can type.
func TestEveryOpenStatusIsSelectableByItsOwnLabel(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	for _, status := range drupal.OpenStatuses() {
		asked := &recordingIssues{scriptedIssues: scriptedIssues{issues: aQueue()}}
		runIssuesCommand(t, asked, scriptedClients{client: gitlabClientFor(server)},
			"issues", "pathauto", "--status="+statusFlagName(status), "--cockpit="+root)

		// Every status wearing that label, and no others: "postponed" is two
		// of drupal.org's statuses, and selecting one would omit half the
		// postponed queue. Never the whole open set, which is the fallback and
		// would mean the flag did nothing.
		if len(asked.scanned) == len(drupal.OpenStatuses()) {
			t.Errorf("%q was ignored", statusFlagName(status))

			continue
		}
		for _, selected := range asked.scanned {
			if statusFlagName(selected) != statusFlagName(status) {
				t.Errorf("%q selected %v", statusFlagName(status), asked.scanned)
			}
		}
		if !contains(asked.scanned, status) {
			t.Errorf("%q did not select itself: %v", statusFlagName(status), asked.scanned)
		}
	}
}

func contains(statuses []drupal.IssueStatus, want drupal.IssueStatus) bool {
	for _, status := range statuses {
		if status == want {
			return true
		}
	}

	return false
}

// An empty queue says so rather than printing headers over nothing.
func TestIssuesSaysWhenThereAreNoMatchingIssues(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	code, stdout, stderr := runIssuesCommand(t, scriptedIssues{},
		scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if strings.Contains(stdout, "CONTRIBUTION") {
		t.Errorf("it printed a table with no rows:\n%s", stdout)
	}
	if !strings.Contains(stderr, "No matching open issues") {
		t.Errorf("stderr: %q", stderr)
	}
}

// A missing snapshot is said out loud, and the suggestion has to be one that
// works for *this* module: the dashboard surveys the watchlist, so pointing an
// unwatched module at --refresh would send somebody to a command that refuses
// them.
func TestIssuesSaysWhyTheContributionColumnIsEmpty(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)
	issues := scriptedIssues{issues: aQueue()}
	clients := scriptedClients{client: gitlabClientFor(server)}

	_, watched, _ := runIssuesCommand(t, issues, clients,
		"issues", "pathauto", "--cockpit="+root)
	if !strings.Contains(watched, "dashboard --refresh=pathauto") {
		t.Errorf("a watched module is not pointed at a refresh:\n%s", watched)
	}

	// An unregistered module gets the other sentence, because a refresh would
	// not cover it. Its cores come from the disk instead of the registry, so
	// there has to be a base artifact set for it to resolve at all.
	where, _ := cockpit.New(root)
	if err := os.WriteFile(where.RegistryPath(), []byte("modules: {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(where.BaseArtifactsPath(), "11"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	code, derived, stderr := runIssuesCommand(t, issues, clients,
		"issues", "pathauto", "--cockpit="+root)
	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if strings.Contains(derived, "dashboard --refresh=pathauto") {
		t.Errorf("an unwatched module was sent to a command that refuses it:\n%s", derived)
	}
	if !strings.Contains(derived, "modules:add") {
		t.Errorf("it does not say how to watch it:\n%s", derived)
	}
}

// GitLab failing costs the contribution column, never the list: the issue
// queue is this command's subject and the column is context.
func TestIssuesStillListsWhenGitlabCannotBeRead(t *testing.T) {
	root := aDashboardCockpit(t)

	code, stdout, stderr := runIssuesCommand(t,
		scriptedIssues{issues: aQueue()}, noClients{},
		"issues", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "#3100001") {
		t.Errorf("the list was lost with the column:\n%s", stdout)
	}
}

// What drupal.org could not read is reported: returning less data is
// indistinguishable at the call site from there being less data.
func TestIssuesReportsWhatTheScanCouldNotRead(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	_, _, stderr := runIssuesCommand(t,
		scriptedIssues{issues: aQueue(), warnings: []string{"an attachment did not answer"}},
		scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	if !strings.Contains(stderr, "an attachment did not answer") {
		t.Errorf("the warning was swallowed: %q", stderr)
	}
}

// rowFor is the table line for one issue, so an assertion about a cell cannot
// be satisfied by the header, the footer, or another issue's row.
func rowFor(t *testing.T, table, nid string) string {
	t.Helper()

	for _, line := range strings.Split(table, "\n") {
		if strings.Contains(line, nid) {
			return line
		}
	}
	t.Fatalf("no row for %s in:\n%s", nid, table)

	return ""
}

// recordingIssues remembers which statuses the scan asked for.
type recordingIssues struct {
	scriptedIssues
	scanned []drupal.IssueStatus
}

func (r *recordingIssues) Issues() IssueSource { return r }

func (r *recordingIssues) ProjectIssues(
	_ string, statuses []drupal.IssueStatus,
) []drupal.Issue {
	r.scanned = statuses

	return r.issues
}

// An issue carrying a merge request shows it, and NEXT points at the command
// that evaluates it rather than at the one that starts work.
func TestIssuesShowsAMergeRequestAndPointsAtCheck(t *testing.T) {
	root := aDashboardCockpit(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.EscapedPath(), "/projects/project%2Fpathauto"):
			_, _ = w.Write([]byte(
				`{"id":1,"path":"pathauto","path_with_namespace":"project/pathauto",` +
					`"web_url":"https://git.drupalcode.org/project/pathauto"}`))
		case strings.Contains(r.URL.String(), "state=opened"):
			_, _ = w.Write([]byte(
				`[{"iid":42,"state":"opened","title":"Issue #3100002: Re-roll for 2.0.x",` +
					`"source_branch":"3100002-re-roll","target_branch":"2.0.x",` +
					`"diff_refs":{"base_sha":"aaa","head_sha":"bbb"},` +
					`"web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/42"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(server.Close)

	code, stdout, stderr := runIssuesCommand(t,
		scriptedIssues{issues: aQueue()}, scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// Both halves in one cell: the merge request and the patch that is also
	// on the issue, which is an ordinary shape in the Drupal community.
	if !strings.Contains(stdout, "!42") || !strings.Contains(stdout, "1 patch") {
		t.Errorf("the contribution cell does not carry both:\n%s", stdout)
	}
	if !strings.Contains(stdout, "upkeep check pathauto 42") {
		t.Errorf("NEXT does not point at the merge request:\n%s", stdout)
	}
	// And the footer no longer says the column is empty.
	if strings.Contains(stdout, "no cached MRs") {
		t.Errorf("a live read was reported as no read:\n%s", stdout)
	}
}

// Every status the table can print gets a cell, including the ones that are
// neither active, review nor RTBC — and an issue drupal.org gave no priority
// prints the placeholder rather than a blank.
func TestIssuesRendersEveryStatusAndAMissingPriority(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	code, stdout, stderr := runIssuesCommand(t, scriptedIssues{issues: []drupal.Issue{
		{Nid: 3100004, Title: "Postponed on core", Status: drupal.StatusPostponed},
		{Nid: 3100005, Title: "Needs work", Status: drupal.StatusNeedsWork},
	}}, scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, expected := range []string{"postponed", "needs work", "–"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the table is missing %q:\n%s", expected, stdout)
		}
	}
}

// One issue is an issue, not "1 issues".
func TestIssuesCountsOneIssueInTheSingular(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	_, stdout, _ := runIssuesCommand(t, scriptedIssues{issues: aQueue()[:1]},
		scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	if !strings.Contains(stdout, "1 open issue ") {
		t.Errorf("footer:\n%s", stdout)
	}
}

// A GitLab that answers the project but not the rest costs the column and not
// the list — the same degradation as not answering at all.
func TestIssuesDegradesWhenOnlyHalfOfGitlabAnswers(t *testing.T) {
	root := aDashboardCockpit(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.EscapedPath(), "/projects/project%2Fpathauto") {
			_, _ = w.Write([]byte(
				`{"id":1,"path":"pathauto","path_with_namespace":"project/pathauto",` +
					`"web_url":"https://git.drupalcode.org/project/pathauto"}`))

			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"500"}`))
	}))
	t.Cleanup(server.Close)

	code, stdout, stderr := runIssuesCommand(t,
		scriptedIssues{issues: aQueue()}, scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "#3100001") {
		t.Errorf("the list was lost:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no cached MRs") {
		t.Errorf("it did not say the column is empty:\n%s", stdout)
	}
}

// A fork map that cannot be read costs only the bot pairing, not the merge
// requests that were read successfully.
func TestIssuesKeepsTheMergeRequestsWhenTheForkMapFails(t *testing.T) {
	root := aDashboardCockpit(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.EscapedPath(), "/projects/project%2Fpathauto"):
			_, _ = w.Write([]byte(
				`{"id":1,"path":"pathauto","path_with_namespace":"project/pathauto",` +
					`"web_url":"https://git.drupalcode.org/project/pathauto"}`))
		case strings.Contains(r.URL.String(), "state=opened"):
			_, _ = w.Write([]byte(
				`[{"iid":42,"state":"opened","title":"Issue #3100002: Re-roll for 2.0.x",` +
					`"source_branch":"3100002-re-roll","target_branch":"2.0.x",` +
					`"diff_refs":{"base_sha":"aaa","head_sha":"bbb"},` +
					`"web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/42"}]`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"500"}`))
		}
	}))
	t.Cleanup(server.Close)

	code, stdout, stderr := runIssuesCommand(t,
		scriptedIssues{issues: aQueue()}, scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "!42") {
		t.Errorf("the merge requests went with the fork map:\n%s", stdout)
	}
}

// --status completes, because the statuses are the one set of values upkeep
// can enumerate without a network.
func TestTheStatusFlagCompletes(t *testing.T) {
	root := aDashboardCockpit(t)

	code, stdout, stderr := invokeWith(t, NewRootFor(noVolumes{}, noSizer),
		cobra.ShellCompRequestCmd, "issues", "pathauto", "--cockpit="+root, "--status=")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, status := range drupal.OpenStatuses() {
		if !strings.Contains(stdout, statusFlagName(status)) {
			t.Errorf("%q is not suggested:\n%s", statusFlagName(status), stdout)
		}
	}
}

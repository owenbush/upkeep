package command

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// workingEngine records the two operations the issue loop drives: cutting a
// work branch, and pushing one.
type workingEngine struct {
	fakeEngine

	started   []adapter.IssueBranch
	bases     []string
	refreshes []adapter.BaseRefresh
	resumed   bool
	startErr  error

	pushed   []string
	remotes  []adapter.GitRemote
	pushSHA  string
	pushErr  error
	recorded string
}

func (e *workingEngine) StartWork(
	_ adapter.Environment, branch adapter.IssueBranch, base string, refresh adapter.BaseRefresh,
) (bool, error) {
	e.started = append(e.started, branch)
	e.bases = append(e.bases, base)
	e.refreshes = append(e.refreshes, refresh)

	return e.resumed, e.startErr
}

func (e *workingEngine) PushWork(
	_ adapter.Environment, branch adapter.IssueBranch, remote adapter.GitRemote,
) (string, error) {
	e.pushed = append(e.pushed, branch.Name)
	e.remotes = append(e.remotes, remote)

	return e.pushSHA, e.pushErr
}

func (e *workingEngine) RecordedBaseBranch(adapter.Environment) string { return e.recorded }

// aWorkingEngine is one wired for a healthy run.
func aWorkingEngine() *workingEngine {
	return &workingEngine{
		fakeEngine: fakeEngine{environment: adapter.Environment{
			ModuleName: "pathauto", CoreMajor: "11",
			ProjectName: "upkeep-pathauto-d11", ProjectPath: "/projects/upkeep-pathauto-d11",
		}},
		pushSHA: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	}
}

// anIssue is the issue the loop acts on.
func anIssue() drupal.Issue {
	return drupal.Issue{
		Nid: 3223746, Title: "Fix the alias cache", Status: drupal.StatusNeedsWork,
		Version: "2.0.x", Category: "Bug report", Component: "Code",
		URL: "https://www.drupal.org/i/3223746",
	}
}

// recordingBrowser answers whether it opened, and remembers what.
type recordingBrowser struct {
	opened []string
	refuse bool
}

func (b *recordingBrowser) Open(url string) bool {
	b.opened = append(b.opened, url)

	return !b.refuse
}

// forkScene is a GitLab that answers the publish flow: a project, its issue
// fork, the branch's merge requests, and the creation.
type forkScene struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []string

	// forkStatus is what a request for the issue fork answers. 404 is an issue
	// nobody has started.
	forkStatus    int
	projectStatus int
	mrStatus      int
	forkAccess    *int
	forkSSHURL    string
	defaultBr     string
	openMR        string
	createdCode   int
	createdBody   string
	createdSeen   map[string]any
}

func aForkScene(t *testing.T) *forkScene {
	t.Helper()

	developer := 30
	scene := &forkScene{
		forkStatus:    http.StatusOK,
		projectStatus: http.StatusOK,
		mrStatus:      http.StatusOK,
		forkAccess:    &developer,
		forkSSHURL:    "git@git.drupal.org:issue/pathauto-3223746.git",
		defaultBr:     "2.0.x",
		openMR:        "[]",
		createdCode:   http.StatusCreated,
		createdBody: `{"iid":77,"state":"opened","title":"Issue #3223746: Fix the alias cache",` +
			`"web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/77"}`,
	}

	scene.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scene.mu.Lock()
		scene.requests = append(scene.requests, r.Method+" "+r.URL.String())
		scene.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		path := r.URL.EscapedPath()

		switch {
		case strings.HasSuffix(path, "/projects/issue%2Fpathauto-3223746"):
			if scene.forkStatus != http.StatusOK {
				w.WriteHeader(scene.forkStatus)
				_, _ = w.Write([]byte(`{"message":"404 Project Not Found"}`))

				return
			}
			_, _ = w.Write([]byte(scene.forkJSON()))

		case strings.HasSuffix(path, "/projects/project%2Fpathauto"):
			if scene.projectStatus != http.StatusOK {
				w.WriteHeader(scene.projectStatus)
				_, _ = w.Write([]byte(`{"message":"500 Internal Server Error"}`))

				return
			}
			_, _ = fmt.Fprintf(w,
				`{"id":1,"path":"pathauto","path_with_namespace":"project/pathauto",`+
					`"web_url":"https://git.drupalcode.org/project/pathauto","default_branch":%q}`,
				scene.defaultBr)

		case strings.HasSuffix(path, "/projects/1/merge_requests") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(scene.openMR))

		case strings.HasSuffix(path, "/projects/1/merge_requests/9"):
			if scene.mrStatus != http.StatusOK {
				w.WriteHeader(scene.mrStatus)
				_, _ = w.Write([]byte(`{"message":"500 Internal Server Error"}`))

				return
			}
			_, _ = w.Write([]byte(
				`{"iid":9,"state":"opened","title":"Issue #3223746: Fix the alias cache",` +
					`"source_branch":"3223746-fix-the-alias-cache","description":"",` +
					`"web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/9"}`))

		case strings.HasSuffix(path, "/projects/2/merge_requests") && r.Method == http.MethodPost:
			body, _ := decodeBody(r)
			scene.mu.Lock()
			scene.createdSeen = body
			scene.mu.Unlock()
			w.WriteHeader(scene.createdCode)
			_, _ = w.Write([]byte(scene.createdBody))

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))
		}
	}))
	t.Cleanup(scene.server.Close)

	return scene
}

// decodeBody decodes a JSON request body.
func decodeBody(r *http.Request) (map[string]any, error) {
	var body map[string]any
	err := json.NewDecoder(r.Body).Decode(&body)

	return body, err
}

func (s *forkScene) forkJSON() string {
	permissions := "null"
	if s.forkAccess != nil {
		permissions = fmt.Sprintf(`{"project_access":{"access_level":%d}}`, *s.forkAccess)
	}

	return fmt.Sprintf(
		`{"id":2,"path":"pathauto-3223746","path_with_namespace":"issue/pathauto-3223746",`+
			`"web_url":"https://git.drupalcode.org/issue/pathauto-3223746",`+
			`"ssh_url_to_repo":%q,"permissions":%s}`,
		s.forkSSHURL, permissions)
}

func (s *forkScene) client() *gitlab.Client {
	return gitlab.NewClient(nil, "a-token-long-enough", s.server.URL+"/api/v4", s.server.URL)
}

// asked reports whether a request matching a fragment was made.
func (s *forkScene) asked(fragment string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, seen := range s.requests {
		if strings.Contains(seen, fragment) {
			return true
		}
	}

	return false
}

// runIssueLoop invokes the tree wired for the issue loop.
func runIssueLoop(
	t *testing.T, engine adapter.Engine, issues IssueClients,
	clients cli.GitlabClients, browser cli.Browser, args ...string,
) (int, string, string) {
	t.Helper()

	root := NewRoot(Surface{
		Engines: &fakeFactory{engine: &fakeEngine{}, replace: engine},
		Clients: clients, Issues: issues, Prompts: noPrompts,
		Volumes: someVolumes(nil), Sizer: noSizer, Browser: browser,
	})

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)

	return cli.Execute(root, errOut), out.String(), errOut.String()
}

// someIssues is an issue source carrying the one issue the loop acts on.
func someIssues() scriptedIssues {
	return scriptedIssues{issues: []drupal.Issue{anIssue()}}
}

// start prints the project path on stdout and nothing else, so
// `cd $(upkeep start …)` works.
func TestStartPrintsThePathAlone(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aWorkingEngine()

	code, stdout, stderr := runIssueLoop(t, engine, someIssues(), noClients{}, cli.NoBrowser{},
		"start", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if stdout != "/projects/upkeep-pathauto-d11\n" {
		t.Errorf("stdout carried more than the path: %q", stdout)
	}
	if len(engine.started) != 1 {
		t.Fatalf("started %+v", engine.started)
	}
	// drupal.org's own convention, so the merge request that follows is linked
	// to the issue with nothing further to configure.
	if engine.started[0].Name != "3223746-fix-the-alias-cache" {
		t.Errorf("branch %q", engine.started[0].Name)
	}
	// The diagnostics are still said, on the other stream.
	for _, expected := range []string{"3223746", "Fix the alias cache", "upkeep publish"} {
		if !strings.Contains(stderr, expected) {
			t.Errorf("stderr is missing %q:\n%s", expected, stderr)
		}
	}
}

// Resuming and starting are different words, because the second would tell
// somebody their branch is new when it holds yesterday's work.
func TestStartSaysWhetherItResumed(t *testing.T) {
	root := anEnvironmentCockpit(t)

	fresh := aWorkingEngine()
	_, _, stderr := runIssueLoop(t, fresh, someIssues(), noClients{}, cli.NoBrowser{},
		"start", "pathauto", "3223746", "--cockpit="+root)
	if !strings.Contains(stderr, "Started") || strings.Contains(stderr, "Resumed") {
		t.Errorf("a fresh branch: %q", stderr)
	}

	existing := aWorkingEngine()
	existing.resumed = true
	_, _, stderr = runIssueLoop(t, existing, someIssues(), noClients{}, cli.NoBrowser{},
		"start", "pathauto", "3223746", "--cockpit="+root)
	if !strings.Contains(stderr, "Resumed") {
		t.Errorf("an existing branch: %q", stderr)
	}
}

// --branch names the branch; --base names what it is cut from; --no-update
// skips the fetch. All three reach the engine, because none of them is
// observable anywhere else.
func TestStartCarriesItsFlagsToTheEngine(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aWorkingEngine()

	code, _, stderr := runIssueLoop(t, engine, someIssues(), noClients{}, cli.NoBrowser{},
		"start", "pathauto", "3223746", "--cockpit="+root,
		"--branch=rework-the-cache", "--base=2.0.x", "--no-update")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if engine.started[0].Name != "rework-the-cache" {
		t.Errorf("branch %q", engine.started[0].Name)
	}
	// Still the issue's branch, whatever it is called: the nid is what pairs
	// the work with the issue.
	if engine.started[0].IssueNid != 3223746 {
		t.Errorf("branch %+v", engine.started[0])
	}
	if engine.bases[0] != "2.0.x" {
		t.Errorf("base %q", engine.bases[0])
	}
	if engine.refreshes[0] != adapter.RefreshSkip {
		t.Errorf("refresh %v", engine.refreshes[0])
	}
}

// The base is empty by default, which the adapter reads as "resolve it from
// the working copy" — a base upkeep guessed would cut the branch from
// somewhere the operator never chose.
func TestStartLeavesTheBaseToTheAdapterByDefault(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aWorkingEngine()

	runIssueLoop(t, engine, someIssues(), noClients{}, cli.NoBrowser{},
		"start", "pathauto", "3223746", "--cockpit="+root)

	if engine.bases[0] != "" {
		t.Errorf("upkeep chose a base of its own: %q", engine.bases[0])
	}
	if engine.refreshes[0] != adapter.RefreshUpdate {
		t.Errorf("the base was not refreshed by default: %v", engine.refreshes[0])
	}
}

// An issue that cannot be read is a refusal, and nothing is provisioned: an
// environment built for an issue that does not exist is minutes spent on a
// typo.
func TestStartRefusesAnUnreadableIssueBeforeProvisioning(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aWorkingEngine()

	code, _, stderr := runIssueLoop(t, engine, noIssues{}, noClients{}, cli.NoBrowser{},
		"start", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "number in the issue URL") {
		t.Errorf("the refusal does not say where the node id comes from: %q", stderr)
	}
	if len(engine.ensured) != 0 {
		t.Errorf("it provisioned %v for an issue it could not read", engine.ensured)
	}
}

// The node id rule, on both commands that take one.
func TestTheIssueLoopRefusesANonNodeId(t *testing.T) {
	root := anEnvironmentCockpit(t)

	for _, command := range []string{"start", "publish"} {
		for _, raw := range []string{"0", "abc", "32.7"} {
			code, _, stderr := runIssueLoop(t, aWorkingEngine(), someIssues(),
				noClients{}, cli.NoBrowser{}, command, "pathauto", raw, "--cockpit="+root)

			if code != workflow.Infrastructure {
				t.Errorf("%s #%s gave exit %d", command, raw, code)
			}
			if !strings.Contains(stderr, "positive integer") {
				t.Errorf("%s #%s: %q", command, raw, stderr)
			}
		}
	}
}

// publish pushes the branch to the issue fork and opens the merge request
// across projects into the canonical one.
func TestPublishPushesToTheForkAndOpensAcrossProjects(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	engine := aWorkingEngine()

	code, stdout, stderr := runIssueLoop(t, engine, someIssues(),
		scriptedClients{client: scene.client()}, cli.NoBrowser{},
		"publish", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(engine.pushed) != 1 || engine.pushed[0] != "3223746-fix-the-alias-cache" {
		t.Fatalf("pushed %v", engine.pushed)
	}
	// Never origin: the branch goes to the issue fork, at the URL GitLab
	// itself advertises.
	if engine.remotes[0].URL != "git@git.drupal.org:issue/pathauto-3223746.git" {
		t.Errorf("pushed to %q", engine.remotes[0].URL)
	}
	if engine.remotes[0].Name != "issue-3223746" {
		t.Errorf("remote named %q", engine.remotes[0].Name)
	}

	// Posted to the fork, which holds the branch, naming the canonical
	// project as the destination.
	if !scene.asked("POST /api/v4/projects/2/merge_requests") {
		t.Errorf("the merge request was not posted to the fork: %v", scene.requests)
	}
	if target, _ := scene.createdSeen["target_project_id"].(float64); target != 1 {
		t.Errorf("it does not target the canonical project: %+v", scene.createdSeen)
	}
	if got := scene.createdSeen["target_branch"]; got != "2.0.x" {
		t.Errorf("target branch %v", got)
	}
	// The drupal.org title convention, so the merge request pairs with its
	// issue by the ordinary rule.
	if got := scene.createdSeen["title"]; got != "Issue #3223746: Fix the alias cache" {
		t.Errorf("title %v", got)
	}
	if !strings.Contains(stdout, "merge_requests/77") {
		t.Errorf("stdout does not link the merge request: %q", stdout)
	}
}

// An issue with no fork is a browser handoff, not an error upkeep fixes by
// making one — and nothing is pushed.
func TestPublishRefusesBeforeProvisioningWhenThereIsNoFork(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	scene.forkStatus = http.StatusNotFound
	engine := aWorkingEngine()

	code, _, stderr := runIssueLoop(t, engine, someIssues(),
		scriptedClients{client: scene.client()}, cli.NoBrowser{},
		"publish", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if len(engine.pushed) != 0 {
		t.Errorf("it pushed %v with nowhere to push to", engine.pushed)
	}
	// And nothing was provisioned either: the fork is a browser click away,
	// and spending minutes building an environment first makes the handoff
	// cost more than the work.
	if len(engine.ensured) != 0 {
		t.Errorf("it provisioned %v before checking there was anywhere to push", engine.ensured)
	}
	for _, expected := range []string{"Create issue fork", "drupal.org/node/3223746", "upkeep publish"} {
		if !strings.Contains(stderr, expected) {
			t.Errorf("the refusal is missing %q:\n%s", expected, stderr)
		}
	}
}

// Push access is asked before the push, not diagnosed after it.
func TestPublishRefusesBeforeProvisioningWithoutPushAccess(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	guest := 10
	scene.forkAccess = &guest
	engine := aWorkingEngine()

	code, _, stderr := runIssueLoop(t, engine, someIssues(),
		scriptedClients{client: scene.client()}, cli.NoBrowser{},
		"publish", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if len(engine.pushed) != 0 {
		t.Errorf("it pushed %v to a fork it cannot write to", engine.pushed)
	}
	if len(engine.ensured) != 0 {
		t.Errorf("it provisioned %v before checking it could push", engine.ensured)
	}
	if !strings.Contains(stderr, "push access") {
		t.Errorf("stderr: %q", stderr)
	}
}

// Unknown access is not "no": an anonymous read omits permissions entirely,
// and refusing on that would block pushes that work.
func TestPublishProceedsWhenPushAccessIsUnknown(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	scene.forkAccess = nil
	engine := aWorkingEngine()

	code, _, stderr := runIssueLoop(t, engine, someIssues(),
		scriptedClients{client: scene.client()}, cli.NoBrowser{},
		"publish", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(engine.pushed) != 1 {
		t.Errorf("it refused on an absent permissions key: %v", engine.pushed)
	}
}

// Re-running publish after more commits updates the open merge request rather
// than opening a second one.
func TestPublishReportsTheOpenMergeRequestRatherThanDuplicatingIt(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	scene.openMR = `[{"iid":9,"state":"opened","source_project_id":2,` +
		`"web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/9"}]`
	engine := aWorkingEngine()

	code, stdout, stderr := runIssueLoop(t, engine, someIssues(),
		scriptedClients{client: scene.client()}, cli.NoBrowser{},
		"publish", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// The push still happened — that is what updates it.
	if len(engine.pushed) != 1 {
		t.Errorf("pushed %v", engine.pushed)
	}
	if scene.asked("POST /api/v4/projects/2/merge_requests") {
		t.Error("it opened a second merge request for a branch that already had one")
	}
	if !strings.Contains(stdout, "merge_requests/9") {
		t.Errorf("stdout does not link the existing merge request: %q", stdout)
	}
}

// The target is the base the work was cut from, else the project default —
// never a tracked core major, because no contrib project has a branch called
// "11".
func TestPublishTargetsTheRecordedBaseThenTheProjectDefault(t *testing.T) {
	root := anEnvironmentCockpit(t)

	recorded := aWorkingEngine()
	recorded.recorded = "3.0.x"
	scene := aForkScene(t)
	runIssueLoop(t, recorded, someIssues(), scriptedClients{client: scene.client()},
		cli.NoBrowser{}, "publish", "pathauto", "3223746", "--cockpit="+root)
	if got := scene.createdSeen["target_branch"]; got != "3.0.x" {
		t.Errorf("the recorded base did not win: %v", got)
	}

	// And --target outranks both.
	named := aForkScene(t)
	runIssueLoop(t, recorded, someIssues(), scriptedClients{client: named.client()},
		cli.NoBrowser{}, "publish", "pathauto", "3223746", "--cockpit="+root, "--target=1.0.x")
	if got := named.createdSeen["target_branch"]; got != "1.0.x" {
		t.Errorf("--target did not win: %v", got)
	}
}

// Nothing to target is a refusal naming the flag — and it says the push
// already happened, because the operator's branch is on the remote either way.
func TestPublishSaysTheBranchWasPushedWhenItCannotTarget(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	scene.defaultBr = ""
	engine := aWorkingEngine()

	code, _, stderr := runIssueLoop(t, engine, someIssues(),
		scriptedClients{client: scene.client()}, cli.NoBrowser{},
		"publish", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if len(engine.pushed) != 1 {
		t.Errorf("pushed %v", engine.pushed)
	}
	for _, expected := range []string{"was pushed", "--target=", "not a core version"} {
		if !strings.Contains(stderr, expected) {
			t.Errorf("the refusal is missing %q:\n%s", expected, stderr)
		}
	}
}

// A merge request that cannot be opened still tells the operator where their
// pushed branch is, and where to open it by hand.
func TestPublishOffersTheBrowserWhenTheApiRefusesTheMergeRequest(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	scene.createdCode = http.StatusForbidden
	scene.createdBody = `{"message":"403 Forbidden"}`
	engine := aWorkingEngine()

	code, _, stderr := runIssueLoop(t, engine, someIssues(),
		scriptedClients{client: scene.client()}, cli.NoBrowser{},
		"publish", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "was pushed") {
		t.Errorf("it does not say the branch is on the remote: %q", stderr)
	}
	if !strings.Contains(stderr, "merge_requests/new") {
		t.Errorf("it does not offer the browser: %q", stderr)
	}
}

// --draft marks it, once: GitLab reads the prefix, and a title already
// carrying it does not need a second.
func TestPublishMarksADraftOnce(t *testing.T) {
	root := anEnvironmentCockpit(t)

	scene := aForkScene(t)
	runIssueLoop(t, aWorkingEngine(), someIssues(), scriptedClients{client: scene.client()},
		cli.NoBrowser{}, "publish", "pathauto", "3223746", "--cockpit="+root, "--draft")
	if got, _ := scene.createdSeen["title"].(string); !strings.HasPrefix(got, "Draft: ") {
		t.Errorf("title %q", got)
	}

	already := aForkScene(t)
	runIssueLoop(t, aWorkingEngine(), someIssues(), scriptedClients{client: already.client()},
		cli.NoBrowser{}, "publish", "pathauto", "3223746", "--cockpit="+root,
		"--draft", "--title=Draft: mine")
	if got := already.createdSeen["title"]; got != "Draft: mine" {
		t.Errorf("the prefix was doubled: %v", got)
	}
}

// publish writes, so it takes the authenticated path: finding out there is no
// credential *after* the push would leave a branch on a remote with nothing
// pointing at it.
func TestPublishRefusesWithoutACredentialBeforePushing(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aWorkingEngine()

	code, _, stderr := runIssueLoop(t, engine, someIssues(),
		scriptedClients{}, cli.NoBrowser{},
		"publish", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if len(engine.pushed) != 0 {
		t.Errorf("it pushed %v with no way to open a merge request", engine.pushed)
	}
	if !strings.Contains(stderr, "token") {
		t.Errorf("stderr: %q", stderr)
	}
}

// issue shows what drupal.org knows and opens the page, which is where a
// status is actually changed.
func TestIssueShowsTheIssueAndOpensIt(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	browser := &recordingBrowser{}

	code, stdout, stderr := runIssueLoop(t, aWorkingEngine(), someIssues(),
		scriptedClients{client: scene.client()}, browser,
		"issue", "pathauto", "9", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, expected := range []string{
		"3223746", "Fix the alias cache", "Needs work", "Bug report", "!9",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the report is missing %q:\n%s", expected, stdout)
		}
	}
	if len(browser.opened) != 1 || browser.opened[0] != "https://www.drupal.org/i/3223746" {
		t.Errorf("opened %v", browser.opened)
	}
}

// --no-open is for a terminal with no browser behind it, and for a script.
func TestIssueOpensNothingWhenToldNotTo(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	browser := &recordingBrowser{}

	code, _, stderr := runIssueLoop(t, aWorkingEngine(), someIssues(),
		scriptedClients{client: scene.client()}, browser,
		"issue", "pathauto", "9", "--cockpit="+root, "--no-open")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(browser.opened) != 0 {
		t.Errorf("it opened %v", browser.opened)
	}
}

// A browser that did not open says so and prints the URL, because otherwise
// the command looks like it did something and the operator has nothing.
func TestIssuePrintsTheUrlWhenTheBrowserWillNotOpen(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	browser := &recordingBrowser{refuse: true}

	code, stdout, stderr := runIssueLoop(t, aWorkingEngine(), someIssues(),
		scriptedClients{client: scene.client()}, browser,
		"issue", "pathauto", "9", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "Could not open browser") {
		t.Errorf("it did not say the browser failed: %q", stdout)
	}
	if strings.Count(stdout, "https://www.drupal.org/i/3223746") < 1 {
		t.Errorf("it did not print the URL: %q", stdout)
	}
}

// drupal.org being unreadable costs the details, never the URL: the page is
// where the operator is going, and its address is derivable from the nid.
func TestIssueStillOffersTheUrlWhenDrupalOrgCannotBeRead(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	browser := &recordingBrowser{}

	code, stdout, stderr := runIssueLoop(t, aWorkingEngine(), noIssues{},
		scriptedClients{client: scene.client()}, browser,
		"issue", "pathauto", "9", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "3223746") {
		t.Errorf("it did not say which issue: %q", stdout)
	}
	if len(browser.opened) != 1 || !strings.Contains(browser.opened[0], "3223746") {
		t.Errorf("opened %v", browser.opened)
	}
}

// A merge request naming no issue is a refusal that says what was searched:
// the alternative is a lookup for issue 0.
func TestIssueRefusesAMergeRequestThatNamesNoIssue(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	scene.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.EscapedPath(), "/projects/project%2Fpathauto") {
			_, _ = w.Write([]byte(
				`{"id":1,"path":"pathauto","path_with_namespace":"project/pathauto",` +
					`"web_url":"https://git.drupalcode.org/project/pathauto"}`))

			return
		}
		_, _ = w.Write([]byte(
			`{"iid":9,"state":"opened","title":"Tidy up","source_branch":"cleanup",` +
				`"description":"","web_url":"https://example.invalid/9"}`))
	})
	browser := &recordingBrowser{}

	code, _, stderr := runIssueLoop(t, aWorkingEngine(), someIssues(),
		scriptedClients{client: scene.client()}, browser,
		"issue", "pathauto", "9", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "Tidy up") || !strings.Contains(stderr, "cleanup") {
		t.Errorf("the refusal does not say what was searched: %q", stderr)
	}
	if len(browser.opened) != 0 {
		t.Errorf("it opened %v for an issue it could not name", browser.opened)
	}
}

// The MR-IID rule applies here too.
func TestIssueRefusesANonMergeRequestIid(t *testing.T) {
	root := anEnvironmentCockpit(t)

	code, _, stderr := runIssueLoop(t, aWorkingEngine(), someIssues(),
		noClients{}, cli.NoBrowser{}, "issue", "pathauto", "0", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "positive integer") {
		t.Errorf("stderr: %q", stderr)
	}
}

// A field drupal.org did not answer is omitted, not printed blank: an empty
// "Priority:" reads as a field somebody forgot to set.
func TestIssueOmitsWhatDrupalOrgDidNotSay(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	bare := anIssue()
	bare.Category, bare.Component, bare.Version = "", "", ""

	_, stdout, _ := runIssueLoop(t, aWorkingEngine(),
		scriptedIssues{issues: []drupal.Issue{bare}},
		scriptedClients{client: scene.client()}, &recordingBrowser{},
		"issue", "pathauto", "9", "--cockpit="+root)

	for _, absent := range []string{"Category:", "Component:", "Version:"} {
		if strings.Contains(stdout, absent) {
			t.Errorf("%q was printed with nothing after it:\n%s", absent, stdout)
		}
	}
	// What it does know is still there.
	if !strings.Contains(stdout, "Title:") {
		t.Errorf("stdout: %q", stdout)
	}
}

// The three commands complete module names, like every other subject command:
// the whole point of the completion work is that these are the arguments
// nobody remembers.
func TestTheIssueLoopCompletesModuleNames(t *testing.T) {
	root := anEnvironmentCockpit(t)

	for _, name := range []string{"issue", "start", "publish"} {
		code, stdout, stderr := invokeWith(t,
			NewRoot(Surface{
				Engines: &fakeFactory{engine: &fakeEngine{}}, Clients: noClients{},
				Issues: noIssues{}, Prompts: noPrompts, Volumes: noVolumes{}, Sizer: noSizer,
				Browser: cli.NoBrowser{},
			}),
			cobra.ShellCompRequestCmd, name, "--cockpit="+root, "")

		if code != workflow.OK {
			t.Errorf("%s: exit %d (%s)", name, code, stderr)
		}
		if !strings.Contains(stdout, "pathauto") {
			t.Errorf("%s suggests no module: %q", name, stdout)
		}
	}
}

// Every GitLab request the issue loop makes says what it was doing when it
// failed, rather than reporting the API's own words alone.
//
// One property over the step list rather than a test each: the assertions
// would say the same thing five times, and a command that grows a request
// would silently not get one.
func TestTheIssueLoopNamesTheStepThatFailed(t *testing.T) {
	root := anEnvironmentCockpit(t)

	for name, run := range map[string]struct {
		args    []string
		breaks  func(*forkScene)
		expects string
	}{
		"issue cannot resolve the project": {
			args:    []string{"issue", "pathauto", "9"},
			breaks:  func(s *forkScene) { s.projectStatus = http.StatusInternalServerError },
			expects: "could not resolve project",
		},
		"issue cannot fetch the merge request": {
			args:    []string{"issue", "pathauto", "9"},
			breaks:  func(s *forkScene) { s.mrStatus = http.StatusInternalServerError },
			expects: "could not fetch MR !9",
		},
		"publish cannot resolve the project": {
			args:    []string{"publish", "pathauto", "3223746"},
			breaks:  func(s *forkScene) { s.projectStatus = http.StatusInternalServerError },
			expects: "pathauto",
		},
		"publish cannot read the issue fork": {
			args:    []string{"publish", "pathauto", "3223746"},
			breaks:  func(s *forkScene) { s.forkStatus = http.StatusInternalServerError },
			expects: "issue fork for #3223746 could not be read",
		},
		"publish cannot list the branch's merge requests": {
			args:    []string{"publish", "pathauto", "3223746"},
			breaks:  func(s *forkScene) { s.openMR = "not json" },
			expects: "could not be listed",
		},
		"the issue fork advertises no SSH URL": {
			args:    []string{"publish", "pathauto", "3223746"},
			breaks:  func(s *forkScene) { s.forkSSHURL = "" },
			expects: "no SSH URL",
		},
	} {
		scene := aForkScene(t)
		run.breaks(scene)

		code, _, stderr := runIssueLoop(t, aWorkingEngine(), someIssues(),
			scriptedClients{client: scene.client()}, cli.NoBrowser{},
			append(run.args, "--cockpit="+root)...)

		if code != workflow.Infrastructure {
			t.Errorf("%s: exit %d", name, code)
		}
		if !strings.Contains(stderr, run.expects) {
			t.Errorf("%s: the refusal does not say %q:\n%s", name, run.expects, stderr)
		}
	}
}

// A module nobody has heard of is refused before any request is made.
func TestTheIssueLoopRefusesAnImpossibleModuleName(t *testing.T) {
	root := anEnvironmentCockpit(t)

	for _, command := range []string{"issue", "start", "publish"} {
		args := []string{command, "Not A Module", "9", "--cockpit=" + root}
		code, _, stderr := runIssueLoop(t, aWorkingEngine(), someIssues(),
			noClients{}, cli.NoBrowser{}, args...)

		if code != workflow.Infrastructure {
			t.Errorf("%s: exit %d", command, code)
		}
		if stderr == "" {
			t.Errorf("%s failed silently", command)
		}
	}
}

// A cockpit that is not one is reported by every command in the loop.
func TestTheIssueLoopReportsAnUnusableCockpit(t *testing.T) {
	notADirectory := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADirectory, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, command := range []string{"issue", "start", "publish"} {
		code, _, stderr := runIssueLoop(t, aWorkingEngine(), someIssues(),
			noClients{}, cli.NoBrowser{},
			command, "pathauto", "9", "--cockpit="+notADirectory)

		if code != workflow.Infrastructure {
			t.Errorf("%s: exit %d", command, code)
		}
		if stderr == "" {
			t.Errorf("%s failed silently", command)
		}
	}
}

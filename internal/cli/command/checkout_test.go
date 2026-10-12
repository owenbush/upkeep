package command

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// aForkedMr serves the three reads a merge-request checkout makes: the
// project, the merge request, and the forks that say which issue its source
// branch belongs to.
type aForkedMr struct {
	server *httptest.Server
	// state and sourceProject are what the scenarios vary.
	state         string
	sourceProject int
	forks         string
	breakMr       bool
	breakForks    bool
	breakFork     bool
	forkMissing   bool
	breakProject  bool
	noSSH         bool
}

func mrOnAFork(t *testing.T) *aForkedMr {
	t.Helper()

	scene := &aForkedMr{
		state: "opened", sourceProject: 2,
		forks: `[{"id":2,"path_with_namespace":"issue/jumplinks-3628056"}]`,
	}
	scene.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.EscapedPath()

		switch {
		case strings.HasSuffix(path, "/projects/project%2Fjumplinks"):
			if scene.breakProject {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"500"}`))

				return
			}
			_, _ = w.Write([]byte(
				`{"id":1,"path":"jumplinks","path_with_namespace":"project/jumplinks",` +
					sshOrNot(scene.noSSH) +
					`"web_url":"https://git.drupalcode.org/project/jumplinks"}`))

		case strings.HasSuffix(path, "/merge_requests/1"):
			if scene.breakMr {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"500"}`))

				return
			}
			_, _ = w.Write([]byte(
				`{"iid":1,"title":"Automated Project Update Bot fixes","state":"` + scene.state + `",` +
					`"source_branch":"project-update-bot-only","target_branch":"1.0.x",` +
					`"source_project_id":` + strconv.Itoa(scene.sourceProject) + `,` +
					`"web_url":"https://git.drupalcode.org/project/jumplinks/-/merge_requests/1"}`))

		case strings.HasSuffix(path, "/forks"):
			if scene.breakForks {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"500"}`))

				return
			}
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`[]`))

				return
			}
			_, _ = w.Write([]byte(scene.forks))

		case strings.HasSuffix(path, "/projects/issue%2Fjumplinks-3628056"):
			if scene.breakFork {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"500"}`))

				return
			}
			if scene.forkMissing {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"404"}`))

				return
			}
			_, _ = w.Write([]byte(
				`{"id":2,"path":"jumplinks-3628056",` +
					`"path_with_namespace":"issue/jumplinks-3628056",` +
					`"ssh_url_to_repo":"git@git.drupal.org:issue/jumplinks-3628056.git",` +
					`"web_url":"https://git.drupalcode.org/issue/jumplinks-3628056"}`))

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))
		}
	}))
	t.Cleanup(scene.server.Close)

	return scene
}

func (s *aForkedMr) client() *gitlab.Client {
	return gitlab.NewClient(nil, "", s.server.URL+"/api/v4", s.server.URL)
}

// runCheckoutCommand invokes the tree with a scripted GitLab and a fake
// engine, and hands back the engine so the checkout can be asserted.
func runCheckoutCommand(
	t *testing.T, scene *aForkedMr, args ...string,
) (int, string, string, *fakeEngine) {
	t.Helper()

	engine := &fakeEngine{}
	root := NewRoot(Surface{
		Engines: &fakeFactory{engine: engine},
		Clients: scriptedClients{client: scene.client()},
		Issues:  noIssues{}, Prompts: noPrompts,
		Volumes: noVolumes{}, Sizer: noSizer, Browser: cli.NoBrowser{},
	})

	code, stdout, stderr := invokeWith(t, root, args...)

	return code, stdout, stderr, engine
}

// aJumplinksCockpit is a cockpit watching jumplinks on cores 11 and 12, which
// is the shape this command exists for.
func aJumplinksCockpit(t *testing.T) string {
	t.Helper()

	root := aDashboardCockpit(t)
	where, err := cockpit.New(root)
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}
	if err := os.WriteFile(where.RegistryPath(), []byte(
		"modules:\n  jumplinks:\n    project: project/jumplinks\n    core_versions: [\"11\", \"12\"]\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return root
}

// The branch is checked out from the fork it lives on, with the SSH remote
// publish will push to — and the fetch URL is anonymous HTTPS, so taking over
// somebody's merge request needs no key.
func TestAMergeRequestsOwnBranchIsCheckedOutFromItsFork(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)

	code, stdout, stderr, engine := runCheckoutCommand(t, scene,
		"checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(engine.mrCheckouts) != 1 {
		t.Fatalf("checkouts: %v", engine.mrCheckouts)
	}
	did := engine.mrCheckouts[0]
	for _, want := range []string{
		"project-update-bot-only",                                      // the MR's own branch
		"https://git.drupalcode.org/issue/jumplinks-3628056.git",       // fetched anonymously
		"issue-3628056=git@git.drupal.org:issue/jumplinks-3628056.git", // pushed over SSH
	} {
		if !strings.Contains(did, want) {
			t.Errorf("%q missing from the checkout: %s", want, did)
		}
	}

	// The report carries the publish line, because it holds the issue node id
	// and the branch — two things nobody should have to work out.
	// --version included, and that is the assertion: an environment is per
	// (module x core) and publish defaults the core to core_versions[0], so a
	// line without it sends somebody to the wrong environment. This line
	// shipped without it once and did exactly that.
	if !strings.Contains(stdout,
		"upkeep publish jumplinks 3628056 --branch project-update-bot-only --version=11") {
		t.Errorf("the report does not say how to send the work back:\n%s", stdout)
	}
	// And the local check that needs neither a push nor a merge request.
	if !strings.Contains(stdout, "--working-copy") {
		t.Errorf("the report does not name the local check:\n%s", stdout)
	}
}

// It never asks whether the code declares the target core.
//
// The whole reason to take over a merge request is often that it does *not*
// support the core yet — a Project Update Bot MR stopping short of the
// info.yml change is the common case. Refusing the checkout because the branch
// lacks what you are about to add would be the tool declining its own purpose.
// `check` asks that question, where a verdict is produced.
func TestCheckingOutAMergeRequestDoesNotRequireTheCoreToBeDeclared(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)

	code, _, stderr, engine := runCheckoutCommand(t, scene,
		"checkout", "jumplinks", "1", "--version=12", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d — it refused a core the merge request does not declare (%s)", code, stderr)
	}
	if len(engine.mrCheckouts) != 1 {
		t.Errorf("it did not check the branch out: %v", engine.mrCheckouts)
	}
	// Nothing was read from the repository to decide: no info.yml, no refusal.
	if strings.Contains(stderr, "core_version_requirement") {
		t.Errorf("it consulted the declared core: %s", stderr)
	}
}

// A branch on the project itself is checked out, not refused.
//
// This used to refuse, on the measurement that 100 of 100 open pathauto merge
// requests came from an issue fork — which is what drupal.org's issue queue
// produces, since the issue page mints the fork and a contributor has no write
// access. A project whose issues live in GitLab has no such step: on `helper`,
// four of five open merge requests are same-project, because the maintainer
// pushes branches straight to the canonical repository. Generalising from one
// project made the command useless on exactly those projects.
func TestABranchOnTheProjectItselfIsCheckedOut(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)
	scene.sourceProject = 1

	code, stdout, stderr, engine := runCheckoutCommand(t, scene,
		"checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(engine.mrCheckouts) != 1 {
		t.Fatalf("it did not check the branch out: %v", engine.mrCheckouts)
	}
	did := engine.mrCheckouts[0]
	// Pushed over SSH to the project GitLab advertises, under a name that is
	// not origin — origin stays HTTPS and anonymous so the fetch needs no key.
	if !strings.Contains(did, "canonical=git@git.drupal.org:project/jumplinks.git") {
		t.Errorf("the push remote is wrong: %s", did)
	}
	if !strings.Contains(did, "https://git.drupalcode.org/project/jumplinks.git") {
		t.Errorf("the fetch is not the anonymous URL: %s", did)
	}
	// No issue fork means no issue to publish against, so the report says to
	// push rather than naming a command that would refuse.
	if strings.Contains(stdout, "upkeep publish") {
		t.Errorf("it offered publish for a branch with no issue:\n%s", stdout)
	}
	if !strings.Contains(stdout, "push canonical project-update-bot-only") {
		t.Errorf("the report does not say how to send the work back:\n%s", stdout)
	}
	if !strings.Contains(stdout, "MR !1 is already open") {
		t.Errorf("the report does not say the push updates the MR:\n%s", stdout)
	}
}

// A project with no SSH URL advertised is refused: there is nowhere to push.
func TestAProjectWithNoSshUrlIsRefused(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)
	scene.sourceProject = 1
	scene.noSSH = true

	code, _, stderr, engine := runCheckoutCommand(t, scene,
		"checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if !strings.Contains(stderr, "no SSH URL") {
		t.Errorf("the refusal does not say why: %s", stderr)
	}
	if len(engine.mrCheckouts) != 0 {
		t.Errorf("it checked out anyway: %v", engine.mrCheckouts)
	}
}

// A source project that is not an issue fork of this project is refused:
// there is no issue to publish it against.
func TestABranchOnAnUnrelatedProjectIsRefused(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)
	scene.forks = `[]`

	code, _, stderr, _ := runCheckoutCommand(t, scene,
		"checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if !strings.Contains(stderr, "nor one of its issue forks") {
		t.Errorf("the refusal does not say why: %s", stderr)
	}
}

// A closed or merged merge request still has a branch, and checking it out is
// a reasonable thing to want — but a push to it will not reopen anything, so
// that is said before the work starts rather than after.
func TestAClosedMergeRequestWarnsAndStillChecksOut(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)
	scene.state = "merged"

	code, _, stderr, engine := runCheckoutCommand(t, scene,
		"checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "will not") {
		t.Errorf("it did not warn that a push changes nothing: %s", stderr)
	}
	if len(engine.mrCheckouts) != 1 {
		t.Errorf("it refused instead of warning: %v", engine.mrCheckouts)
	}
}

// A merge request iid that is not one is refused locally, before any request:
// !0 is not a merge request, and turning it into a 404 would be worse.
func TestANonMergeRequestIidIsRefusedBeforeAnyRequest(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)

	for _, iid := range []string{"0", "-1", "two"} {
		code, _, _, engine := runCheckoutCommand(t, scene,
			"checkout", "jumplinks", iid, "--cockpit="+root)

		if code != workflow.Infrastructure {
			t.Errorf("%q: exit %d", iid, code)
		}
		if len(engine.mrCheckouts) != 0 {
			t.Errorf("%q: it acted on it: %v", iid, engine.mrCheckouts)
		}
	}
}

// completion is wired, like every other subject command.
func TestCheckoutCompletesTheModule(t *testing.T) {
	cmd := NewCheckout(&fakeFactory{engine: &fakeEngine{}}, noClients{})

	if cmd.ValidArgsFunction == nil {
		t.Error("the module argument does not complete")
	}
	if cmd.Flags().Lookup(cli.FlagVersion) == nil {
		t.Error("it takes no --version")
	}
}

// Every read it makes can fail, and each failure names what could not be
// read rather than leaving the operator with a bare HTTP code.
func TestEveryReadAMergeRequestCheckoutMakesCanFail(t *testing.T) {
	for name, breaks := range map[string]func(*aForkedMr){
		"the merge request": func(s *aForkedMr) { s.breakMr = true },
		"the fork listing":  func(s *aForkedMr) { s.breakForks = true },
		"the fork itself":   func(s *aForkedMr) { s.breakFork = true },
	} {
		root := aJumplinksCockpit(t)
		scene := mrOnAFork(t)
		breaks(scene)

		code, _, stderr, engine := runCheckoutCommand(t, scene,
			"checkout", "jumplinks", "1", "--cockpit="+root)

		if code != workflow.Infrastructure {
			t.Errorf("%s: exit %d, want %d", name, code, workflow.Infrastructure)
		}
		if stderr == "" {
			t.Errorf("%s: it failed silently", name)
		}
		if len(engine.mrCheckouts) != 0 {
			t.Errorf("%s: it checked out anyway: %v", name, engine.mrCheckouts)
		}
	}
}

// An issue with no fork at all, though the merge request says its branch is
// there: a contradiction worth reporting rather than dereferencing.
func TestAnIssueWithNoForkIsReported(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)
	scene.forkMissing = true

	code, _, stderr, _ := runCheckoutCommand(t, scene,
		"checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if !strings.Contains(stderr, "no fork") {
		t.Errorf("the refusal does not say what is missing: %s", stderr)
	}
}

// A checkout the engine refuses is the command's failure: it must not report
// a branch it is not on.
func TestACheckoutTheEngineRefusesFailsTheCommand(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)

	engine := &fakeEngine{checkoutErr: errRefused}
	rootCmd := NewRoot(Surface{
		Engines: &fakeFactory{engine: engine},
		Clients: scriptedClients{client: scene.client()},
		Issues:  noIssues{}, Prompts: noPrompts,
		Volumes: noVolumes{}, Sizer: noSizer, Browser: cli.NoBrowser{},
	})

	code, stdout, _ := invokeWith(t, rootCmd, "checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if strings.Contains(stdout, "Branch") {
		t.Errorf("it reported a checkout that did not happen:\n%s", stdout)
	}
}

// errRefused stands in for an engine that will not do it.
var errRefused = errors.New("the engine refused")

// A project that cannot be resolved is reported with the module named, not as
// a bare HTTP code.
func TestAModuleWhoseProjectCannotBeResolvedIsReported(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)
	scene.breakProject = true

	code, _, stderr, _ := runCheckoutCommand(t, scene,
		"checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if !strings.Contains(stderr, "jumplinks") {
		t.Errorf("the failure does not name the module: %s", stderr)
	}
}

// An environment that cannot be provisioned fails before any checkout.
func TestAnEnvironmentThatCannotBeProvisionedStopsTheCheckout(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)

	engine := &fakeEngine{ensureErr: errRefused}
	rootCmd := NewRoot(Surface{
		Engines: &fakeFactory{engine: engine},
		Clients: scriptedClients{client: scene.client()},
		Issues:  noIssues{}, Prompts: noPrompts,
		Volumes: noVolumes{}, Sizer: noSizer, Browser: cli.NoBrowser{},
	})

	code, _, _ := invokeWith(t, rootCmd, "checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if len(engine.mrCheckouts) != 0 {
		t.Errorf("it checked out into an environment that does not exist: %v", engine.mrCheckouts)
	}
}

// sshOrNot omits the SSH URL, for the project GitLab advertises none for.
func sshOrNot(absent bool) string {
	if absent {
		return ""
	}

	return `"ssh_url_to_repo":"git@git.drupal.org:project/jumplinks.git",`
}

// A merge request that does not say where its branch is gets its own refusal.
//
// Zero means GitLab did not tell us, which is not the same as "it is on the
// project" — and saying the latter in the former's words would be a guess
// dressed as a fact. Two conditions, two messages.
func TestAMergeRequestWithNoSourceProjectIsRefused(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)
	scene.sourceProject = 0

	code, _, stderr, engine := runCheckoutCommand(t, scene,
		"checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if !strings.Contains(stderr, "does not say which project") {
		t.Errorf("the refusal invents a location: %s", stderr)
	}
	// Not the same-project message, and not the fork-listing one either.
	if strings.Contains(stderr, "itself") || strings.Contains(stderr, "issue forks") {
		t.Errorf("it blamed the wrong thing: %s", stderr)
	}
	if len(engine.mrCheckouts) != 0 {
		t.Errorf("it checked out anyway: %v", engine.mrCheckouts)
	}
}

// A fork GitLab lists but will not serve is reported rather than
// dereferenced.
func TestAForkThatCannotBeReadIsReported(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)
	scene.breakFork = true

	code, _, stderr, _ := runCheckoutCommand(t, scene,
		"checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if !strings.Contains(stderr, "could not be read") {
		t.Errorf("the failure does not say what could not be read: %s", stderr)
	}
}

// The command is `checkout`, and `check` is still its own command.
//
// They differ by three characters and do very different things — one produces
// a verdict on a disposable branch, the other puts your working copy on
// somebody's source branch — so the pair is worth pinning. Cobra matches a
// name exactly unless EnablePrefixMatching is set, and nothing here sets it.
func TestCheckoutAndCheckAreDistinctCommands(t *testing.T) {
	root := NewRootFor(noVolumes{}, noSizer)

	byName := map[string]*cobra.Command{}
	for _, command := range root.Commands() {
		byName[command.Name()] = command
	}

	for _, name := range []string{"check", "checkout"} {
		if byName[name] == nil {
			t.Fatalf("there is no %q command", name)
		}
	}
	if byName["check"] == byName["checkout"] {
		t.Error("one command answers to both names")
	}
	if byName["mr:checkout"] != nil {
		t.Error("the old namespaced name is still registered")
	}
}

// The old name fails, and the failure names the command to run.
//
// Not an alias: `mr:checkout` is gone and exits 2. But it was the name while
// this was being built, so it is in shell histories, and a bare "unknown
// command" is the dead end every other refusal in this tool avoids. Cobra's
// own suggestion would not fire — the edit distance is 3 against a minimum
// of 2 — so SuggestFor carries it.
func TestTheOldNameNamesTheNewOne(t *testing.T) {
	code, _, stderr := invoke(t, "mr:checkout", "jumplinks", "1")

	if code != workflow.Infrastructure {
		t.Errorf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if !strings.Contains(stderr, "checkout") {
		t.Errorf("the failure does not name the command to run: %q", stderr)
	}
	if !strings.Contains(stderr, "Did you mean") {
		t.Errorf("the failure does not offer it: %q", stderr)
	}
}

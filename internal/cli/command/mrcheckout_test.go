package command

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

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

// runMrCheckoutCommand invokes the tree with a scripted GitLab and a fake
// engine, and hands back the engine so the checkout can be asserted.
func runMrCheckoutCommand(
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

	code, stdout, stderr, engine := runMrCheckoutCommand(t, scene,
		"mr:checkout", "jumplinks", "1", "--cockpit="+root)

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

	code, _, stderr, engine := runMrCheckoutCommand(t, scene,
		"mr:checkout", "jumplinks", "1", "--version=12", "--cockpit="+root)

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

// A branch on the canonical project is refused, naming what to do instead:
// upkeep pushes to issue forks, which is where drupal.org puts contributions.
func TestAMergeRequestFromTheProjectItselfIsRefused(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)
	scene.sourceProject = 1

	code, _, stderr, engine := runMrCheckoutCommand(t, scene,
		"mr:checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	// A payload that does not carry source_project_id at all reads as zero,
	// and zero must refuse too rather than be looked up as a fork id.
	absent := mrOnAFork(t)
	absent.sourceProject = 0
	var absentErr string
	code, _, absentErr, _ = runMrCheckoutCommand(t, absent,
		"mr:checkout", "jumplinks", "1", "--cockpit="+aJumplinksCockpit(t))
	if code != workflow.Infrastructure {
		t.Errorf("a merge request with no source project was accepted: exit %d", code)
	}
	// Its own message: "on the project itself" would be a guess dressed as a
	// fact, and the fork-listing fallback would blame the wrong thing.
	if !strings.Contains(absentErr, "does not say which project") {
		t.Errorf("the refusal invents a location: %s", absentErr)
	}
	// The specific diagnosis, not the generic one: with the same-project case
	// removed, this falls through to "not an issue fork of", which also
	// mentions a fork and would let the loss pass unnoticed.
	if !strings.Contains(stderr, "itself rather than on an issue fork") {
		t.Errorf("the refusal does not say why: %s", stderr)
	}
	if len(engine.mrCheckouts) != 0 {
		t.Errorf("it checked something out anyway: %v", engine.mrCheckouts)
	}
}

// A source project that is not an issue fork of this project is refused:
// there is no issue to publish it against.
func TestABranchOnAnUnrelatedProjectIsRefused(t *testing.T) {
	root := aJumplinksCockpit(t)
	scene := mrOnAFork(t)
	scene.forks = `[]`

	code, _, stderr, _ := runMrCheckoutCommand(t, scene,
		"mr:checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if !strings.Contains(stderr, "not an issue fork") {
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

	code, _, stderr, engine := runMrCheckoutCommand(t, scene,
		"mr:checkout", "jumplinks", "1", "--cockpit="+root)

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
		code, _, _, engine := runMrCheckoutCommand(t, scene,
			"mr:checkout", "jumplinks", iid, "--cockpit="+root)

		if code != workflow.Infrastructure {
			t.Errorf("%q: exit %d", iid, code)
		}
		if len(engine.mrCheckouts) != 0 {
			t.Errorf("%q: it acted on it: %v", iid, engine.mrCheckouts)
		}
	}
}

// completion is wired, like every other subject command.
func TestMrCheckoutCompletesTheModule(t *testing.T) {
	cmd := NewMrCheckout(&fakeFactory{engine: &fakeEngine{}}, noClients{})

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

		code, _, stderr, engine := runMrCheckoutCommand(t, scene,
			"mr:checkout", "jumplinks", "1", "--cockpit="+root)

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

	code, _, stderr, _ := runMrCheckoutCommand(t, scene,
		"mr:checkout", "jumplinks", "1", "--cockpit="+root)

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

	code, stdout, _ := invokeWith(t, rootCmd, "mr:checkout", "jumplinks", "1", "--cockpit="+root)

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

	code, _, stderr, _ := runMrCheckoutCommand(t, scene,
		"mr:checkout", "jumplinks", "1", "--cockpit="+root)

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

	code, _, _ := invokeWith(t, rootCmd, "mr:checkout", "jumplinks", "1", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if len(engine.mrCheckouts) != 0 {
		t.Errorf("it checked out into an environment that does not exist: %v", engine.mrCheckouts)
	}
}

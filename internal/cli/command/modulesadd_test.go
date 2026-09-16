package command

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// aMembershipGitlab answers the memberships listing.
type aMembership struct {
	server *httptest.Server
	status int
	body   string
}

func memberOf(t *testing.T, body string) *aMembership {
	t.Helper()

	scene := &aMembership{status: http.StatusOK, body: body}
	scene.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if scene.status != http.StatusOK {
			w.WriteHeader(scene.status)
			_, _ = w.Write([]byte(`{"message":"500 Internal Server Error"}`))

			return
		}
		// One page, then empty — which is what ends the pagination. A stub
		// that answered every page identically would be a server that never
		// stops handing back full pages, which the client is right to refuse.
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))

			return
		}
		_, _ = w.Write([]byte(scene.body))
	}))
	t.Cleanup(scene.server.Close)

	return scene
}

func (s *aMembership) client() *gitlab.Client {
	return gitlab.NewClient(nil, "a-token-long-enough", s.server.URL+"/api/v4", s.server.URL)
}

// someMemberships is what a maintainer's account actually looks like: two
// contrib modules, one already registered, and a membership that is not a
// contrib module at all.
const someMemberships = `[
	{"id":1,"path":"token","path_with_namespace":"project/token"},
	{"id":2,"path":"pathauto","path_with_namespace":"project/pathauto"},
	{"id":3,"path":"field_helper","path_with_namespace":"project/field_helper"},
	{"id":4,"path":"infrastructure","path_with_namespace":"infrastructure/ci-templates"},
	{"id":5,"path":"nested","path_with_namespace":"project/group/nested"}
]`

// runModulesAddCommand invokes the tree with a scripted GitLab and prompt.
func runModulesAddCommand(
	t *testing.T, scene *aMembership, prompt scriptedPrompt, args ...string,
) (int, string, string) {
	t.Helper()

	at := 0
	prompt.at = &at

	root := NewRoot(Surface{
		Engines: &fakeFactory{engine: &fakeEngine{}},
		Clients: scriptedClients{client: scene.client()},
		Issues:  noIssues{},
		Prompts: func(*cobra.Command) cli.Prompt { return prompt },
		Volumes: noVolumes{}, Sizer: noSizer, Browser: cli.NoBrowser{},
	})

	return invokeWith(t, root, args...)
}

// registryOf reads back what was written.
func registryOf(t *testing.T, root string) map[string]cockpit.Module {
	t.Helper()

	where, err := cockpit.New(root)
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}
	modules, err := cli.Modules(where)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	return modules
}

// Names given on the command line are registered without a prompt.
func TestModulesAddRegistersWhatWasNamed(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{},
		"modules:add", "token", "field_helper", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	modules := registryOf(t, root)
	for _, name := range []string{"token", "field_helper"} {
		if _, registered := modules[name]; !registered {
			t.Errorf("%s was not registered: %v", name, modules)
		}
	}
	// The project path comes from GitLab rather than being assembled: that is
	// where the module actually lives, and this codebase does not build URLs
	// or paths out of parts it could have been told.
	if modules["token"].Project != "project/token" {
		t.Errorf("project %q", modules["token"].Project)
	}
	// And the default core is what the flag says it is.
	if strings.Join(modules["token"].CoreVersions, ",") != "11" {
		t.Errorf("cores %v", modules["token"].CoreVersions)
	}
	// The one that was already there is untouched, and nothing invented an
	// entry for the membership that is not a contrib module.
	if _, registered := modules["ci-templates"]; registered {
		t.Errorf("a non-contrib membership was registered: %v", modules)
	}
}

// --core-versions is what the new entries track.
func TestModulesAddTracksTheGivenCores(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{},
		"modules:add", "token", "--core-versions= 10 , 11 ", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if got := strings.Join(registryOf(t, root)["token"].CoreVersions, ","); got != "10,11" {
		t.Errorf("cores %q", got)
	}
}

// A core list naming nothing is refused before the listing: entries tracking
// no core would be registered and then useless.
func TestModulesAddRefusesAnEmptyCoreList(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{},
		"modules:add", "token", "--core-versions=, ,", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "at least one core major") {
		t.Errorf("stderr: %q", stderr)
	}
	if _, registered := registryOf(t, root)["token"]; registered {
		t.Error("it registered an entry tracking no core")
	}
}

// A name that is not one of your memberships is a typo or somebody else's
// project, and inventing a registry entry for it would send every survey
// command looking for a project that is not yours.
func TestModulesAddRefusesAModuleYouDoNotMaintain(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{},
		"modules:add", "token", "views", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "views") {
		t.Errorf("the refusal does not name what was wrong: %q", stderr)
	}
	// Nothing was written: the run refused as a whole rather than registering
	// the half it recognised.
	if _, registered := registryOf(t, root)["token"]; registered {
		t.Error("it registered part of a refused run")
	}
}

// Re-running with a name already registered does nothing for it rather than
// refusing — that is how somebody adds one more to a list they already typed.
func TestModulesAddAcceptsANameAlreadyRegistered(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{},
		"modules:add", "pathauto", "token", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	modules := registryOf(t, root)
	if _, registered := modules["token"]; !registered {
		t.Errorf("the new one was not registered: %v", modules)
	}
	// And the existing entry keeps its own core list rather than being
	// rewritten with the flag's default.
	if got := strings.Join(modules["pathauto"].CoreVersions, ","); got != "11" {
		t.Errorf("the existing entry was rewritten: %q", got)
	}
}

// Nothing left to offer says so rather than prompting with an empty list.
func TestModulesAddSaysWhenThereIsNothingToAdd(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, `[{"id":2,"path":"pathauto","path_with_namespace":"project/pathauto"}]`)

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{interactive: true},
		"modules:add", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "already registered") {
		t.Errorf("stderr: %q", stderr)
	}
}

// With no names and a terminal, the memberships are offered.
func TestModulesAddOffersTheUnregisteredMemberships(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)
	asked := []string{}

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{
		interactive: true, answers: []string{"field_helper"}, asked: &asked,
	}, "modules:add", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "3 unregistered") {
		t.Errorf("asked %v", asked)
	}

	modules := registryOf(t, root)
	if _, registered := modules["field_helper"]; !registered {
		t.Errorf("the chosen module was not registered: %v", modules)
	}
	// Only what was chosen: the list is an opt-in, not a default.
	if _, registered := modules["token"]; registered {
		t.Errorf("an unchosen module was registered: %v", modules)
	}
}

// Choosing nothing leaves the registry alone and says so.
func TestModulesAddChangesNothingWhenNothingIsChosen(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{interactive: true},
		"modules:add", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "registry unchanged") {
		t.Errorf("stderr: %q", stderr)
	}
	if len(registryOf(t, root)) != 1 {
		t.Errorf("the registry was changed: %v", registryOf(t, root))
	}
}

// No terminal and no names is a refusal that lists what could have been
// chosen, because a scripted run cannot be asked.
func TestModulesAddRefusesToGuessWithNoTerminal(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{},
		"modules:add", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	for _, expected := range []string{"non-interactive", "token", "field_helper"} {
		if !strings.Contains(stderr, expected) {
			t.Errorf("the refusal is missing %q: %q", expected, stderr)
		}
	}
}

// Memberships are a question about you, which GitLab will not answer
// anonymously.
func TestModulesAddNeedsACredential(t *testing.T) {
	root := aDashboardCockpit(t)

	root2 := NewRoot(Surface{
		Engines: &fakeFactory{engine: &fakeEngine{}}, Clients: scriptedClients{},
		Issues: noIssues{}, Prompts: noPrompts, Volumes: noVolumes{}, Sizer: noSizer,
		Browser: cli.NoBrowser{},
	})
	code, _, stderr := invokeWith(t, root2, "modules:add", "token", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "token") {
		t.Errorf("stderr: %q", stderr)
	}
}

// A listing that fails is reported with GitLab's own words, and nothing is
// written.
func TestModulesAddReportsAFailedListing(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)
	scene.status = http.StatusInternalServerError

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{},
		"modules:add", "token", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "memberships") {
		t.Errorf("stderr: %q", stderr)
	}
	if _, registered := registryOf(t, root)["token"]; registered {
		t.Error("it registered over a failed listing")
	}
}

// A registry that could not be written is never reported as modules
// registered: both halves of that sentence would be false.
func TestModulesAddNeverClaimsAWriteThatFailed(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)

	where, _ := cockpit.New(root)
	if err := os.Chmod(where.Root, 0o500); err != nil {
		t.Skipf("cannot make the cockpit read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(where.Root, 0o700) })
	if err := os.WriteFile(where.Root+"/probe", nil, 0o644); err == nil {
		t.Skip("the directory is still writable; probably running as root")
	}

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{},
		"modules:add", "token", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if strings.Contains(stderr, "Registered") {
		t.Errorf("a failed write was reported as a registration: %q", stderr)
	}
}

// The prompt offers the memberships in name order, so two runs show the same
// list and a number typed at it means the same thing twice.
func TestModulesAddOffersTheMembershipsInOrder(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)
	offered := &offeringPrompt{}

	tree := NewRoot(Surface{
		Engines: &fakeFactory{engine: &fakeEngine{}},
		Clients: scriptedClients{client: scene.client()},
		Issues:  noIssues{},
		Prompts: func(*cobra.Command) cli.Prompt { return offered },
		Volumes: noVolumes{}, Sizer: noSizer, Browser: cli.NoBrowser{},
	})
	if code, _, stderr := invokeWith(t, tree, "modules:add", "--cockpit="+root); code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	if strings.Join(offered.options, ",") != "field_helper,nested,token" {
		t.Errorf("offered %v", offered.options)
	}
}

// offeringPrompt records what it was offered and chooses nothing.
type offeringPrompt struct {
	options []string
}

func (p *offeringPrompt) Interactive() bool { return true }

func (p *offeringPrompt) Choose(string, []string) string { return "" }

func (p *offeringPrompt) ChooseMany(_ string, options []string) []string {
	p.options = options

	return nil
}

// The registry entry names the path GitLab gave, not one assembled from the
// machine name: the same rule as the push URL, for the same reason — a path
// built out of parts points somewhere that may not exist.
func TestModulesAddRecordsThePathGitlabGave(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := memberOf(t, someMemberships)

	code, _, stderr := runModulesAddCommand(t, scene, scriptedPrompt{},
		"modules:add", "nested", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if got := registryOf(t, root)["nested"].Project; got != "project/group/nested" {
		t.Errorf("project %q — it was assembled rather than taken", got)
	}
}

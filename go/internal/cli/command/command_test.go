package command

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/maintenance"
	"github.com/owenbush/upkeep/internal/workflow"
)

// invoke runs the whole command tree the way the binary does, and reports what
// the process would have exited with.
func invoke(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	return invokeWith(t, NewRootFor(noVolumes{}, noSizer), args...)
}

// invokeWith runs a given tree, for the commands whose behaviour depends on
// what they were wired to.
func invokeWith(t *testing.T, root *cobra.Command, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)

	code = cli.Execute(root, errOut)

	return code, out.String(), errOut.String()
}

// NewRootFor is the command tree with a stubbed engine factory, for the
// commands that do not need one.
func NewRootFor(volumes Volumes, sizer maintenance.Sizer) *cobra.Command {
	return NewRoot(adapter.NewDdevContribFactory(nil), noClients{}, volumes, sizer)
}

// noClients stands in for the GitLab client source where a command under test
// never reaches one.
type noClients struct{}

func (noClients) ReadOnly(gitlab.Report) *gitlab.Client { return gitlab.NewClient(nil, "", "", "") }

func (noClients) Authenticated(gitlab.Report) (*gitlab.Client, error) {
	return nil, errors.New("no GitLab token is configured")
}

// A fresh cockpit is every directory the tool reads from plus a registry that
// shows the shape of an entry.
func TestInitScaffoldsAWholeCockpit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cockpit")

	code, stdout, _ := invoke(t, "init", root)
	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, root) {
		t.Errorf("it did not say where: %q", stdout)
	}

	where, err := cockpit.New(root)
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}
	for _, path := range []string{
		where.RegistryPath(),
		where.BaseArtifactsPath(), where.FixturesPath(), where.ProjectsPath(),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was not created", path)
		}
	}

	// The registry is immediately loadable, and shows what an entry looks
	// like: the first thing anyone does next is add one.
	registry, err := os.ReadFile(where.RegistryPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(registry), "core_versions") {
		t.Errorf("the template shows no entry shape:\n%s", registry)
	}
	if _, err := cli.Modules(where); err != nil {
		t.Errorf("a freshly created registry does not load: %v", err)
	}

	// The three directories are empty by design, and git does not track empty
	// directories — so a cockpit kept in version control would lose them.
	for _, dir := range []string{
		where.BaseArtifactsPath(), where.FixturesPath(), where.ProjectsPath(),
	} {
		if _, err := os.Stat(filepath.Join(dir, ".gitkeep")); err != nil {
			t.Errorf("%s would not survive a commit", dir)
		}
	}
}

// Scaffolding over an existing cockpit would replace the registry, which is
// the one file nobody can reconstruct.
func TestInitRefusesOverAnExistingCockpit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cockpit")
	if code, _, _ := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("exit %d", code)
	}

	where, _ := cockpit.New(root)
	if err := os.WriteFile(where.RegistryPath(), []byte("modules:\n  token:\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, _, stderr := invoke(t, "init", root)
	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "already exists") {
		t.Errorf("stderr: %q", stderr)
	}

	// And the registry is exactly as it was.
	contents, err := os.ReadFile(where.RegistryPath())
	if err != nil || !strings.Contains(string(contents), "token") {
		t.Errorf("the existing registry was replaced: %q (%v)", contents, err)
	}
}

// A directory named registry.yml is not a cockpit, and claiming it is would
// send somebody looking for one.
func TestADirectoryNamedLikeARegistryIsNotACockpit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cockpit")
	if err := os.MkdirAll(filepath.Join(root, cockpit.RegistryFilename), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	code, _, stderr := invoke(t, "init", root)
	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if strings.Contains(stderr, "already exists") {
		t.Errorf("a directory was reported as an existing cockpit: %q", stderr)
	}
	if !strings.Contains(stderr, cockpit.RegistryFilename) {
		t.Errorf("the refusal does not name the path in the way: %q", stderr)
	}
}

// The listing, and the empty case that tells somebody where to add entries.
func TestModulesListsTheRegistryAndSaysWhereToAddTo(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cockpit")
	if code, _, _ := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("exit %d", code)
	}

	code, stdout, _ := invoke(t, "modules", "--cockpit="+root)
	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, filepath.Join(root, cockpit.RegistryFilename)) {
		t.Errorf("an empty registry did not say where to add: %q", stdout)
	}

	where, _ := cockpit.New(root)
	registry := "modules:\n" +
		"  token:\n    project: project/token\n    core_versions: [\"10\", \"11\"]\n" +
		"  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\"]\n"
	if err := os.WriteFile(where.RegistryPath(), []byte(registry), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, stdout, _ = invoke(t, "modules", "--cockpit="+root)
	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	for _, expected := range []string{"pathauto", "project/pathauto", "10, 11", "token"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the listing is missing %q:\n%s", expected, stdout)
		}
	}
	// Name order, so two runs agree with each other.
	if strings.Index(stdout, "pathauto") > strings.Index(stdout, "token") {
		t.Errorf("the listing is unordered:\n%s", stdout)
	}
}

// A malformed registry is reported once, by the command that needed it.
func TestAMalformedRegistryIsAnInfrastructureFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, cockpit.RegistryFilename), []byte("\tnot: [yaml"), 0o644,
	); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, stdout, stderr := invoke(t, "modules", "--cockpit="+root)
	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if stdout != "" {
		t.Errorf("a failed command still produced output: %q", stdout)
	}
	if stderr == "" {
		t.Error("it failed silently")
	}
}

// `--version` is the target core selector on the commands that take it, so the
// application version is a command of its own — one spelling cannot mean both.
func TestTheApplicationVersionIsACommandNotAFlag(t *testing.T) {
	code, stdout, _ := invoke(t, "version")
	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if strings.TrimSpace(stdout) != Version {
		t.Errorf("got %q, want %q", stdout, Version)
	}
}

// Shell completion comes for free and upkeep needs it, which is why the CLI
// framework was chosen for it.
func TestCompletionIsGeneratedForEveryShell(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		code, stdout, stderr := invoke(t, "completion", shell)
		if code != workflow.OK {
			t.Errorf("%s: exit %d (%s)", shell, code, stderr)
		}
		if len(stdout) < 100 {
			t.Errorf("%s: produced nothing usable: %q", shell, stdout)
		}
	}
}

// Every command is reachable and describes itself: the command list is the
// first thing anybody reads.
func TestEveryCommandIsDescribed(t *testing.T) {
	root := NewRootFor(noVolumes{}, noSizer)

	for _, command := range root.Commands() {
		if command.Short == "" {
			t.Errorf("%q describes itself as nothing", command.Name())
		}
		if strings.HasSuffix(command.Short, ".") {
			t.Errorf("%q ends its summary with a full stop: %q", command.Name(), command.Short)
		}
	}
}

// Nothing outside the composition root names a concrete engine: every command
// that needs one is handed the factory.
func TestCommandsReceiveTheEngineFactoryRatherThanBuildingOne(t *testing.T) {
	var built []string
	root := NewRoot(recordingFactory{built: &built}, noClients{}, noVolumes{}, noSizer)

	if root.Use != "upkeep" {
		t.Errorf("root %q", root.Use)
	}
	// Assembling the tree must not build an engine: that resolves a projects
	// root and would refuse before a command that needs no environment ran.
	if len(built) != 0 {
		t.Errorf("building the command tree built an engine: %v", built)
	}
}

// noVolumes stands in for the engine's volume listing: a status run in a test
// has no container runtime and must not need one.
type noVolumes struct{}

func (noVolumes) Volumes() []adapter.ProjectVolume { return nil }

// noSizer measures nothing, so a listing test is about the listing.
func noSizer(string) int64 { return 0 }

// recordingFactory stands in for the engine factory.
type recordingFactory struct{ built *[]string }

func (f recordingFactory) Build(
	where *cockpit.Cockpit, projectsRootOption string,
	stageLog adapter.Log, processLog func(string), onIdle func(),
) (adapter.Engine, error) {
	*f.built = append(*f.built, where.Root)

	return nil, nil
}

// writeFile replaces a file's contents.
func writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o644)
}

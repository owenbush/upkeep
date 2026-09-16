package command

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/proc"
	"github.com/owenbush/upkeep/internal/workflow"
)

// buildingRunner answers the build's shell-outs, recording what it was asked.
type buildingRunner struct {
	commands []string
	// does runs when a command matching a fragment is seen, so a step can
	// leave behind what the next step reads.
	does map[string]func([]string)
	fail map[string]error
}

func aBuildingRunner(t *testing.T) *buildingRunner {
	t.Helper()

	runner := &buildingRunner{does: map[string]func([]string){}, fail: map[string]error{}}

	// create-project's third argument is where the resolved tree goes, and the
	// lock file is what the build reads the core version out of.
	runner.does["composer create-project"] = func(command []string) {
		treePath := command[3]
		if err := os.MkdirAll(treePath, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		lock := `{"packages":[{"name":"drupal/core","version":"11.4.6"}]}`
		if err := os.WriteFile(
			filepath.Join(treePath, "composer.lock"), []byte(lock), 0o644,
		); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	runner.does["rm -rf"] = func(command []string) { _ = os.RemoveAll(command[2]) }

	return runner
}

func (r *buildingRunner) record(command []string) error {
	line := strings.Join(command, " ")
	r.commands = append(r.commands, line)

	for fragment, err := range r.fail {
		if strings.Contains(line, fragment) {
			return err
		}
	}
	for fragment, does := range r.does {
		if strings.Contains(line, fragment) {
			does(command)
		}
	}

	return nil
}

func (r *buildingRunner) Run(command []string, _ string, _ time.Duration) (string, error) {
	if err := r.record(command); err != nil {
		return "", err
	}

	return "", nil
}

func (r *buildingRunner) TryRun(command []string, _ string, _ time.Duration) (string, bool) {
	err := r.record(command)

	return "", err == nil
}

func (r *buildingRunner) Capture(command []string, _ string, _ time.Duration) proc.Captured {
	if err := r.record(command); err != nil {
		return proc.Captured{Output: err.Error()}
	}

	return proc.Captured{}
}

// ran reports whether a command matching a fragment was run.
func (r *buildingRunner) ran(fragment string) bool {
	for _, command := range r.commands {
		if strings.Contains(command, fragment) {
			return true
		}
	}

	return false
}

// installingSite stands in for the throwaway project the clean install runs
// in, writing the dump the build then keeps.
type installingSite struct {
	installed []string
	tornDown  []string
	fail      error
}

func (s *installingSite) CleanInstallAndDump(
	_, _, _, projectName, dumpPath string,
) (baseartifact.InstallEnvironment, error) {
	s.installed = append(s.installed, projectName)
	if s.fail != nil {
		return baseartifact.InstallEnvironment{}, s.fail
	}
	if err := os.MkdirAll(filepath.Dir(dumpPath), 0o755); err == nil {
		_ = os.WriteFile(dumpPath, []byte("-- a database"), 0o644)
	}

	return baseartifact.InstallEnvironment{PHPVersion: "8.3.14", DBEngine: "mariadb:10.11"}, nil
}

func (s *installingSite) Teardown(_, projectName string) {
	s.tornDown = append(s.tornDown, projectName)
}

// runBuildCommand invokes the tree with the build wired to fakes.
func runBuildCommand(
	t *testing.T, runner *buildingRunner, site *installingSite, args ...string,
) (int, string, string) {
	t.Helper()

	factory := &fakeFactory{engine: &fakeEngine{}, runner: runner, site: site}
	root := NewRoot(Surface{
		Engines: factory, Clients: noClients{}, Issues: noIssues{}, Prompts: noPrompts,
		Volumes: noVolumes{}, Sizer: noSizer, Browser: cli.NoBrowser{},
	})

	return invokeWith(t, root, args...)
}

// A build produces both artifacts and says where they are, because the next
// thing somebody does is look at them.
func TestABuildProducesTheTreeAndTheDump(t *testing.T) {
	root := anEnvironmentCockpit(t)
	runner, site := aBuildingRunner(t), &installingSite{}

	code, stdout, stderr := runBuildCommand(t, runner, site,
		"base-artifacts:build", "--version=11", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, expected := range []string{"Drupal 11", "11.4.6", "8.3.14", "mariadb:10.11"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the report is missing %q:\n%s", expected, stdout)
		}
	}

	where, _ := cockpit.New(root)
	layout := baseartifact.NewLayout(where.BaseArtifactsPath())
	for _, path := range []func(string) (string, error){layout.TreePath, layout.DumpPath} {
		at, err := path("11")
		if err != nil {
			t.Fatalf("path: %v", err)
		}
		if _, err := os.Stat(at); err != nil {
			t.Errorf("%s is not there: %v", at, err)
		}
		if !strings.Contains(stdout, at) {
			t.Errorf("the report does not say where %s is:\n%s", at, stdout)
		}
	}
	// The install ran, and the throwaway project was disposed of afterwards.
	if len(site.installed) != 1 || len(site.tornDown) != 1 {
		t.Errorf("installed %v, tore down %v", site.installed, site.tornDown)
	}
}

// --version is required, and named: there is no sensible default core to build
// artifacts for, and guessing would build the wrong thing slowly.
func TestABuildRequiresACoreVersion(t *testing.T) {
	root := anEnvironmentCockpit(t)
	runner, site := aBuildingRunner(t), &installingSite{}

	code, _, stderr := runBuildCommand(t, runner, site,
		"base-artifacts:build", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "--version=11") {
		t.Errorf("the refusal does not show the flag: %q", stderr)
	}
	if len(runner.commands) != 0 {
		t.Errorf("it ran %v before knowing what to build", runner.commands)
	}
}

// --stability reaches composer's constraint. It is a flag and never a
// fallback: substituting a pre-release when a stable constraint resolves
// nothing would make every later verdict a statement about a tree nobody asked
// for.
func TestABuildCarriesTheStabilityToComposer(t *testing.T) {
	root := anEnvironmentCockpit(t)
	runner, site := aBuildingRunner(t), &installingSite{}

	code, _, stderr := runBuildCommand(t, runner, site,
		"base-artifacts:build", "--version=12", "--stability=alpha", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !runner.ran("^12@alpha") {
		t.Errorf("the stability did not reach composer: %v", runner.commands)
	}

	// And without it, the constraint is the plain one.
	plain := aBuildingRunner(t)
	runBuildCommand(t, plain, &installingSite{},
		"base-artifacts:build", "--version=12", "--cockpit="+root)
	if plain.ran("@alpha") {
		t.Errorf("a stability was substituted: %v", plain.commands)
	}
}

// A stability composer does not know is refused before anything is resolved.
func TestABuildRefusesAnUnknownStability(t *testing.T) {
	root := anEnvironmentCockpit(t)
	runner, site := aBuildingRunner(t), &installingSite{}

	code, _, stderr := runBuildCommand(t, runner, site,
		"base-artifacts:build", "--version=12", "--stability=nearly", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "alpha") {
		t.Errorf("the refusal does not list what composer knows: %q", stderr)
	}
	if len(runner.commands) != 0 {
		t.Errorf("it resolved %v against a stability composer does not know", runner.commands)
	}
}

// The scratch directory is held to the same $HOME containment rule as the
// projects root: the throwaway project is bind-mounted, and one outside the
// home directory produces an install that cannot start.
func TestABuildRefusesAScratchDirOutsideHome(t *testing.T) {
	root := anEnvironmentCockpit(t)
	runner, site := aBuildingRunner(t), &installingSite{}
	outside := t.TempDir()

	code, _, stderr := runBuildCommand(t, runner, site,
		"base-artifacts:build", "--version=11", "--scratch-dir="+outside, "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "--scratch-dir") {
		t.Errorf("the refusal does not name the flag: %q", stderr)
	}
	if len(runner.commands) != 0 {
		t.Errorf("it built %v somewhere the install could not start", runner.commands)
	}
}

// The default scratch directory is under $HOME, which is what makes it pass
// the rule above without anybody having to know about it.
func TestTheDefaultScratchDirIsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	if got := cli.DefaultScratchDir(); !strings.HasPrefix(got, home) {
		t.Errorf("the default is %q, outside %q", got, home)
	}

	// With no home there is nowhere right to put it, so the temp directory
	// stands and the containment rule refuses it a moment later — naming the
	// flag, rather than failing inside a container.
	t.Setenv("HOME", "")
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := cli.DefaultScratchDir(); got == "" {
		t.Error("it defaulted to nothing")
	}
}

// A build that fails leaves the existing set alone and says so: the whole
// point of staging beside it.
func TestAFailedBuildLeavesTheExistingSetAlone(t *testing.T) {
	root := anEnvironmentCockpit(t)

	if code, _, stderr := runBuildCommand(t, aBuildingRunner(t), &installingSite{},
		"base-artifacts:build", "--version=11", "--cockpit="+root); code != workflow.OK {
		t.Fatalf("the first build: exit %d (%s)", code, stderr)
	}

	where, _ := cockpit.New(root)
	layout := baseartifact.NewLayout(where.BaseArtifactsPath())
	tree, _ := layout.TreePath("11")

	failing := &installingSite{fail: errors.New("the site install refused")}
	code, _, stderr := runBuildCommand(t, aBuildingRunner(t), failing,
		"base-artifacts:build", "--version=11", "--force", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "the site install refused") {
		t.Errorf("the reason was lost: %q", stderr)
	}
	// The set that was there is still there.
	if _, err := os.Stat(tree); err != nil {
		t.Errorf("a failed rebuild destroyed the existing set: %v", err)
	}
	// And the core still reads as built, so nothing downstream sees it vanish.
	versions, err := layout.VersionsOnDisk()
	if err != nil || len(versions) != 1 || versions[0] != "11" {
		t.Errorf("versions on disk: %v (%v)", versions, err)
	}
}

// Rebuilding over an existing set is deliberate: without --force it refuses
// rather than spending minutes replacing something somebody may be using.
func TestARebuildNeedsToBeAskedFor(t *testing.T) {
	root := anEnvironmentCockpit(t)

	if code, _, stderr := runBuildCommand(t, aBuildingRunner(t), &installingSite{},
		"base-artifacts:build", "--version=11", "--cockpit="+root); code != workflow.OK {
		t.Fatalf("the first build: exit %d (%s)", code, stderr)
	}

	runner := aBuildingRunner(t)
	code, _, stderr := runBuildCommand(t, runner, &installingSite{},
		"base-artifacts:build", "--version=11", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("the refusal does not say how to mean it: %q", stderr)
	}
	if len(runner.commands) != 0 {
		t.Errorf("it resolved %v before refusing", runner.commands)
	}
}

// A cockpit that is not one is refused before a build that takes minutes.
func TestABuildRequiresARealCockpit(t *testing.T) {
	runner, site := aBuildingRunner(t), &installingSite{}

	code, _, stderr := runBuildCommand(t, runner, site,
		"base-artifacts:build", "--version=11", "--cockpit="+filepath.Join(t.TempDir(), "nowhere"))

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if stderr == "" {
		t.Error("it failed silently")
	}
	if len(runner.commands) != 0 {
		t.Errorf("it built %v without a cockpit to build into", runner.commands)
	}
}

// --stability completes: composer's stability names are a closed set upkeep
// already holds, so suggesting them costs nothing.
func TestTheStabilityFlagCompletes(t *testing.T) {
	root := anEnvironmentCockpit(t)

	code, stdout, stderr := runBuildCommand(t, aBuildingRunner(t), &installingSite{},
		cobra.ShellCompRequestCmd, "base-artifacts:build", "--cockpit="+root, "--stability=")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, stability := range baseartifact.Stabilities {
		if !strings.Contains(stdout, stability) {
			t.Errorf("%q is not suggested:\n%s", stability, stdout)
		}
	}
}

// A core major that cannot name a directory is refused before the build, not
// at the end of one that takes minutes.
func TestABuildRefusesAnImpossibleCoreVersion(t *testing.T) {
	root := anEnvironmentCockpit(t)

	for _, version := range []string{"eleven", "11.4", "../escape", "-1"} {
		runner := aBuildingRunner(t)

		code, _, stderr := runBuildCommand(t, runner, &installingSite{},
			"base-artifacts:build", "--version="+version, "--cockpit="+root)

		if code != workflow.Infrastructure {
			t.Errorf("%q gave exit %d", version, code)
		}
		if stderr == "" {
			t.Errorf("%q failed silently", version)
		}
		if len(runner.commands) != 0 {
			t.Errorf("%q ran %v before refusing", version, runner.commands)
		}
	}
}

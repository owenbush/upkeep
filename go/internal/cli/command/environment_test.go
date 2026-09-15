package command

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// fakeEngine answers the few engine calls a command makes, and records them.
type fakeEngine struct {
	adapter.Engine

	envPaths    map[string]string
	ensured     []string
	environment adapter.Environment
	ensureErr   error
	checkedOut  []string
	checkoutErr error
	stage       adapter.Log
}

func (e *fakeEngine) ResolveEnvPath(moduleName, coreMajor string) string {
	return e.envPaths[moduleName+"/"+coreMajor]
}

func (e *fakeEngine) EnsureEnv(module cockpit.Module, coreMajor string) (adapter.Environment, error) {
	e.ensured = append(e.ensured, module.Name+"/"+coreMajor)
	if e.stage != nil {
		e.stage("Provisioning environment " + module.Name + " ...")
	}
	if e.ensureErr != nil {
		return adapter.Environment{}, e.ensureErr
	}

	return e.environment, nil
}

func (e *fakeEngine) CheckoutBranch(_ adapter.Environment, branch string) error {
	e.checkedOut = append(e.checkedOut, branch)

	return e.checkoutErr
}

func (e *fakeEngine) ApplyMr(adapter.Environment, gitlab.MergeRequest) error { return nil }

func (e *fakeEngine) RunChecks(adapter.Environment, []check.Type) (check.RunResult, error) {
	return check.RunResult{}, nil
}

// fakeFactory hands out one engine and remembers what it was asked for.
type fakeFactory struct {
	engine *fakeEngine
	// replace stands in for the engine entirely, for the commands whose flow
	// needs more than the shared fake answers.
	replace      adapter.Engine
	projectsRoot string
	builds       int
	// quiet is whether the engine was built with nowhere to report progress,
	// which is what keeps a status line out of a path meant to be captured.
	quiet bool
}

func (f *fakeFactory) Build(
	where *cockpit.Cockpit, projectsRootOption string,
	stageLog adapter.Log, processLog func(string), onIdle func(),
) (adapter.Engine, error) {
	f.builds++
	f.projectsRoot = projectsRootOption
	f.quiet = stageLog == nil && processLog == nil && onIdle == nil
	f.engine.stage = stageLog

	// The first thing the real factory does, and the reason it can refuse: a
	// projects root outside the home directory is bind-mounted into a VM that
	// cannot see it, so an environment there could never start. A fake that
	// skipped this would let every command look like it enforces a rule none
	// of them had to pass through.
	if _, err := adapter.ResolveProjectsRoot(projectsRootOption, where.Root); err != nil {
		return nil, err
	}

	if f.replace != nil {
		if recording, isRecording := f.replace.(*checkingEngine); isRecording {
			recording.stage = stageLog
		}

		return f.replace, nil
	}

	return f.engine, nil
}

// anEnvironmentCockpit is a cockpit with one registered module.
func anEnvironmentCockpit(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	root := filepath.Join(home, "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}

	where, _ := cockpit.New(root)
	registry := "modules:\n  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\", \"10\"]\n"
	if err := os.WriteFile(where.RegistryPath(), []byte(registry), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return root
}

// runWithEngine invokes the tree against a scripted engine.
func runWithEngine(
	t *testing.T, engine *fakeEngine, args ...string,
) (int, string, string, *fakeFactory) {
	t.Helper()

	factory := &fakeFactory{engine: engine}
	root := NewRoot(factory, noClients{}, noPrompts, noVolumes{}, noSizer)

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)

	return cli.Execute(root, errOut), out.String(), errOut.String(), factory
}

// The path, and nothing else, so `cd $(upkeep env:path pathauto)` works.
func TestEnvPathPrintsThePathAlone(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{envPaths: map[string]string{
		"pathauto/11": "/projects/upkeep-pathauto-d11",
	}}

	code, stdout, stderr, _ := runWithEngine(t, engine,
		"env:path", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if stdout != "/projects/upkeep-pathauto-d11\n" {
		t.Errorf("stdout carried more than the path: %q", stdout)
	}
}

// An environment that is not there is a refusal naming the commands that make
// one — never a ten-minute provision nobody asked for.
func TestEnvPathRefusesRatherThanProvisioning(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{envPaths: map[string]string{}}

	code, stdout, stderr, _ := runWithEngine(t, engine,
		"env:path", "pathauto", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if stdout != "" {
		t.Errorf("stdout carried something for a path that does not exist: %q", stdout)
	}
	if !strings.Contains(stderr, "upkeep check") {
		t.Errorf("the refusal does not say how to make one: %q", stderr)
	}
	if len(engine.ensured) != 0 {
		t.Errorf("it provisioned: %v", engine.ensured)
	}
}

// The core is the module's first unless asked otherwise, and an untracked one
// is refused before the engine is even built.
func TestEnvPathTakesTheTargetCoreFromTheFlagOrTheRegistry(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{envPaths: map[string]string{
		"pathauto/11": "/projects/d11", "pathauto/10": "/projects/d10",
	}}

	_, stdout, _, _ := runWithEngine(t, engine, "env:path", "pathauto", "--cockpit="+root)
	if strings.TrimSpace(stdout) != "/projects/d11" {
		t.Errorf("the default core gave %q", stdout)
	}

	_, stdout, _, _ = runWithEngine(t, engine,
		"env:path", "pathauto", "--version=10", "--cockpit="+root)
	if strings.TrimSpace(stdout) != "/projects/d10" {
		t.Errorf("--version=10 gave %q", stdout)
	}

	code, _, stderr, factory := runWithEngine(t, engine,
		"env:path", "pathauto", "--version=9", "--cockpit="+root)
	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if factory.builds != 0 {
		t.Error("it built an engine for a core the module does not track")
	}
	if !strings.Contains(stderr, "does not track") {
		t.Errorf("stderr: %q", stderr)
	}
}

// exec's stdout belongs to the wrapped command.
func TestExecHandsStdoutToTheWrappedCommand(t *testing.T) {
	dir := t.TempDir()
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{envPaths: map[string]string{"pathauto/11": dir}}

	code, stdout, _, _ := runWithEngine(t, engine,
		"exec", "pathauto", "--cockpit="+root, "--", "sh", "-c", "echo payload")

	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	if strings.TrimSpace(stdout) != "payload" {
		t.Errorf("stdout: %q", stdout)
	}
}

// It runs in the environment directory, which is the whole point.
func TestExecRunsInTheEnvironmentDirectory(t *testing.T) {
	dir := t.TempDir()
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{envPaths: map[string]string{"pathauto/11": dir}}

	_, stdout, _, _ := runWithEngine(t, engine,
		"exec", "pathauto", "--cockpit="+root, "--", "pwd")

	if !strings.HasSuffix(strings.TrimSpace(stdout), filepath.Base(dir)) {
		t.Errorf("it ran somewhere else: %q, want %q", stdout, dir)
	}
}

// Every non-zero child code collapses to 1, so a child exiting 2 can never be
// mistaken for an upkeep infrastructure failure.
func TestEveryFailingChildCollapsesToOne(t *testing.T) {
	dir := t.TempDir()
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{envPaths: map[string]string{"pathauto/11": dir}}

	for _, childCode := range []string{"1", "2", "3", "127"} {
		code, _, _, _ := runWithEngine(t, engine,
			"exec", "pathauto", "--cockpit="+root, "--", "sh", "-c", "exit "+childCode)

		if code != workflow.Failed {
			t.Errorf("a child exiting %s gave upkeep exit %d", childCode, code)
		}
	}
}

// A child that never started is upkeep failing, not a verdict about the
// command: there is no code the child chose.
func TestAChildThatNeverStartedIsAnInfrastructureFailure(t *testing.T) {
	dir := t.TempDir()
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{envPaths: map[string]string{"pathauto/11": dir}}

	code, _, stderr, _ := runWithEngine(t, engine,
		"exec", "pathauto", "--cockpit="+root, "--", "no-such-binary-anywhere")

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if stderr == "" {
		t.Error("it failed silently")
	}
}

// exec needs a command to run.
func TestExecNeedsSomethingToRun(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{envPaths: map[string]string{"pathauto/11": t.TempDir()}}

	code, _, _, factory := runWithEngine(t, engine, "exec", "pathauto", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if factory.builds != 0 {
		t.Error("it resolved an environment for a command nobody gave")
	}
}

// dev provisions, and hands back where to go.
func TestDevProvisionsAndSaysWhereToGo(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{environment: adapter.Environment{
		ModuleName: "pathauto", CoreMajor: "11",
		ProjectName: "upkeep-pathauto-d11", ProjectPath: "/projects/upkeep-pathauto-d11",
		PrimaryURL: "https://upkeep-pathauto-d11.ddev.site",
	}}

	code, stdout, _, _ := runWithEngine(t, engine, "dev", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	if len(engine.ensured) != 1 || engine.ensured[0] != "pathauto/11" {
		t.Errorf("ensured %v", engine.ensured)
	}
	for _, expected := range []string{
		"upkeep-pathauto-d11",
		"/projects/upkeep-pathauto-d11/module",
		"https://upkeep-pathauto-d11.ddev.site",
		"cd /projects/upkeep-pathauto-d11/module",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the answer is missing %q:\n%s", expected, stdout)
		}
	}
}

// The provisioning transcript is progress, not the answer: it stays on stderr
// so `upkeep dev pathauto > where.txt` gets the four lines it asked for.
func TestDevsProgressStaysOffItsAnswer(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{environment: adapter.Environment{
		ProjectName: "upkeep-pathauto-d11", ProjectPath: "/projects/upkeep-pathauto-d11",
	}}

	_, stdout, stderr, _ := runWithEngine(t, engine, "dev", "pathauto", "--cockpit="+root)

	if strings.Contains(stdout, "Provisioning") {
		t.Errorf("progress landed in the answer:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Provisioning") {
		t.Errorf("progress went nowhere: %q", stderr)
	}
}

// --branch puts the working copy where the maintainer asked before saying it
// is ready.
func TestDevChecksOutTheBranchItWasGiven(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{environment: adapter.Environment{ProjectPath: "/projects/x"}}

	code, _, _, _ := runWithEngine(t, engine,
		"dev", "pathauto", "--branch=2.0.x", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	if len(engine.checkedOut) != 1 || engine.checkedOut[0] != "2.0.x" {
		t.Errorf("checked out %v", engine.checkedOut)
	}

	// And without the flag it touches nothing: a working copy left on a branch
	// is where somebody left it.
	engine.checkedOut = nil
	runWithEngine(t, engine, "dev", "pathauto", "--cockpit="+root)
	if len(engine.checkedOut) != 0 {
		t.Errorf("it moved the working copy unasked: %v", engine.checkedOut)
	}
}

// A checkout that fails is a failure, not an environment reported as ready on
// a branch it is not on.
func TestDevFailsWhenTheBranchWillNotCheckOut(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{
		environment: adapter.Environment{ProjectPath: "/projects/x"},
		checkoutErr: errors.New("no such branch"),
	}

	code, stdout, stderr, _ := runWithEngine(t, engine,
		"dev", "pathauto", "--branch=nope", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if strings.Contains(stdout, "cd ") {
		t.Errorf("it told somebody to go to an environment that is not ready:\n%s", stdout)
	}
	if !strings.Contains(stderr, "no such branch") {
		t.Errorf("stderr: %q", stderr)
	}
}

// The projects root reaches the factory on every command that takes the flag,
// so --projects-root is never quietly ignored.
func TestTheProjectsRootFlagReachesTheEngine(t *testing.T) {
	root := anEnvironmentCockpit(t)

	// Written out rather than appended to, because on exec everything after
	// "--" belongs to the wrapped command: a flag appended there would be
	// handed to the child, and the test would be asserting nothing.
	// Under the home directory, because the factory refuses anything else —
	// which is a different property, tested next to it.
	elsewhere := filepath.Join(os.Getenv("HOME"), "elsewhere")

	for name, args := range map[string][]string{
		"env:path": {"env:path", "pathauto", "--cockpit=" + root, "--projects-root=" + elsewhere},
		"dev":      {"dev", "pathauto", "--cockpit=" + root, "--projects-root=" + elsewhere},
		"exec": {
			"exec", "pathauto", "--cockpit=" + root, "--projects-root=" + elsewhere, "--", "true",
		},
	} {
		engine := &fakeEngine{
			envPaths:    map[string]string{"pathauto/11": t.TempDir()},
			environment: adapter.Environment{ProjectPath: "/projects/x"},
		}
		_, _, _, factory := runWithEngine(t, engine, args...)

		if factory.projectsRoot != elsewhere {
			t.Errorf("%s built an engine for %q", name, factory.projectsRoot)
		}
	}
}

// The two commands whose stdout is somebody else's get an engine that reports
// nothing: a status line would end up inside the path being captured or the
// output of the command being wrapped.
func TestThePassThroughCommandsGetASilentEngine(t *testing.T) {
	root := anEnvironmentCockpit(t)

	for name, args := range map[string][]string{
		"env:path": {"env:path", "pathauto", "--cockpit=" + root},
		"exec":     {"exec", "pathauto", "--cockpit=" + root, "--", "true"},
	} {
		engine := &fakeEngine{envPaths: map[string]string{"pathauto/11": t.TempDir()}}
		_, _, _, factory := runWithEngine(t, engine, args...)

		if !factory.quiet {
			t.Errorf("%s was given somewhere to print progress", name)
		}
	}

	// And dev, whose answer is its own, is not silent — a provision is minutes
	// long and a silent one is indistinguishable from a wedged one.
	engine := &fakeEngine{environment: adapter.Environment{ProjectPath: "/projects/x"}}
	_, _, _, factory := runWithEngine(t, engine, "dev", "pathauto", "--cockpit="+root)
	if factory.quiet {
		t.Error("dev provisions in silence")
	}
}

// Stdin reaches the wrapped command, so an interactive one works through the
// wrapper as it would without it.
func TestExecHandsStdinToTheWrappedCommand(t *testing.T) {
	dir := t.TempDir()
	root := anEnvironmentCockpit(t)
	engine := &fakeEngine{envPaths: map[string]string{"pathauto/11": dir}}

	factory := &fakeFactory{engine: engine}
	tree := NewRoot(factory, noClients{}, noPrompts, noVolumes{}, noSizer)
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	tree.SetOut(out)
	tree.SetErr(errOut)
	tree.SetIn(strings.NewReader("typed at the prompt\n"))
	tree.SetArgs([]string{"exec", "pathauto", "--cockpit=" + root, "--", "cat"})

	if code := cli.Execute(tree, errOut); code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, errOut)
	}
	if strings.TrimSpace(out.String()) != "typed at the prompt" {
		t.Errorf("stdin did not reach the command: %q", out)
	}
}

// The command names are the PHP's, colons and all.
func TestTheEnvironmentCommandsKeepTheirNames(t *testing.T) {
	root := NewRootFor(noVolumes{}, noSizer)

	names := map[string]bool{}
	for _, command := range root.Commands() {
		names[command.Name()] = true
	}
	for _, name := range []string{"env:path", "exec", "dev"} {
		if !names[name] {
			t.Errorf("%q is missing or renamed", name)
		}
	}
}

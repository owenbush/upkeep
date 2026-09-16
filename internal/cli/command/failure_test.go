package command

import (
	"errors"
	"strings"
	"testing"

	"os"
	"path/filepath"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// refusingEngine fails exactly one engine call and succeeds at the rest.
type refusingEngine struct {
	patchingEngine
	refuse string
}

func (e *refusingEngine) EnsureEnv(module cockpit.Module, coreMajor string) (adapter.Environment, error) {
	if e.refuse == "EnsureEnv" {
		return adapter.Environment{}, errors.New("EnsureEnv refused")
	}

	return e.patchingEngine.EnsureEnv(module, coreMajor)
}

func (e *refusingEngine) ApplyMr(environment adapter.Environment, mr gitlab.MergeRequest) error {
	if e.refuse == "ApplyMr" {
		return errors.New("ApplyMr refused")
	}

	return e.patchingEngine.ApplyMr(environment, mr)
}

func (e *refusingEngine) LoadFixture(environment adapter.Environment, name string) error {
	if e.refuse == "LoadFixture" {
		return errors.New("LoadFixture refused")
	}

	return e.patchingEngine.LoadFixture(environment, name)
}

func (e *refusingEngine) RunChecks(
	environment adapter.Environment, types []check.Type,
) (check.RunResult, error) {
	if e.refuse == "RunChecks" {
		return check.RunResult{}, errors.New("RunChecks refused")
	}

	return e.patchingEngine.RunChecks(environment, types)
}

func (e *refusingEngine) Serve(environment adapter.Environment) (adapter.ServeResult, error) {
	if e.refuse == "Serve" {
		return adapter.ServeResult{}, errors.New("Serve refused")
	}

	return e.patchingEngine.Serve(environment)
}

func (e *refusingEngine) CheckoutBranch(environment adapter.Environment, branch string) error {
	if e.refuse == "CheckoutBranch" {
		return errors.New("CheckoutBranch refused")
	}

	return e.patchingEngine.CheckoutBranch(environment, branch)
}

func (e *refusingEngine) ApplyPatch(
	environment adapter.Environment, patch adapter.PatchApplication, refresh adapter.BaseRefresh,
) error {
	if e.refuse == "ApplyPatch" {
		return errors.New("ApplyPatch refused")
	}

	return e.patchingEngine.ApplyPatch(environment, patch, refresh)
}

func (e *refusingEngine) PromotePatch(
	environment adapter.Environment, patch adapter.PatchApplication, branch adapter.IssueBranch,
	message string, refresh adapter.BaseRefresh, partial bool,
) (adapter.PatchPromotion, error) {
	if e.refuse == "PromotePatch" {
		return adapter.PatchPromotion{}, errors.New("PromotePatch refused")
	}

	return e.patchingEngine.PromotePatch(environment, patch, branch, message, refresh, partial)
}

func (e *refusingEngine) StartWork(
	_ adapter.Environment, _ adapter.IssueBranch, _ string, _ adapter.BaseRefresh,
) (bool, error) {
	if e.refuse == "StartWork" {
		return false, errors.New("StartWork refused")
	}

	return false, nil
}

func (e *refusingEngine) PushWork(
	_ adapter.Environment, _ adapter.IssueBranch, _ adapter.GitRemote,
) (string, error) {
	if e.refuse == "PushWork" {
		return "", errors.New("PushWork refused")
	}

	return "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", nil
}

func (e *refusingEngine) RecordedBaseBranch(adapter.Environment) string { return "2.0.x" }

// No engine failure may be swallowed.
//
// Every one of these calls is a `return err` away from a command reporting a
// verdict about work that never happened — a green suite over a tree that was
// not built, a site declared live that was never served. Written as one
// property rather than a test per call: the assertions would all say the same
// thing, and a command that grows a step would silently not get one.
func TestNoEngineFailureIsSwallowed(t *testing.T) {
	root := anEnvironmentCockpit(t)

	// Which calls each command is expected to make and must refuse on.
	runs := map[string]struct {
		args  []string
		calls []string
	}{
		"check --working-copy": {
			args:  []string{"check", "pathauto", "--working-copy", "--fixture=x", "--cockpit=" + root},
			calls: []string{"EnsureEnv", "LoadFixture", "RunChecks"},
		},
		"check <mr>": {
			args:  []string{"check", "pathauto", "12", "--fixture=x", "--cockpit=" + root},
			calls: []string{"EnsureEnv", "ApplyMr", "LoadFixture", "RunChecks"},
		},
		"review": {
			args:  []string{"review", "pathauto", "12", "--cockpit=" + root},
			calls: []string{"EnsureEnv", "ApplyMr", "Serve"},
		},
		"dev": {
			args:  []string{"dev", "pathauto", "--branch=2.0.x", "--cockpit=" + root},
			calls: []string{"EnsureEnv", "CheckoutBranch"},
		},
	}

	for name, run := range patchRuns(t, root) {
		runs[name] = run
	}
	// The issue loop's own two, which reach the engine through a different
	// harness because they need drupal.org and an issue fork to get there.
	runs["start"] = struct {
		args  []string
		calls []string
	}{
		args:  []string{"start", "pathauto", "3223746", "--cockpit=" + root},
		calls: []string{"EnsureEnv", "StartWork"},
	}
	runs["publish"] = struct {
		args  []string
		calls []string
	}{
		args:  []string{"publish", "pathauto", "3223746", "--cockpit=" + root},
		calls: []string{"EnsureEnv", "PushWork"},
	}

	var swallowed []string
	for name, run := range runs {
		for _, call := range run.calls {
			engine := &refusingEngine{patchingEngine: *aPatchingEngine(), refuse: call}
			engine.statusOK = true
			engine.status = adapter.WorkingCopyStatus{CurrentBranch: "2.0.x"}

			var code int
			var stdout, stderr string
			switch {
			case strings.HasPrefix(name, "patch:"):
				code, stdout, stderr = runPatchCommand(
					t, engine, patchIssues, scriptedPrompt{}, run.args...)
			case name == "start" || name == "publish":
				code, stdout, stderr = runIssueLoop(t, engine, someIssues(),
					scriptedClients{client: aForkScene(t).client()}, cli.NoBrowser{}, run.args...)
			default:
				client, done := aResolvableGitlab(t, nil)
				code, stdout, stderr = runCheckCommand(t, engine, client, run.args...)
				done()
			}

			if code != workflow.Infrastructure {
				swallowed = append(swallowed, name+" carried on past a failed "+call)

				continue
			}
			if !strings.Contains(stderr, call+" refused") {
				swallowed = append(swallowed, name+" lost the reason "+call+" failed")
			}
			// And nothing that reads as an answer was printed.
			if strings.Contains(stdout, "All checks green") ||
				strings.Contains(stdout, "is live for review") {
				swallowed = append(swallowed, name+" answered after a failed "+call)
			}
		}
	}

	if len(swallowed) > 0 {
		t.Errorf("engine failures were swallowed:\n  %s", strings.Join(swallowed, "\n  "))
	}
}

// patchRuns are the patch commands' engine calls, which need an issue and a
// patch host to reach at all.
func patchRuns(t *testing.T, root string) map[string]struct {
	args  []string
	calls []string
} {
	t.Helper()

	host, _ := aPatchHost(t, aPatchDiff)
	patchIssues = scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}

	return map[string]struct {
		args  []string
		calls []string
	}{
		"patch:apply": {
			args:  []string{"patch:apply", "pathauto", "3597857", "--cockpit=" + root},
			calls: []string{"EnsureEnv", "ApplyPatch"},
		},
		"patch:check": {
			args: []string{
				"patch:check", "pathauto", "3597857", "--fixture=x", "--cockpit=" + root,
			},
			calls: []string{"EnsureEnv", "ApplyPatch", "LoadFixture", "RunChecks"},
		},
		"patch:promote": {
			args:  []string{"patch:promote", "pathauto", "3597857", "--cockpit=" + root},
			calls: []string{"EnsureEnv", "PromotePatch"},
		},
	}
}

// patchIssues is what the patch runs resolve against, set by patchRuns.
var patchIssues IssueClients = noIssues{}

// And no cockpit failure may be either: every command that reads a registry
// reports a malformed one rather than working around it.
func TestNoCommandWorksAroundAMalformedRegistry(t *testing.T) {
	root := anEnvironmentCockpit(t)
	where, _ := cockpit.New(root)
	if err := writeFile(where.RegistryPath(), "\tnot: [yaml"); err != nil {
		t.Fatalf("write: %v", err)
	}

	for name, args := range map[string][]string{
		"check":         {"check", "pathauto", "--working-copy", "--cockpit=" + root},
		"review":        {"review", "pathauto", "12", "--cockpit=" + root},
		"dev":           {"dev", "pathauto", "--cockpit=" + root},
		"env:path":      {"env:path", "pathauto", "--cockpit=" + root},
		"exec":          {"exec", "pathauto", "--cockpit=" + root, "--", "true"},
		"modules":       {"modules", "--cockpit=" + root},
		"status":        {"status", "--cockpit=" + root},
		"merge":         {"merge", "--fast-lane", "--cockpit=" + root},
		"prune":         {"prune", "--all", "--cockpit=" + root},
		"patch:apply":   {"patch:apply", "pathauto", "3597857", "--cockpit=" + root},
		"patch:check":   {"patch:check", "pathauto", "3597857", "--cockpit=" + root},
		"patch:promote": {"patch:promote", "pathauto", "3597857", "--cockpit=" + root},
		"issue":         {"issue", "pathauto", "9", "--cockpit=" + root},
		"start":         {"start", "pathauto", "3223746", "--cockpit=" + root},
		"publish":       {"publish", "pathauto", "3223746", "--cockpit=" + root},
		"issues":        {"issues", "pathauto", "--cockpit=" + root},
		"patches":       {"patches", "--cockpit=" + root},
		"needs-work":    {"needs-work", "pathauto", "12", "--cockpit=" + root},
	} {
		code, stdout, stderr := runCheckCommand(t, aCheckingEngine(), nil, args...)

		if code != workflow.Infrastructure {
			t.Errorf("%s: exit %d over an unparseable registry", name, code)
		}
		if stdout != "" {
			t.Errorf("%s produced output anyway: %q", name, stdout)
		}
		if stderr == "" {
			t.Errorf("%s failed silently", name)
		}
	}
}

// A cockpit that cannot be resolved at all is refused by every command that
// takes the flag, before any work and with the same wording.
//
// `--cockpit=` is the case worth naming: an empty value is a mistake somebody
// made, not a request for the default, and silently falling back would run the
// command against whatever directory they happened to be in.
func TestNoCommandFallsBackFromAnUnusableCockpit(t *testing.T) {
	for name, args := range map[string][]string{
		"check":                 {"check", "pathauto", "--working-copy", "--cockpit="},
		"review":                {"review", "pathauto", "12", "--cockpit="},
		"dev":                   {"dev", "pathauto", "--cockpit="},
		"env:path":              {"env:path", "pathauto", "--cockpit="},
		"exec":                  {"exec", "pathauto", "--cockpit=", "--", "true"},
		"modules":               {"modules", "--cockpit="},
		"status":                {"status", "--cockpit="},
		"base-artifacts:status": {"base-artifacts:status", "--cockpit="},
		"merge":                 {"merge", "--fast-lane", "--cockpit="},
		"prune":                 {"prune", "--all", "--cockpit="},
		"patch:apply":           {"patch:apply", "pathauto", "3597857", "--cockpit="},
		"patch:check":           {"patch:check", "pathauto", "3597857", "--cockpit="},
		"patch:promote":         {"patch:promote", "pathauto", "3597857", "--cockpit="},
	} {
		code, stdout, stderr := runCheckCommand(t, aCheckingEngine(), nil, args...)

		if code != workflow.Infrastructure {
			t.Errorf("%s: exit %d for an empty --cockpit", name, code)
		}
		if stdout != "" {
			t.Errorf("%s produced output anyway: %q", name, stdout)
		}
		if stderr == "" {
			t.Errorf("%s failed silently", name)
		}
	}
}

// A cockpit whose registry is missing entirely is the ordinary first-run
// mistake, and every command says the same thing about it.
func TestAMissingRegistryIsReportedTheSameWayEverywhere(t *testing.T) {
	empty := t.TempDir()

	for name, args := range map[string][]string{
		"modules":               {"modules", "--cockpit=" + empty},
		"status":                {"status", "--cockpit=" + empty},
		"base-artifacts:status": {"base-artifacts:status", "--cockpit=" + empty},
		"check":                 {"check", "pathauto", "--working-copy", "--cockpit=" + empty},
		"prune":                 {"prune", "--all", "--cockpit=" + empty},
	} {
		code, _, stderr := runCheckCommand(t, aCheckingEngine(), nil, args...)

		if code != workflow.Infrastructure {
			t.Errorf("%s: exit %d", name, code)
		}
		if !strings.Contains(stderr, "upkeep init") {
			t.Errorf("%s did not say how to make a cockpit: %q", name, stderr)
		}
	}
}

// A projects root that could never work is refused by every command that
// needs an environment.
//
// The tree is bind-mounted into the container runtime's VM and the macOS
// providers only share the home directory, so one outside it can never start.
// Refused before any work rather than discovered at provisioning time.
func TestNoCommandWorksWithAProjectsRootThatCouldNeverStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	root := filepath.Join(home, "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}
	where, _ := cockpit.New(root)
	registry := "modules:\n  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\"]\n"
	if err := os.WriteFile(where.RegistryPath(), []byte(registry), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	outside := t.TempDir()

	for name, args := range map[string][]string{
		"check":       {"check", "pathauto", "--working-copy"},
		"dev":         {"dev", "pathauto"},
		"env:path":    {"env:path", "pathauto"},
		"status":      {"status"},
		"prune":       {"prune", "--all"},
		"patch:apply": {"patch:apply", "pathauto", "3597857"},
		"review":      {"review", "pathauto", "12"},
	} {
		code, stdout, stderr := runCheckCommand(t, aCheckingEngine(), nil,
			append(args, "--cockpit="+root, "--projects-root="+outside)...)

		if code != workflow.Infrastructure {
			t.Errorf("%s: exit %d", name, code)
		}
		if !strings.Contains(stderr, "home directory") {
			t.Errorf("%s: the refusal does not say the rule: %q", name, stderr)
		}
		if stdout != "" {
			t.Errorf("%s produced output anyway: %q", name, stdout)
		}
	}
}

// A check with no output at all still gets a line: a blank under a "FAILED"
// heading reads as the report being broken rather than as the check having
// said nothing.
func TestAFailingCheckWithNoOutputStillSaysSomething(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := filepath.Join(home, "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}
	where, _ := cockpit.New(root)
	if err := os.WriteFile(where.RegistryPath(),
		[]byte("modules:\n  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\"]\n"),
		0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	engine := aCheckingEngine()
	engine.statusOK = true
	one := 1
	engine.run = check.RunResult{Results: []check.Result{
		{Type: check.PhpUnit, Status: check.Failed, ExitCode: &one, Output: ""},
	}}

	_, stdout, _ := runCheckCommand(t, engine, nil,
		"check", "pathauto", "--working-copy", "--cockpit="+root)

	if !strings.Contains(stdout, "(no output captured)") {
		t.Errorf("a silent failure printed a blank:\n%s", stdout)
	}
}

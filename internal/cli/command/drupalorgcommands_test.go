package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupalorgcli"
	"github.com/owenbush/upkeep/internal/workflow"
)

// stubLogVar names the file the stub drupalorg appends each call to. Set per
// test, so one stub binary serves the whole package.
const stubLogVar = "UPKEEP_TEST_DRUPALORG_LOG"

// TestMain installs one stub drupalorg for the package.
//
// One for the whole process, because drupalorgcli checks the binary and its
// version once per process and this package cannot reset that: a stub set per
// test would be ignored after the first. Each call appends "<cwd>|<argv>" to
// the file stubLogVar names, so a test reads what was run and where — and an
// absent file is proof nothing was. STUB_DRUPALORG_FAIL makes it fail the way
// the real CLI does on a missing issue.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "upkeep-drupalorg-stub-")
	if err != nil {
		panic(err)
	}
	stub := filepath.Join(dir, "drupalorg")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo 'Drupal.org CLI 0.10.3'; exit 0; fi
if [ -n "$` + stubLogVar + `" ]; then echo "$(pwd -P)|$*" >> "$` + stubLogVar + `"; fi
if [ -n "$STUB_DRUPALORG_FAIL" ]; then echo "Work item not found." >&2; exit 1; fi
echo "stub did $1"
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		panic(err)
	}
	if err := os.Setenv(drupalorgcli.BinEnvVar, stub); err != nil {
		panic(err)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// drupalorgCalls points the stub's log at a fresh file and returns a reader
// for what was run.
func drupalorgCalls(t *testing.T) func() []string {
	t.Helper()

	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv(stubLogVar, log)

	return func() []string {
		raw, err := os.ReadFile(log)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatalf("read stub log: %v", err)
		}

		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
}

// argvOf drops the cwd half of a logged call.
func argvOf(call string) string {
	_, argv, _ := strings.Cut(call, "|")

	return argv
}

// The commands that post on drupal.org under the maintainer's name refuse
// with nobody there to confirm, and run nothing first.
func TestPostingCommandsRefuseWithNobodyThereToConfirm(t *testing.T) {
	root := aDashboardCockpit(t)

	for _, args := range [][]string{
		{"issue:fork", "pathauto", "3223746"},
		{"issue:get-access", "pathauto", "3223746"},
		{"issue:assign", "pathauto", "3223746"},
		{"issue:unassign", "pathauto", "3223746", "someone"},
		{"issue:reassign", "pathauto", "3223746", "someone"},
		{"issue:label", "pathauto", "3223746", "foo"},
		{"issue:unlabel", "pathauto", "3223746", "foo"},
		{"issue:relabel", "pathauto", "3223746", "foo"},
	} {
		t.Run(args[0], func(t *testing.T) {
			calls := drupalorgCalls(t)

			code, stdout, stderr := invoke(t, append(args, "--cockpit="+root)...)

			if code != workflow.Infrastructure {
				t.Errorf("exit %d, want %d", code, workflow.Infrastructure)
			}
			if got := calls(); got != nil {
				t.Errorf("drupalorg ran before the gate: %v", got)
			}
			if stdout != "" {
				t.Errorf("stdout: %q", stdout)
			}
			for _, want := range []string{"project/pathauto#3223746", "under your name", "--yes"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal is missing %q: %q", want, stderr)
				}
			}
		})
	}
}

// --yes goes through, with the issue qualified by its project so the action
// cannot land on another project's issue of the same number, and the
// remaining arguments passed after "--" so none can be read as an option.
func TestPostingCommandsPassAProjectQualifiedRef(t *testing.T) {
	root := aDashboardCockpit(t)
	calls := drupalorgCalls(t)

	code, stdout, stderr := invoke(t,
		"issue:label", "pathauto", "3223746", "state::needsReview", "Novice", "--yes", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	got := calls()
	want := "issue:label --no-interaction -- project/pathauto#3223746 state::needsReview Novice"
	if len(got) != 1 || argvOf(got[0]) != want {
		t.Errorf("ran %v, want %q", got, want)
	}
	if stdout != "stub did issue:label\n" {
		t.Errorf("the CLI's output was not passed on: %q", stdout)
	}
}

// Without a cockpit, the module name is still enough: these commands need a
// project path, not a core or an environment, and drupal.org's convention
// gives it. A name that cannot be a machine name is still refused.
func TestProjectCommandsWorkWithoutACockpit(t *testing.T) {
	t.Setenv(cockpit.EnvVar, "")
	t.Chdir(t.TempDir())
	calls := drupalorgCalls(t)

	code, _, stderr := invoke(t, "issue:label", "content_sync", "3346618", "foo", "--yes")
	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if got := calls(); len(got) != 1 ||
		argvOf(got[0]) != "issue:label --no-interaction -- project/content_sync#3346618 foo" {
		t.Errorf("ran %v", got)
	}

	code, _, stderr = invoke(t, "issue:label", "Not-A-Module", "1", "foo", "--yes")
	if code != workflow.Infrastructure || !strings.Contains(stderr, "machine name") {
		t.Errorf("exit %d (%s)", code, stderr)
	}
}

// A cockpit named explicitly that has no registry is an error, not a quiet
// fall back to a guessed project path.
func TestANamedCockpitWithoutARegistryIsNotGuessedPast(t *testing.T) {
	calls := drupalorgCalls(t)

	code, _, stderr := invoke(t, "issue:fork", "pathauto", "22", "--yes", "--cockpit="+t.TempDir())

	if code != workflow.Infrastructure || !strings.Contains(stderr, "registry") {
		t.Errorf("exit %d (%s)", code, stderr)
	}
	if calls() != nil {
		t.Error("drupalorg ran")
	}
}

func TestArgumentsAreValidatedBeforeAnythingRuns(t *testing.T) {
	root := aDashboardCockpit(t)
	calls := drupalorgCalls(t)

	for _, args := range [][]string{
		{"issue:label", "pathauto", "abc", "foo", "--yes"},
		{"issue:setup-remote", "pathauto", "0"},
	} {
		code, _, stderr := invoke(t, append(args, "--cockpit="+root)...)
		if code != workflow.Infrastructure || !strings.Contains(stderr, "positive integer") {
			t.Errorf("%v: exit %d (%s)", args, code, stderr)
		}
	}
	if calls() != nil {
		t.Error("drupalorg ran")
	}
}

// The CLI failing is upkeep failing to do the job: exit 2, with its words.
func TestACLIFailureIsReported(t *testing.T) {
	root := aDashboardCockpit(t)
	drupalorgCalls(t)
	t.Setenv("STUB_DRUPALORG_FAIL", "1")

	code, stdout, stderr := invoke(t, "issue:fork", "pathauto", "22", "--yes", "--cockpit="+root)

	if code != workflow.Infrastructure || !strings.Contains(stderr, "not found") || stdout != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// setup-remote and the fork checkout run git through the CLI, so they run
// inside the module's working tree — the one env:path names — and nowhere
// else.
func TestWorkingTreeCommandsRunInTheModuleWorkingCopy(t *testing.T) {
	root := anEnvironmentCockpit(t)
	environment := t.TempDir()
	if err := os.Mkdir(filepath.Join(environment, "module"), 0o755); err != nil {
		t.Fatal(err)
	}
	moduleDir, _ := filepath.EvalSymlinks(filepath.Join(environment, "module"))
	engine := &fakeEngine{envPaths: map[string]string{"pathauto/11": environment}}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"issue:setup-remote", "pathauto", "3223746"},
			"issue:setup-remote --no-interaction -- 3223746"},
		{[]string{"issue:fork-checkout", "pathauto", "3223746"},
			"issue:checkout --no-interaction -- 3223746"},
		{[]string{"issue:fork-checkout", "pathauto", "3223746", "3223746-fix-it"},
			"issue:checkout --no-interaction -- 3223746 3223746-fix-it"},
	} {
		calls := drupalorgCalls(t)

		code, stdout, stderr, _ := runWithEngine(t, engine, append(tc.args, "--cockpit="+root)...)

		if code != workflow.OK {
			t.Fatalf("%v: exit %d (%s)", tc.args, code, stderr)
		}
		got := calls()
		if len(got) != 1 || got[0] != moduleDir+"|"+tc.want {
			t.Errorf("%v: ran %v, want %q in %s", tc.args, got, tc.want, moduleDir)
		}
		if !strings.HasPrefix(stdout, "stub did ") {
			t.Errorf("%v: stdout %q", tc.args, stdout)
		}
	}
}

// No environment is a refusal naming how to make one, as env:path's is — not
// git run somewhere else.
func TestWorkingTreeCommandsRefuseWithoutAnEnvironment(t *testing.T) {
	root := anEnvironmentCockpit(t)
	calls := drupalorgCalls(t)

	code, _, stderr, _ := runWithEngine(t, &fakeEngine{envPaths: map[string]string{}},
		"issue:setup-remote", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.Infrastructure || !strings.Contains(stderr, "upkeep check") {
		t.Errorf("exit %d (%s)", code, stderr)
	}
	if calls() != nil {
		t.Error("drupalorg ran")
	}
}

// Which commands are gated is the decision, so it is pinned: the ones that
// post under your name take --yes, the local and read-only ones do not need
// it, and the existing checkout keeps its name and its arguments.
func TestTheDrupalorgCommandSurface(t *testing.T) {
	gated := map[string]bool{
		"issue:fork": true, "issue:get-access": true, "issue:assign": true, "issue:unassign": true,
		"issue:reassign": true, "issue:label": true, "issue:unlabel": true, "issue:relabel": true,
		"issue:setup-remote": false, "issue:fork-checkout": false,
	}
	found := map[string]bool{}
	for _, cmd := range documentedCommands(t) {
		if cmd.Name() == "checkout" && cmd.Use != "checkout <module> <iid>" {
			t.Errorf("checkout changed: %q", cmd.Use)
		}
		wantGate, listed := gated[cmd.Name()]
		if !listed {
			continue
		}
		found[cmd.Name()] = true
		if hasYes := cmd.Flags().Lookup("yes") != nil; hasYes != wantGate {
			t.Errorf("%s: --yes present=%t, want %t", cmd.Name(), hasYes, wantGate)
		}
	}
	for name := range gated {
		if !found[name] {
			t.Errorf("%s is not registered", name)
		}
	}
}

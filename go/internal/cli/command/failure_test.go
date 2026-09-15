package command

import (
	"errors"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// refusingEngine fails exactly one engine call and succeeds at the rest.
type refusingEngine struct {
	checkingEngine
	refuse string
}

func (e *refusingEngine) EnsureEnv(module cockpit.Module, coreMajor string) (adapter.Environment, error) {
	if e.refuse == "EnsureEnv" {
		return adapter.Environment{}, errors.New("EnsureEnv refused")
	}

	return e.checkingEngine.EnsureEnv(module, coreMajor)
}

func (e *refusingEngine) ApplyMr(environment adapter.Environment, mr gitlab.MergeRequest) error {
	if e.refuse == "ApplyMr" {
		return errors.New("ApplyMr refused")
	}

	return e.checkingEngine.ApplyMr(environment, mr)
}

func (e *refusingEngine) LoadFixture(environment adapter.Environment, name string) error {
	if e.refuse == "LoadFixture" {
		return errors.New("LoadFixture refused")
	}

	return e.checkingEngine.LoadFixture(environment, name)
}

func (e *refusingEngine) RunChecks(
	environment adapter.Environment, types []check.Type,
) (check.RunResult, error) {
	if e.refuse == "RunChecks" {
		return check.RunResult{}, errors.New("RunChecks refused")
	}

	return e.checkingEngine.RunChecks(environment, types)
}

func (e *refusingEngine) Serve(environment adapter.Environment) (adapter.ServeResult, error) {
	if e.refuse == "Serve" {
		return adapter.ServeResult{}, errors.New("Serve refused")
	}

	return e.checkingEngine.Serve(environment)
}

func (e *refusingEngine) CheckoutBranch(environment adapter.Environment, branch string) error {
	if e.refuse == "CheckoutBranch" {
		return errors.New("CheckoutBranch refused")
	}

	return e.checkingEngine.CheckoutBranch(environment, branch)
}

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

	var swallowed []string
	for name, run := range runs {
		for _, call := range run.calls {
			engine := &refusingEngine{checkingEngine: *aCheckingEngine(), refuse: call}
			engine.statusOK = true
			engine.status = adapter.WorkingCopyStatus{CurrentBranch: "2.0.x"}

			client, done := aResolvableGitlab(t, nil)
			code, stdout, stderr := runCheckCommand(t, engine, client, run.args...)
			done()

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

// And no cockpit failure may be either: every command that reads a registry
// reports a malformed one rather than working around it.
func TestNoCommandWorksAroundAMalformedRegistry(t *testing.T) {
	root := anEnvironmentCockpit(t)
	where, _ := cockpit.New(root)
	if err := writeFile(where.RegistryPath(), "\tnot: [yaml"); err != nil {
		t.Fatalf("write: %v", err)
	}

	for name, args := range map[string][]string{
		"check":    {"check", "pathauto", "--working-copy", "--cockpit=" + root},
		"review":   {"review", "pathauto", "12", "--cockpit=" + root},
		"dev":      {"dev", "pathauto", "--cockpit=" + root},
		"env:path": {"env:path", "pathauto", "--cockpit=" + root},
		"exec":     {"exec", "pathauto", "--cockpit=" + root, "--", "true"},
		"modules":  {"modules", "--cockpit=" + root},
		"status":   {"status", "--cockpit=" + root},
		"merge":    {"merge", "--fast-lane", "--cockpit=" + root},
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

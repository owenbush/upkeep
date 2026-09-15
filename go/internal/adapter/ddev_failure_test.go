package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No step whose failure is fatal may be swallowed.
//
// Each of these operations is a sequence of commands, and every one of them
// is a `return err` away from being reported as success. Written as one
// property rather than a test per command: the individual assertions would all
// say the same thing, and a sequence that grows a step would silently not get
// one.
//
// The distinction the property turns on is which runner method a step went
// through. Run means the step must succeed; TryRun and Capture mean its
// failure is an answer — a probe for a ref that may not exist, a check whose
// red result is the point. So the run is made once with everything working, to
// learn which commands were required, and then once per required command with
// that one failing.
//
// Reported together rather than one at a time, because a sequence with two
// swallowed failures should say so in one run.
func TestNoFatalStepIsSwallowed(t *testing.T) {
	for name, newOperation := range fatalStepCases() {
		t.Run(name, func(t *testing.T) {
			// One scene for every attempt: the commands carry absolute paths,
			// and a scene rebuilt per attempt would name different ones, so
			// the failure being scripted would match nothing.
			operation := newOperation(t)

			runner := newRunner()
			if err := operation(runner); err != nil {
				t.Fatalf("the healthy run failed, so there is nothing to vary: %v\n%s",
					err, runner.transcript())
			}

			required := runner.mustSucceed()
			if len(required) == 0 {
				t.Fatal("the operation ran nothing that has to succeed")
			}

			var swallowed []string
			for _, command := range required {
				failing := newRunner()
				failing.fails(command)
				if err := operation(failing); err == nil {
					swallowed = append(swallowed, command)
				}
			}

			if len(swallowed) > 0 {
				t.Errorf(
					"these failed and the operation still reported success:\n  %s",
					strings.Join(swallowed, "\n  "),
				)
			}
		})
	}
}

// fatalStepCases is one entry per operation. The outer call builds the scene
// once; the inner one scripts and runs a healthy pass against a given runner,
// and may be called repeatedly.
func fatalStepCases() map[string]func(*testing.T) func(*recordingRunner) error {
	branch := IssueBranchFor(3601234, "Fix the thing")
	remote := IssueForkRemote(3601234, "git@git.drupal.org:issue/pathauto-3601234.git")

	return map[string]func(*testing.T) func(*recordingRunner) error{
		"ApplyMr": inEnvironment(func(environment Environment, runner *recordingRunner) error {
			scriptCleanOn(runner, "2.0.x").
				answer("ls-remote origin", "abc\t"+MergeRef(12)+"\n").
				answer("rev-parse --abbrev-ref HEAD", "mr-12\n").
				answer("rev-parse HEAD", "merge222\n")

			return engineWith(runner).ApplyMr(environment, anMr(12, "2.0.x"))
		}),

		"ApplyPatch": inEnvironment(func(environment Environment, runner *recordingRunner) error {
			scriptCleanOn(runner, "2.0.x").
				answer("rev-parse HEAD", "patched1\n").
				answer("config --get upkeep.base-branch", "2.0.x\n")

			return engineWith(runner).ApplyPatch(
				environment,
				PatchApplication{IssueNid: 3601234, LocalPath: "/tmp/p.patch", BaseBranch: "2.0.x"},
				RefreshUpdate,
			)
		}),

		"StartWork": inEnvironment(func(environment Environment, runner *recordingRunner) error {
			scriptCleanOn(runner, "2.0.x")
			_, err := engineWith(runner).StartWork(environment, branch, "2.0.x", RefreshUpdate)

			return err
		}),

		"PushWork": inEnvironment(func(environment Environment, runner *recordingRunner) error {
			scriptCleanOn(runner, "2.0.x").
				answer("symbolic-ref --short HEAD", branch.Name+"\n").
				answer("rev-parse HEAD", "pushed11\n")
			_, err := engineWith(runner).PushWork(environment, branch, remote)

			return err
		}),

		"CheckoutBranch": inEnvironment(func(environment Environment, runner *recordingRunner) error {
			scriptCleanOn(runner, "2.0.x")

			return engineWith(runner).CheckoutBranch(environment, "3.0.x")
		}),

		"Serve": inEnvironment(func(environment Environment, runner *recordingRunner) error {
			_, err := engineWith(runner).Serve(environment)

			return err
		}),

		"LoadFixture": inEnvironment(func(environment Environment, runner *recordingRunner) error {
			return engineWith(runner).LoadFixture(environment, "baseline")
		}),

		"EnsureEnv": func(t *testing.T) func(*recordingRunner) error {
			root := filepath.Join(t.TempDir(), "projects")
			layout := artifactsFor(t, "11", "11.4.6")
			projectPath := filepath.Join(root, "upkeep-pathauto-d11")

			return func(runner *recordingRunner) error {
				// Each attempt starts from nothing, or the one before it would
				// have left an environment to reuse instead of provisioning.
				if err := os.RemoveAll(projectPath); err != nil {
					t.Fatalf("remove: %v", err)
				}
				aSuccessfulProvision(t, runner, projectPath)
				_, err := NewDdevContrib(layout, root, runner, nil).EnsureEnv(pathauto, "11")

				return err
			}
		},
	}
}

// inEnvironment builds the project once and hands it to every attempt.
func inEnvironment(
	operation func(Environment, *recordingRunner) error,
) func(*testing.T) func(*recordingRunner) error {
	return func(t *testing.T) func(*recordingRunner) error {
		environment := anEnvironment(t)

		return func(runner *recordingRunner) error { return operation(environment, runner) }
	}
}

// scriptCleanOn is cleanOn's answers applied to an existing runner, so a case
// can add to a runner the property test owns.
func scriptCleanOn(runner *recordingRunner, branch string) *recordingRunner {
	return runner.
		answer("status --porcelain", "").
		answer("symbolic-ref --short HEAD", branch+"\n").
		answer("rev-list --count @{upstream}..HEAD", "0\n")
}

package command

import (
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/workflow"
)

// publish refuses when nobody is there to confirm, and does nothing first.
//
// The refusal this exists for. A script, a pipe or an agent can reach publish,
// and what it does is open a merge request on drupal.org under the
// maintainer's name — the mistake is not a broken build but a wrong thing said
// in public.
func TestPublishRefusesWithNobodyThereToConfirm(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	engine := aWorkingEngine()

	code, _, stderr := runIssueLoop(t, engine, someIssues(),
		scriptedClients{client: scene.client()}, cli.NoBrowser{},
		"publish", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if len(engine.pushed) != 0 {
		t.Errorf("it pushed %v", engine.pushed)
	}
	if len(engine.ensured) != 0 {
		t.Errorf("it provisioned %v before refusing", engine.ensured)
	}
	// Nothing was even asked of GitLab: the gate is before the credential, so
	// "nobody is here to confirm" does not arrive dressed as a token problem.
	if len(scene.requests) != 0 {
		t.Errorf("it made %d request(s) before refusing", len(scene.requests))
	}

	for _, want := range []string{"under your name", "--yes"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal is missing %q: %q", want, stderr)
		}
	}
}

// --yes is the way through, because deliberate scripted publishing is a
// legitimate thing to want.
func TestYesGetsPublishThroughTheGate(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	engine := aWorkingEngine()

	code, _, stderr := runIssueLoop(t, engine, someIssues(),
		scriptedClients{client: scene.client()}, cli.NoBrowser{},
		"publish", "--yes", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(engine.pushed) != 1 {
		t.Errorf("pushed %v", engine.pushed)
	}
}

// needs-work refuses too, and posts nothing.
func TestNeedsWorkRefusesWithNobodyThereToConfirm(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)

	code, _, stderr := runNeedsWorkCommand(t, scene, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d, want %d", code, workflow.Infrastructure)
	}
	for _, want := range []string{"somebody else will read", "--yes"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal is missing %q: %q", want, stderr)
		}
	}
}

// --dry-run is exempt, because it posts nothing.
//
// Previewing the comment is a read, and an agent preparing one for a human to
// send is exactly the use worth leaving open. Gating it would have made the
// safe path as awkward as the unsafe one.
func TestDryRunNeedsNoConfirmation(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)

	code, _, stderr := runNeedsWorkCommand(t, scene, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--dry-run", "--cockpit="+root)

	if strings.Contains(stderr, "--yes") {
		t.Errorf("a dry run was gated: %q", stderr)
	}
	// It may still fail for its own reasons; what matters is that the gate
	// was not the reason.
	_ = code
}

// merge keeps its own refusal and gains no --yes.
//
// The two are not the same stance. A merge consumes an approval only a human
// can give, which is Drupal Association policy; a publish is an action a
// human can authorise in advance. Giving merge a --yes would weaken the one
// the policy depends on.
func TestMergeHasNoWayPastItsOwnRefusal(t *testing.T) {
	for _, cmd := range documentedCommands(t) {
		if cmd.Name() != "merge" {
			continue
		}
		if cmd.Flags().Lookup("yes") != nil {
			t.Error("merge gained a --yes flag")
		}

		return
	}

	t.Fatal("merge is not in the command tree")
}

// Only the outward-facing commands are gated.
//
// A gate on a command that writes nothing but local state would be imposing a
// restriction for the sake of symmetry, and would make an agent's safe work
// needlessly awkward.
func TestOnlyOutwardCommandsAreGated(t *testing.T) {
	expected := map[string]bool{"publish": true, "needs-work": true}

	for _, cmd := range documentedCommands(t) {
		gated := cmd.Flags().Lookup("yes") != nil
		// prune has had --yes since long before this, for deleting rather
		// than for writing outward.
		if cmd.Name() == "prune" {
			continue
		}
		if gated != expected[cmd.Name()] {
			t.Errorf("%s: gated=%t, want %t", cmd.Name(), gated, expected[cmd.Name()])
		}
	}
}

// The gate is asked before the credential is.
//
// Same reasoning as the node id being checked first: "there is nobody here to
// confirm" is a more basic refusal than "you have no token", it costs no round
// trip, and a caller told the wrong one goes and configures a credential they
// did not need.
func TestTheGateIsAskedBeforeTheCredential(t *testing.T) {
	root := anEnvironmentCockpit(t)

	code, _, stderr := runIssueLoop(t, aWorkingEngine(), someIssues(),
		noClients{}, cli.NoBrowser{},
		"publish", "pathauto", "3223746", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("the gate did not speak first: %q", stderr)
	}
	if strings.Contains(stderr, "No GitLab token") {
		t.Errorf("it reported a missing credential instead of the gate: %q", stderr)
	}
}

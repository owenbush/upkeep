package command

import (
	"os"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/workflow"
)

// The entry goes, and the surveys stop covering it.
func TestModulesUntrackRemovesTheEntry(t *testing.T) {
	root := aTrackingCockpit(t)

	code, stdout, stderr := invoke(t, "modules:untrack", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if _, still := registryOf(t, root)["pathauto"]; still {
		t.Error("the entry is still in the registry")
	}
	if stdout != "" {
		t.Errorf("stdout was not empty: %q", stdout)
	}
}

// Every other entry is left exactly as it was: this removes one line of a file
// somebody reads, not everything around it.
func TestModulesUntrackLeavesEveryOtherEntryAlone(t *testing.T) {
	root := aTrackingCockpit(t)

	if code, _, stderr := invoke(t,
		"modules:untrack", "pathauto", "--cockpit="+root); code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	modules := registryOf(t, root)
	kept, found := modules["jumplinks"]
	if !found {
		t.Fatalf("it removed more than it was asked to: %v", modules)
	}
	if kept.Project != "project/jumplinks" || strings.Join(kept.CoreVersions, ",") != "11" {
		t.Errorf("jumplinks changed: %+v", kept)
	}
}

// The report says what stopped and what did not, because "no longer watched"
// and "uninstalled" are very different things and only one of them happened.
func TestModulesUntrackSaysWhatItDidAndDidNotDo(t *testing.T) {
	root := aTrackingCockpit(t)

	_, _, stderr := invoke(t, "modules:untrack", "pathauto", "--cockpit="+root)

	for _, want := range []string{
		"no longer watched", // what changed
		"dashboard",         // which commands stop covering it
		"Nothing on disk",   // and what did not change
		"upkeep prune",      // the command for the part this one does not do
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("%q is missing from the report: %q", want, stderr)
		}
	}
}

// The cores it removed come back in the report, in the shape of the command
// that restores them.
//
// They are a judgement somebody made and the registry was the only place they
// lived — GitLab holds the project path, so `modules:add` gets that from the
// authority, but nothing anywhere else knows which cores were being tracked.
func TestModulesUntrackReportsTheCoresItRemoved(t *testing.T) {
	root := aTrackingCockpit(t)

	_, _, stderr := invoke(t, "modules:untrack", "pathauto", "--cockpit="+root)

	if !strings.Contains(stderr, "upkeep modules:add pathauto --core-versions=10,11") {
		t.Errorf("the report did not carry the cores it removed: %q", stderr)
	}
}

// A module with no entry is refused: there is nothing to remove, and reporting
// one as unwatched would be reporting an edit that never happened.
func TestModulesUntrackRefusesAModuleWithNoEntry(t *testing.T) {
	root := aTrackingCockpit(t)

	code, _, stderr := invoke(t, "modules:untrack", "token", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	for _, want := range []string{"token", "jumplinks", "pathauto"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("%q is missing from the refusal: %q", want, stderr)
		}
	}
	if strings.Contains(stderr, "no longer watched") {
		t.Errorf("it reported an edit it did not make: %q", stderr)
	}
}

// Unwatching the last module leaves the cockpit in its first-run state rather
// than a broken one: `modules: {}` is what init scaffolds and what the loader
// accepts.
func TestModulesUntrackMayEmptyTheRegistry(t *testing.T) {
	root := aTrackingCockpit(t)

	for _, module := range []string{"jumplinks", "pathauto"} {
		if code, _, stderr := invoke(t,
			"modules:untrack", module, "--cockpit="+root); code != workflow.OK {
			t.Fatalf("%s: exit %d (%s)", module, code, stderr)
		}
	}

	if modules := registryOf(t, root); len(modules) != 0 {
		t.Errorf("the registry still holds %v", modules)
	}

	// And the cockpit still works: `modules` reads it and says what to do.
	code, stdout, stderr := invoke(t, "modules", "--cockpit="+root)
	if code != workflow.OK {
		t.Fatalf("the emptied registry broke `modules`: exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "upkeep modules:add") {
		t.Errorf("got %q", stdout)
	}
}

// A registry that cannot be written is a failure, not a module reported as
// unwatched over a file that never changed.
func TestModulesUntrackReportsARegistryItCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into every directory regardless of its mode")
	}

	root := aTrackingCockpit(t)
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	code, _, stderr := invoke(t, "modules:untrack", "pathauto", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if strings.Contains(stderr, "no longer watched") {
		t.Errorf("it reported an edit that did not land: %q", stderr)
	}
}

// modules:track refuses to empty a module's core list, and the command it
// names for doing that deliberately is one that runs.
//
// Scraped and executed rather than compared, for the same reason as the
// untracked-core refusal: a renamed command fails here and a string match
// would not.
func TestTheCommandAnEmptiedCoreListNamesIsOneThatWorks(t *testing.T) {
	root := aTrackingCockpit(t)

	code, _, stderr := invoke(t,
		"modules:track", "pathauto", "--remove=10,11", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	suggested := suggestedUpkeepCommand(t, stderr)

	if code, _, stderr := invoke(t, append(suggested, "--cockpit="+root)...); code != workflow.OK {
		t.Fatalf("the suggested command failed: upkeep %s\n%s", strings.Join(suggested, " "), stderr)
	}
	if _, still := registryOf(t, root)["pathauto"]; still {
		t.Error("the suggested command did not stop watching the module")
	}
}

// And the restore it offers is a command that exists, with the flag it claims.
//
// Not executed here — modules:add reads GitLab for your memberships, which is
// a round trip this suite has no business making — so what is checked is that
// the flag is one the command actually has.
func TestTheRestoreUntrackOffersNamesARealFlag(t *testing.T) {
	root := aTrackingCockpit(t)

	_, _, stderr := invoke(t, "modules:untrack", "pathauto", "--cockpit="+root)

	suggested := suggestedUpkeepCommand(t, stderr)
	if len(suggested) == 0 || suggested[0] != "modules:add" {
		t.Fatalf("the restore did not name modules:add: %v", stderr)
	}

	add := NewModulesAdd(noClients{}, noPrompts)
	for _, argument := range suggested[1:] {
		flag, _, isFlag := strings.Cut(argument, "=")
		if !isFlag {
			continue
		}
		if add.Flags().Lookup(strings.TrimPrefix(flag, "--")) == nil {
			t.Errorf("modules:add has no %s flag, but the restore passes it: %v", flag, suggested)
		}
	}
}

// Nothing here invents a registry entry for a module the cockpit does not
// carry, which is what separates these two from every subject command.
func TestTheRegistryCommandsNeverInventAnEntry(t *testing.T) {
	root := aTrackingCockpit(t)

	for name, args := range map[string][]string{
		"modules:track":   {"modules:track", "token", "12", "--cockpit=" + root},
		"modules:untrack": {"modules:untrack", "token", "--cockpit=" + root},
	} {
		if code, _, _ := invoke(t, args...); code != workflow.Infrastructure {
			t.Errorf("%s: exit %d for a module with no entry", name, code)
		}
		if _, invented := registryOf(t, root)["token"]; invented {
			t.Fatalf("%s wrote an entry for a module nobody registered", name)
		}
	}

	// And a derived module is exactly what they refuse: `ResolveModule` would
	// hand back a usable Module for this name, project path and all.
	if _, err := cockpit.ResolveModule(
		registryOf(t, root), "token", []string{"11"},
	); err != nil {
		t.Fatalf("the premise does not hold — ResolveModule refused too: %v", err)
	}
}

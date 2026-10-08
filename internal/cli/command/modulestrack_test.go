package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/workflow"
)

// aTrackingCockpit is two registered modules, one tracking a single core and
// one tracking two, so the order of the list is observable.
func aTrackingCockpit(t *testing.T) string {
	t.Helper()

	root := aDashboardCockpit(t)
	where, err := cockpit.New(root)
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}
	if err := os.WriteFile(where.RegistryPath(), []byte(
		"modules:\n"+
			"  jumplinks:\n    project: project/jumplinks\n    core_versions: [\"11\"]\n"+
			"  pathauto:\n    project: project/pathauto\n    core_versions: [\"10\", \"11\"]\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return root
}

// coresOf is what a module tracks, in file order.
func coresOf(t *testing.T, root, module string) string {
	t.Helper()

	return strings.Join(registryOf(t, root)[module].CoreVersions, ",")
}

// hasBuilt puts a base artifact set for a core on disk, which is what decides
// whether a newly tracked core draws the build warning.
func hasBuilt(t *testing.T, root, core string) {
	t.Helper()

	where, err := cockpit.New(root)
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(where.BaseArtifactsPath(), core), 0o755); err != nil {
		t.Fatalf("base artifacts: %v", err)
	}
}

// A core named as an argument is added, and the default does not move.
//
// The whole point of appending rather than inserting or sorting: core_versions[0]
// is what a command targets when --version is omitted, so a maintainer adding
// next year's core must not find every bare `upkeep check` silently retargeted
// at it.
func TestModulesTrackAddsACoreWithoutMovingTheDefault(t *testing.T) {
	root := aTrackingCockpit(t)

	code, stdout, stderr := invoke(t, "modules:track", "jumplinks", "12", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if got := coresOf(t, root, "jumplinks"); got != "11,12" {
		t.Errorf("tracks %q, want \"11,12\"", got)
	}
	if !strings.Contains(stderr, "now tracks core 11, 12 (default 11)") {
		t.Errorf("the report did not name the list and its default: %q", stderr)
	}
	// stdout belongs to `upkeep modules`; this command's answer is the edit.
	if stdout != "" {
		t.Errorf("stdout was not empty: %q", stdout)
	}
}

// Adding a core already tracked changes nothing and says so.
//
// It must not rewrite the file either: a write regenerates the YAML and
// consumes any comment in it, so a no-op that rewrites costs a maintainer
// their annotations for nothing.
func TestModulesTrackIsIdempotent(t *testing.T) {
	root := aTrackingCockpit(t)
	where, _ := cockpit.New(root)

	if code, _, stderr := invoke(t,
		"modules:track", "jumplinks", "12", "--cockpit="+root); code != workflow.OK {
		t.Fatalf("first: exit %d (%s)", code, stderr)
	}
	before, err := os.ReadFile(where.RegistryPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	code, _, stderr := invoke(t, "modules:track", "jumplinks", "12", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if strings.Contains(stderr, "now tracks") {
		t.Errorf("a second add reported a change: %q", stderr)
	}
	after, err := os.ReadFile(where.RegistryPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("the registry was rewritten for no change:\n%s", after)
	}
}

// Naming no core at all reports what the module tracks and writes nothing.
func TestModulesTrackWithNoCoresOnlyReports(t *testing.T) {
	root := aTrackingCockpit(t)

	code, _, stderr := invoke(t, "modules:track", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "pathauto tracks core 10, 11 (default 10)") {
		t.Errorf("got %q", stderr)
	}
	if got := coresOf(t, root, "pathauto"); got != "10,11" {
		t.Errorf("tracks %q, want \"10,11\"", got)
	}
}

// --remove drops a core.
func TestModulesTrackRemovesACore(t *testing.T) {
	root := aTrackingCockpit(t)

	code, _, stderr := invoke(t, "modules:track", "pathauto", "--remove=10", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if got := coresOf(t, root, "pathauto"); got != "11" {
		t.Errorf("tracks %q, want \"11\"", got)
	}
}

// Removing a core the module does not track is not an error — the list is
// already what was asked for, which is the same answer as doing it twice.
func TestModulesTrackRemovingAnUntrackedCoreChangesNothing(t *testing.T) {
	root := aTrackingCockpit(t)

	code, _, stderr := invoke(t, "modules:track", "jumplinks", "--remove=10", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if got := coresOf(t, root, "jumplinks"); got != "11" {
		t.Errorf("tracks %q, want \"11\"", got)
	}
}

// --set replaces the list in the order given, which is how the default moves.
func TestModulesTrackSetReplacesTheListInOrder(t *testing.T) {
	root := aTrackingCockpit(t)

	code, _, stderr := invoke(t, "modules:track", "pathauto", "--set=12,11", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if got := coresOf(t, root, "pathauto"); got != "12,11" {
		t.Errorf("tracks %q, want \"12,11\" — the order given, not sorted", got)
	}
	if !strings.Contains(stderr, "default 12") {
		t.Errorf("the moved default was not reported: %q", stderr)
	}
}

// --set keeps a repeated core once, at its first position.
func TestModulesTrackSetKeepsEachCoreOnce(t *testing.T) {
	root := aTrackingCockpit(t)

	code, _, stderr := invoke(t, "modules:track", "pathauto", "--set=12,11,12", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if got := coresOf(t, root, "pathauto"); got != "12,11" {
		t.Errorf("tracks %q, want \"12,11\"", got)
	}
}

// --set with anything else is refused, because the two say different things
// about the same list and there is no reading of the pair that is obviously
// right. The registry is untouched.
func TestModulesTrackRefusesSetCombinedWithAnythingElse(t *testing.T) {
	for _, args := range [][]string{
		{"modules:track", "pathauto", "12", "--set=11"},
		{"modules:track", "pathauto", "--remove=10", "--set=11"},
	} {
		root := aTrackingCockpit(t)
		code, _, stderr := invoke(t, append(args, "--cockpit="+root)...)

		if code != workflow.Infrastructure {
			t.Errorf("%v: exit %d, want %d", args, code, workflow.Infrastructure)
		}
		if !strings.Contains(stderr, "--set replaces the whole list") {
			t.Errorf("%v: got %q", args, stderr)
		}
		if got := coresOf(t, root, "pathauto"); got != "10,11" {
			t.Errorf("%v: the registry changed to %q", args, got)
		}
	}
}

// A core that is not a whole major version is refused, naming the argument
// rather than letting the registry loader report the file.
func TestModulesTrackRefusesSomethingThatIsNotACoreMajor(t *testing.T) {
	for _, args := range [][]string{
		{"modules:track", "jumplinks", "twelve"},
		{"modules:track", "jumplinks", "11.2"},
		{"modules:track", "jumplinks", "--remove=../11"},
		{"modules:track", "jumplinks", "--set=^12"},
	} {
		root := aTrackingCockpit(t)
		code, _, stderr := invoke(t, append(args, "--cockpit="+root)...)

		if code != workflow.Infrastructure {
			t.Errorf("%v: exit %d, want %d", args, code, workflow.Infrastructure)
		}
		if !strings.Contains(stderr, "is not a Drupal core major version") {
			t.Errorf("%v: got %q", args, stderr)
		}
		if got := coresOf(t, root, "jumplinks"); got != "11" {
			t.Errorf("%v: the registry changed to %q", args, got)
		}
	}
}

// Removing every core is refused rather than written: the loader rejects an
// empty core_versions, so writing it would leave a registry no command can
// read — every module in the file unusable, not just this one.
func TestModulesTrackRefusesToLeaveAModuleTrackingNothing(t *testing.T) {
	root := aTrackingCockpit(t)

	code, _, stderr := invoke(t,
		"modules:track", "pathauto", "--remove=10,11", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if !strings.Contains(stderr, "tracking no core at all") {
		t.Errorf("got %q", stderr)
	}
	if got := coresOf(t, root, "pathauto"); got != "10,11" {
		t.Errorf("the registry changed to %q", got)
	}
}

// An unregistered module is refused and told what is registered.
//
// The strict lookup, not the resolver every subject command uses: a module
// derived from the base artifacts on this machine has no entry to edit, and
// inventing one here would write a project path nobody chose.
func TestModulesTrackRefusesAModuleWithNoEntry(t *testing.T) {
	root := aTrackingCockpit(t)
	hasBuilt(t, root, "11")

	code, _, stderr := invoke(t, "modules:track", "token", "12", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	for _, want := range []string{"token", "jumplinks", "pathauto"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("%q is missing from the refusal: %q", want, stderr)
		}
	}
}

// Every other entry keeps its definition and its position in the file, because
// an edit rebuilds the whole document and a registry is a file people read.
func TestModulesTrackLeavesEveryOtherEntryWhereItWas(t *testing.T) {
	root := aTrackingCockpit(t)
	where, _ := cockpit.New(root)

	if code, _, stderr := invoke(t,
		"modules:track", "jumplinks", "12", "--cockpit="+root); code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	written, err := os.ReadFile(where.RegistryPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Index(string(written), "jumplinks") > strings.Index(string(written), "pathauto") {
		t.Errorf("the entries were reordered:\n%s", written)
	}

	modules := registryOf(t, root)
	if got := coresOf(t, root, "pathauto"); got != "10,11" {
		t.Errorf("pathauto's cores changed to %q", got)
	}
	if modules["pathauto"].Project != "project/pathauto" {
		t.Errorf("pathauto's project changed to %q", modules["pathauto"].Project)
	}
}

// Tracking a core with no base artifacts warns and names the build, because
// the check it was tracked for is the thing that needs them — without this the
// command succeeds and the very next run refuses.
func TestModulesTrackWarnsAboutACoreWithNoBaseArtifacts(t *testing.T) {
	root := aTrackingCockpit(t)
	hasBuilt(t, root, "11")

	code, _, stderr := invoke(t, "modules:track", "jumplinks", "12", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "no base artifacts for core 12") {
		t.Errorf("no warning: %q", stderr)
	}
	if !strings.Contains(stderr, "base-artifacts:build --version=12") {
		t.Errorf("the warning did not name the build: %q", stderr)
	}
	// And the edit still happened: this is advice, not a refusal.
	if got := coresOf(t, root, "jumplinks"); got != "11,12" {
		t.Errorf("tracks %q", got)
	}
}

// A core that is built draws no warning.
func TestModulesTrackIsQuietWhenTheCoreIsBuilt(t *testing.T) {
	root := aTrackingCockpit(t)
	hasBuilt(t, root, "11")
	hasBuilt(t, root, "12")

	code, _, stderr := invoke(t, "modules:track", "jumplinks", "12", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if strings.Contains(stderr, "no base artifacts") {
		t.Errorf("warned about a built core: %q", stderr)
	}
}

// Nothing built at all is silence rather than a warning per core: an empty
// answer from CoresOnDisk means both "none built" and "could not look", and a
// cockpit that has never built one would otherwise warn about every core it
// already tracks.
func TestModulesTrackSaysNothingAboutArtifactsWhenNoneAreBuilt(t *testing.T) {
	root := aTrackingCockpit(t)

	code, _, stderr := invoke(t, "modules:track", "jumplinks", "12", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if strings.Contains(stderr, "no base artifacts") {
		t.Errorf("got %q", stderr)
	}
}

// A core already tracked is never warned about, however the list changes:
// the warning is about new ground, and repeating it for what was already there
// would fire on every --set.
func TestModulesTrackOnlyWarnsAboutNewlyTrackedCores(t *testing.T) {
	root := aTrackingCockpit(t)
	hasBuilt(t, root, "10")

	code, _, stderr := invoke(t, "modules:track", "pathauto", "--set=11,10", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// Core 11 has no artifacts here and is reordered to the front, so a
	// warning keyed on the list rather than on what is new to it would fire.
	if strings.Contains(stderr, "no base artifacts") {
		t.Errorf("warned about a core it already tracked: %q", stderr)
	}
}

// A registry that cannot be written is a failure, not a success over a file
// that never changed.
//
// The Editor validates and renames rather than writing in place, so the write
// needs the *directory*, and this is the one path where the edit is computed,
// accepted and then cannot land.
func TestModulesTrackReportsARegistryItCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into every directory regardless of its mode")
	}

	root := aTrackingCockpit(t)
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	code, stdout, stderr := invoke(t, "modules:track", "jumplinks", "12", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Fatalf("exit %d, want %d", code, workflow.Infrastructure)
	}
	if stdout != "" {
		t.Errorf("stdout: %q", stdout)
	}
	if strings.Contains(stderr, "now tracks") {
		t.Errorf("it reported an edit that did not land: %q", stderr)
	}
	if stderr == "" {
		t.Error("it failed silently")
	}
}

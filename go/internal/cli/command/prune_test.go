package command

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/maintenance"
	"github.com/owenbush/upkeep/internal/workflow"
)

// tearingEngine records what it was asked to dispose of, and can refuse.
type tearingEngine struct {
	fakeEngine

	tornDown []string
	refuse   error
}

func (e *tearingEngine) Teardown(module cockpit.Module, coreMajor string) error {
	if e.refuse != nil {
		return e.refuse
	}
	e.tornDown = append(e.tornDown, module.Name+"/"+coreMajor)

	return nil
}

// aPrunableCockpit lays out two environments, a snapshot, an artifact set and
// a library dump, under a home directory the containment rule accepts.
func aPrunableCockpit(t *testing.T) string {
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

	for name, age := range map[string]time.Duration{
		"upkeep-pathauto-d11": 40 * 24 * time.Hour,
		"upkeep-pathauto-d10": 2 * time.Hour,
	} {
		project := filepath.Join(where.ProjectsPath(), name)
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		core := "11"
		if strings.HasSuffix(name, "d10") {
			core = "10"
		}
		meta := adapter.EnvironmentMeta{
			ModuleName: "pathauto", CoreMajor: core, SeedCoreVersion: core + ".4.6",
			AddOnVersion: adapter.EngineAddOnVersion,
			CreatedAt:    time.Now().Add(-age), LastUsedAt: time.Now().Add(-age),
		}
		if err := meta.WriteTo(project); err != nil {
			t.Fatalf("meta: %v", err)
		}
	}

	// A materialised snapshot, which its committed dump can rebuild.
	snapshot := filepath.Join(
		where.ProjectsPath(), "upkeep-pathauto-d11", adapter.SnapshotMaterializedDir, "baseline.sql")
	if err := os.MkdirAll(filepath.Dir(snapshot), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(snapshot, []byte("dump"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(where.BaseArtifactsPath(), "11"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(where.FixturesPath(), "baseline.sql.gz"), []byte("dump"), 0o644,
	); err != nil {
		t.Fatalf("write: %v", err)
	}

	return root
}

// runPruneCommand invokes the tree with a scripted engine and volume listing.
func runPruneCommand(
	t *testing.T, engine *tearingEngine, volumes Volumes, args ...string,
) (int, string, string) {
	t.Helper()

	root := NewRoot(
		&fakeFactory{engine: &fakeEngine{}, replace: engine},
		noClients{}, noIssues{}, noPrompts, volumes, func(string) int64 { return 1024 },
	)

	return invokeWith(t, root, args...)
}

// Exactly one scope, and no default: two at once would be ambiguous about what
// the plan covers, and a default would make the destructive command the one
// somebody runs by accident.
func TestPruneNeedsExactlyOneScope(t *testing.T) {
	root := aPrunableCockpit(t)
	engine := &tearingEngine{}

	for name, args := range map[string][]string{
		"none":     {"prune", "--cockpit=" + root},
		"two":      {"prune", "--trees", "--all", "--cockpit=" + root},
		"all four": {"prune", "--trees", "--snapshots", "--projects", "--all", "--cockpit=" + root},
	} {
		code, stdout, stderr := runPruneCommand(t, engine, someVolumes(nil), args...)

		if code != workflow.Infrastructure {
			t.Errorf("%s: exit %d", name, code)
		}
		if !strings.Contains(stderr, "exactly one prune scope") {
			t.Errorf("%s: stderr %q", name, stderr)
		}
		if stdout != "" {
			t.Errorf("%s: it showed a plan anyway: %q", name, stdout)
		}
	}
	if len(engine.tornDown) != 0 {
		t.Errorf("it tore something down: %v", engine.tornDown)
	}
}

// Dry-run by default. Without --yes it lists what it would delete and deletes
// nothing — this is the only destructive command, and the plan is worth
// reading before it is carried out.
func TestPruneDeletesNothingWithoutYes(t *testing.T) {
	root := aPrunableCockpit(t)
	engine := &tearingEngine{}

	code, stdout, _ := runPruneCommand(t, engine, someVolumes(nil), "prune", "--all", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	if len(engine.tornDown) != 0 {
		t.Errorf("a dry run tore down %v", engine.tornDown)
	}
	if !strings.Contains(stdout, "Dry run: nothing was deleted") {
		t.Errorf("it did not say it was a dry run:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--yes") {
		t.Errorf("it did not say how to actually reclaim:\n%s", stdout)
	}
	// The plan says what and how much.
	if !strings.Contains(stdout, "Total reclaimable:") {
		t.Errorf("no total:\n%s", stdout)
	}

	// And the environments are still on disk.
	where, _ := cockpit.New(root)
	if _, err := os.Stat(filepath.Join(where.ProjectsPath(), "upkeep-pathauto-d11")); err != nil {
		t.Errorf("a dry run removed a tree: %v", err)
	}
}

// With --yes, environments go through the adapter's teardown — never a tree
// removal, which would leave containers running.
func TestPruneWithYesDisposesThroughTheAdapter(t *testing.T) {
	root := aPrunableCockpit(t)
	engine := &tearingEngine{}

	code, stdout, _ := runPruneCommand(t, engine, someVolumes(nil),
		"prune", "--trees", "--yes", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	if len(engine.tornDown) != 2 {
		t.Errorf("torn down %v", engine.tornDown)
	}
	if !strings.Contains(stdout, "Pruned 2 item(s)") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// Base artifacts and committed dumps are protected regardless of any flag
// combination: they are canonical and expensive to rebuild.
func TestPruneNeverOffersTheCanonicalThings(t *testing.T) {
	root := aPrunableCockpit(t)

	_, stdout, _ := runPruneCommand(t, &tearingEngine{}, someVolumes(nil),
		"prune", "--all", "--cockpit="+root)

	for _, protected := range []string{"base-artifacts", "fixtures/baseline.sql.gz"} {
		if strings.Contains(stdout, protected) {
			t.Errorf("%s was offered for deletion:\n%s", protected, stdout)
		}
	}
}

// A keep marker takes a whole environment out of every scope.
func TestAKeepMarkedEnvironmentIsNeverOffered(t *testing.T) {
	root := aPrunableCockpit(t)
	where, _ := cockpit.New(root)
	kept := filepath.Join(where.ProjectsPath(), "upkeep-pathauto-d11", maintenance.KeepMarker)
	if err := os.WriteFile(kept, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	engine := &tearingEngine{}
	_, stdout, _ := runPruneCommand(t, engine, someVolumes(nil),
		"prune", "--all", "--yes", "--cockpit="+root)

	if strings.Contains(stdout, "upkeep-pathauto-d11 ") {
		t.Errorf("a keep-marked environment was pruned:\n%s", stdout)
	}
	for _, torn := range engine.tornDown {
		if torn == "pathauto/11" {
			t.Error("a keep-marked environment was torn down")
		}
	}
}

// --older-than admits only what has sat unused that long, and an item of
// unknown age is then excluded: deleting on the strength of a question that
// could not be asked is the one mistake a prune must not make.
func TestOlderThanExcludesTheRecentAndTheUnknown(t *testing.T) {
	root := aPrunableCockpit(t)
	where, _ := cockpit.New(root)
	// A partial provision: no meta, so no age.
	if err := os.MkdirAll(filepath.Join(where.ProjectsPath(), "upkeep-token-d11"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, stdout, _ := runPruneCommand(t, &tearingEngine{}, someVolumes(nil),
		"prune", "--trees", "--older-than=30d", "--cockpit="+root)

	if !strings.Contains(stdout, "upkeep-pathauto-d11") {
		t.Errorf("the 40-day-old environment was not offered:\n%s", stdout)
	}
	if strings.Contains(stdout, "upkeep-pathauto-d10") {
		t.Errorf("a two-hour-old environment was offered:\n%s", stdout)
	}
	if strings.Contains(stdout, "upkeep-token-d11") {
		t.Errorf("an environment of unknown age was offered:\n%s", stdout)
	}
}

// A duration nobody can parse is refused before anything is scanned.
func TestAnUnparseableAgeFilterIsRefusedBeforeScanning(t *testing.T) {
	root := aPrunableCockpit(t)

	code, stdout, stderr := runPruneCommand(t, &tearingEngine{}, someVolumes(nil),
		"prune", "--all", "--older-than=soon", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if stdout != "" {
		t.Errorf("it scanned anyway: %q", stdout)
	}
	if !strings.Contains(stderr, "30d") {
		t.Errorf("the refusal does not show the shape: %q", stderr)
	}
}

// Volumes come into the plan only for the scopes that cover them: a snapshot
// prune has no business listing an engine volume.
func TestVolumesAreOnlyProbedForTheScopesThatCoverThem(t *testing.T) {
	root := aPrunableCockpit(t)
	volumes := someVolumes{{
		Name: "upkeep-pathauto-d11-mariadb", ProjectName: "upkeep-pathauto-d11", SizeBytes: 700_000_000,
	}}

	_, withVolumes, _ := runPruneCommand(t, &tearingEngine{}, volumes,
		"prune", "--projects", "--cockpit="+root)
	if !strings.Contains(withVolumes, "upkeep-pathauto-d11-mariadb") {
		t.Errorf("--projects did not list the volume:\n%s", withVolumes)
	}

	_, withoutVolumes, _ := runPruneCommand(t, &tearingEngine{}, volumes,
		"prune", "--snapshots", "--cockpit="+root)
	if strings.Contains(withoutVolumes, "mariadb") {
		t.Errorf("--snapshots listed a volume:\n%s", withoutVolumes)
	}
}

// And the runtime is not asked at all for a scope that cannot use the answer:
// listing every volume on the machine is slow, and a snapshot prune has no
// business waiting for it.
func TestTheContainerRuntimeIsNotAskedForScopesThatIgnoreVolumes(t *testing.T) {
	root := aPrunableCockpit(t)

	for scope, expected := range map[string]int{
		"--snapshots": 0,
		"--trees":     0,
		"--projects":  1,
		"--all":       1,
	} {
		probe := &countingVolumes{}
		runPruneCommand(t, &tearingEngine{}, probe, "prune", scope, "--cockpit="+root)

		if probe.calls != expected {
			t.Errorf("%s probed the runtime %d times, want %d", scope, probe.calls, expected)
		}
	}
}

// countingVolumes records how often the runtime was asked.
type countingVolumes struct{ calls int }

func (v *countingVolumes) Volumes() []adapter.ProjectVolume {
	v.calls++

	return nil
}

// Nothing matching is an answer, not an empty table.
func TestNothingToPruneSaysSo(t *testing.T) {
	root := aPrunableCockpit(t)

	code, stdout, _ := runPruneCommand(t, &tearingEngine{}, someVolumes(nil),
		"prune", "--trees", "--older-than=520w", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stdout, "Nothing to prune") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if strings.Contains(stdout, "Reclaimable") {
		t.Errorf("it printed an empty table:\n%s", stdout)
	}
}

// A teardown that refuses is a skip with its own reason, and the run says so
// rather than reporting bytes it did not free.
func TestARefusedTeardownIsReportedAndNotCounted(t *testing.T) {
	root := aPrunableCockpit(t)
	engine := &tearingEngine{refuse: errors.New("the working copy has local work")}

	code, stdout, stderr := runPruneCommand(t, engine, someVolumes(nil),
		"prune", "--trees", "--yes", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "local work") {
		t.Errorf("the adapter's own words were dropped: %q", stderr)
	}
	if !strings.Contains(stdout, "Pruned 0 item(s), reclaimed 0 B") {
		t.Errorf("it counted bytes it did not free:\n%s", stdout)
	}
}

// A registry that does not parse is reported before any plan is shown:
// discovering it halfway through would abort a run the operator has already
// been shown a plan for.
func TestPruneReportsAMalformedRegistryBeforeShowingAPlan(t *testing.T) {
	root := aPrunableCockpit(t)
	where, _ := cockpit.New(root)
	if err := os.WriteFile(where.RegistryPath(), []byte("\tnot: [yaml"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, stdout, stderr := runPruneCommand(t, &tearingEngine{}, someVolumes(nil),
		"prune", "--all", "--yes", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if stdout != "" {
		t.Errorf("it showed a plan over a registry it could not read: %q", stdout)
	}
	if stderr == "" {
		t.Error("it failed silently")
	}
}

// A directory that could not be read is warned about rather than folded into
// the plan as if it were empty: under-reporting an inventory can only
// under-delete, but the operator has to know the plan is incomplete.
func TestAnUnreadableDirectoryIsWarnedAboutAndTheRunContinues(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory regardless of its mode")
	}

	root := aPrunableCockpit(t)
	where, _ := cockpit.New(root)
	blocked := filepath.Join(where.ProjectsPath(), "upkeep-pathauto-d11", adapter.SnapshotMaterializedDir)
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	code, stdout, stderr := runPruneCommand(t, &tearingEngine{}, someVolumes(nil),
		"prune", "--all", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("a warning became a failure: exit %d", code)
	}
	if !strings.Contains(stderr, "not readable") {
		t.Errorf("it was folded in as empty: %q", stderr)
	}
	// And the rest of the plan is still shown.
	if !strings.Contains(stdout, "upkeep-pathauto-d10") {
		t.Errorf("the rest of the disk went with it:\n%s", stdout)
	}
}

// --keep-latest retains the newest snapshots per project regardless of age.
func TestKeepLatestRetainsTheNewestSnapshots(t *testing.T) {
	root := aPrunableCockpit(t)
	where, _ := cockpit.New(root)
	materialized := filepath.Join(
		where.ProjectsPath(), "upkeep-pathauto-d11", adapter.SnapshotMaterializedDir)

	// Every snapshot's age set explicitly, including the one the cockpit
	// already had: "the newest" is only meaningful over a known order, and a
	// file written a moment ago is newer than anything a test can schedule.
	ages := map[string]time.Duration{
		"baseline.sql": 30 * 24 * time.Hour,
		"older.sql":    20 * 24 * time.Hour,
		"newer.sql":    10 * 24 * time.Hour,
	}
	for name, age := range ages {
		path := filepath.Join(materialized, name)
		if err := os.WriteFile(path, []byte("dump"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	_, stdout, _ := runPruneCommand(t, &tearingEngine{}, someVolumes(nil),
		"prune", "--snapshots", "--keep-latest=1", "--cockpit="+root)

	if strings.Contains(stdout, "newer.sql") {
		t.Errorf("the newest snapshot was offered:\n%s", stdout)
	}
	for _, offered := range []string{"older.sql", "baseline.sql"} {
		if !strings.Contains(stdout, offered) {
			t.Errorf("%s was not offered:\n%s", offered, stdout)
		}
	}

	// And without the flag, every one of them is.
	_, all, _ := runPruneCommand(t, &tearingEngine{}, someVolumes(nil),
		"prune", "--snapshots", "--cockpit="+root)
	for name := range ages {
		if !strings.Contains(all, name) {
			t.Errorf("%s was retained with no --keep-latest:\n%s", name, all)
		}
	}
}

// Nothing under a protected root is ever a candidate, whatever category it
// arrives as.
//
// The category rules cover the ordinary case — a base artifact is protected
// because it is one — but they say nothing about an item that lands under a
// protected root wearing a different hat. A projects root pointed at the
// artifact library puts environment trees inside it, and prune deleting out of
// the library because somebody mistyped a path is the failure this guard
// exists for.
func TestNothingUnderAProtectedRootIsEverACandidate(t *testing.T) {
	root := aPrunableCockpit(t)
	where, _ := cockpit.New(root)

	// An environment directory inside the artifact library.
	stray := filepath.Join(where.BaseArtifactsPath(), "upkeep-pathauto-d11")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := adapter.EnvironmentMeta{
		ModuleName: "pathauto", CoreMajor: "11", SeedCoreVersion: "11.4.6",
		AddOnVersion: adapter.EngineAddOnVersion,
		CreatedAt:    time.Now().Add(-90 * 24 * time.Hour),
		LastUsedAt:   time.Now().Add(-90 * 24 * time.Hour),
	}
	if err := meta.WriteTo(stray); err != nil {
		t.Fatalf("meta: %v", err)
	}

	engine := &tearingEngine{}
	code, stdout, _ := runPruneCommand(t, engine, someVolumes(nil),
		"prune", "--all", "--yes",
		"--cockpit="+root, "--projects-root="+where.BaseArtifactsPath())

	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(stdout, where.BaseArtifactsPath()) {
		t.Errorf("something inside the artifact library was pruned:\n%s", stdout)
	}
	if len(engine.tornDown) != 0 {
		t.Errorf("it tore down %v out of the artifact library", engine.tornDown)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Errorf("the tree inside the library was removed: %v", err)
	}
}

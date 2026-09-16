package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cockpit"
)

// recordingTeardown records what it was asked to dispose of, and can refuse.
type recordingTeardown struct {
	tornDown []string
	refuse   map[string]error
}

func newTeardown() *recordingTeardown {
	return &recordingTeardown{refuse: map[string]error{}}
}

func (r *recordingTeardown) Teardown(module cockpit.Module, coreMajor string) error {
	name := module.Name + "/" + coreMajor
	if err, refused := r.refuse[name]; refused {
		return err
	}
	r.tornDown = append(r.tornDown, name)

	return nil
}

// anExecutor builds one over an empty protection set and the given registry.
func anExecutor(
	teardown Teardown, modules map[string]cockpit.Module, protectedRoots []string,
) (*Executor, *[]string) {
	said := &[]string{}

	return NewExecutor(NewSelector(protectedRoots), teardown, modules, func(line string) {
		*said = append(*said, line)
	}), said
}

var watchlist = map[string]cockpit.Module{
	"pathauto": {Name: "pathauto", Project: "project/pathauto",
		CoreVersions: []string{"10", "11"}, Watched: true},
}

func aTree(projectName, module, coreMajor string, size int64) Item {
	return Item{
		Path: "/projects/" + projectName, Category: ProjectTree, Size: size,
		Module: module, CoreMajor: coreMajor, ProjectName: projectName,
	}
}

// An environment is disposed of through the adapter, never as a tree removal:
// that would leave its containers running and its named volumes allocated.
func TestAnEnvironmentIsDisposedOfThroughTheAdapter(t *testing.T) {
	teardown := newTeardown()
	executor, said := anExecutor(teardown, watchlist, nil)

	outcome, err := executor.Execute([]Item{aTree("upkeep-pathauto-d11", "pathauto", "11", 2_000_000_000)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(teardown.tornDown) != 1 || teardown.tornDown[0] != "pathauto/11" {
		t.Errorf("torn down %v", teardown.tornDown)
	}
	if outcome.FreedBytes != 2_000_000_000 {
		t.Errorf("freed %d", outcome.FreedBytes)
	}
	if len(outcome.Deleted) != 1 || len(outcome.Skipped) != 0 {
		t.Errorf("got %+v", outcome)
	}
	if !strings.Contains(strings.Join(*said, "\n"), "upkeep-pathauto-d11") {
		t.Errorf("it said nothing about what it was disposing of: %v", *said)
	}
}

// Selection is re-asserted immediately before deletion, and a protected item
// anywhere in the list stops the whole run: the list comes from the selector,
// so a protected item in it means selection was bypassed, and the safe
// response to a bug in a destructive path is to stop.
func TestAProtectedItemAbortsTheRunWithoutDeletingAnything(t *testing.T) {
	teardown := newTeardown()
	executor, _ := anExecutor(teardown, watchlist, []string{"/cockpit/base-artifacts"})

	_, err := executor.Execute([]Item{
		aTree("upkeep-pathauto-d11", "pathauto", "11", 1),
		{Path: "/cockpit/base-artifacts/11", Category: BaseArtifact},
	})

	if err == nil {
		t.Fatal("it pruned a protected item")
	}
	if !strings.Contains(err.Error(), "base artifact") {
		t.Errorf("the refusal does not say why it is protected: %v", err)
	}
	// Nothing at all, including the item before the protected one.
	if len(teardown.tornDown) != 0 {
		t.Errorf("it deleted %v before aborting", teardown.tornDown)
	}
}

// Each protection rule stops a run on its own.
func TestEveryProtectionRuleAbortsARun(t *testing.T) {
	for name, item := range map[string]Item{
		"a base artifact":       {Path: "/cockpit/base-artifacts/11", Category: BaseArtifact},
		"a committed dump":      {Path: "/cockpit/fixtures/baseline.sql.gz", Category: FixtureDump},
		"a keep-marked tree":    {Path: "/projects/x", Category: ProjectTree, KeepMarked: true},
		"a tests/fixtures dump": {Path: "/projects/x/module/tests/fixtures/a.sql", Category: Snapshot},
	} {
		teardown := newTeardown()
		executor, _ := anExecutor(teardown, watchlist, nil)

		if _, err := executor.Execute([]Item{item}); err == nil {
			t.Errorf("%s was pruned", name)
		}
	}
}

// A tree nothing can attribute is never guessed at: teardown is destructive
// and names an environment, so guessing wrong disposes of a different one than
// the operator was shown.
func TestATreeThatCannotBeAttributedIsSkippedRatherThanGuessedAt(t *testing.T) {
	teardown := newTeardown()
	executor, _ := anExecutor(teardown, watchlist, nil)

	// A partial provision: no meta, and a name no registered pair produces.
	outcome, err := executor.Execute([]Item{{
		Path: "/projects/upkeep-mystery-d99", Category: ProjectTree, Size: 3_000_000_000,
		ProjectName: "upkeep-mystery-d99",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(teardown.tornDown) != 0 {
		t.Errorf("it tore down %v", teardown.tornDown)
	}
	if len(outcome.Skipped) != 1 {
		t.Fatalf("got %+v", outcome)
	}
	if !strings.Contains(outcome.Skipped[0].Reason, "refusing to guess") {
		t.Errorf("reason %q", outcome.Skipped[0].Reason)
	}
	// And its bytes are not counted as freed, because they were not.
	if outcome.FreedBytes != 0 {
		t.Errorf("freed %d bytes it did not free", outcome.FreedBytes)
	}
}

// A tree with no meta but a name a registered pair produces is resolved from
// the registry — that is how an environment whose provision was interrupted
// before the marker still gets collected.
func TestATreeWithNoMetaIsResolvedFromTheRegistry(t *testing.T) {
	teardown := newTeardown()
	executor, _ := anExecutor(teardown, watchlist, nil)

	outcome, err := executor.Execute([]Item{{
		Path: "/projects/upkeep-pathauto-d10", Category: ProjectTree, Size: 1_000,
		ProjectName: "upkeep-pathauto-d10",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(teardown.tornDown) != 1 || teardown.tornDown[0] != "pathauto/10" {
		t.Errorf("torn down %v", teardown.tornDown)
	}
	if len(outcome.Deleted) != 1 {
		t.Errorf("got %+v", outcome)
	}
}

// The registry is a watchlist, not a gate: an environment for a module nobody
// is watching is still this tool's to tear down.
func TestAnEnvironmentForAnUnwatchedModuleIsStillTornDown(t *testing.T) {
	teardown := newTeardown()
	executor, _ := anExecutor(teardown, watchlist, nil)

	outcome, err := executor.Execute([]Item{
		aTree("upkeep-token-d12", "token", "12", 500),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(teardown.tornDown) != 1 || teardown.tornDown[0] != "token/12" {
		t.Errorf("torn down %v", teardown.tornDown)
	}
	if len(outcome.Deleted) != 1 {
		t.Errorf("got %+v", outcome)
	}
}

// A teardown that fails is a skip with its own reason, not a run that stops:
// the other candidates are independent, and one wedged engine project must not
// hold the whole reclaim hostage.
func TestAFailedTeardownSkipsThatOneAndContinues(t *testing.T) {
	teardown := newTeardown()
	teardown.refuse["pathauto/11"] = errors.New("the working copy has local work")
	executor, _ := anExecutor(teardown, watchlist, nil)

	outcome, err := executor.Execute([]Item{
		aTree("upkeep-pathauto-d11", "pathauto", "11", 2_000_000_000),
		aTree("upkeep-pathauto-d10", "pathauto", "10", 1_000_000_000),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(outcome.Deleted) != 1 || outcome.Deleted[0].ProjectName != "upkeep-pathauto-d10" {
		t.Errorf("deleted %+v", outcome.Deleted)
	}
	if len(outcome.Skipped) != 1 {
		t.Fatalf("skipped %+v", outcome.Skipped)
	}
	// The adapter's own words, because it knows why and this does not.
	if !strings.Contains(outcome.Skipped[0].Reason, "local work") {
		t.Errorf("reason %q", outcome.Skipped[0].Reason)
	}
	// Only what was actually freed.
	if outcome.FreedBytes != 1_000_000_000 {
		t.Errorf("freed %d", outcome.FreedBytes)
	}
}

// A volume is reclaimed by its project's teardown and never touched directly,
// so it is credited only when that teardown happened.
func TestAVolumeIsCreditedOnlyWhenItsTreeWasTornDown(t *testing.T) {
	teardown := newTeardown()
	executor, _ := anExecutor(teardown, watchlist, nil)

	outcome, err := executor.Execute([]Item{
		aTree("upkeep-pathauto-d11", "pathauto", "11", 2_000_000_000),
		{Path: "upkeep-pathauto-d11-mariadb", Category: ProjectVolume, Size: 800_000_000,
			ProjectName: "upkeep-pathauto-d11"},
		// Its tree is not in this run at all.
		{Path: "upkeep-pathauto-d10-mariadb", Category: ProjectVolume, Size: 700_000_000,
			ProjectName: "upkeep-pathauto-d10"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if outcome.FreedBytes != 2_800_000_000 {
		t.Errorf("freed %d", outcome.FreedBytes)
	}
	if len(outcome.Skipped) != 1 || outcome.Skipped[0].Item.Path != "upkeep-pathauto-d10-mariadb" {
		t.Fatalf("skipped %+v", outcome.Skipped)
	}
	if !strings.Contains(outcome.Skipped[0].Reason, "its tree was not pruned in this run") {
		t.Errorf("reason %q", outcome.Skipped[0].Reason)
	}
}

// A volume whose tree's teardown failed is not credited either: the teardown
// is what would have reclaimed it, and it did not happen.
func TestAVolumeIsNotCreditedWhenItsTreesTeardownFailed(t *testing.T) {
	teardown := newTeardown()
	teardown.refuse["pathauto/11"] = errors.New("engine refused")
	executor, _ := anExecutor(teardown, watchlist, nil)

	outcome, err := executor.Execute([]Item{
		aTree("upkeep-pathauto-d11", "pathauto", "11", 2_000_000_000),
		{Path: "upkeep-pathauto-d11-mariadb", Category: ProjectVolume, Size: 800_000_000,
			ProjectName: "upkeep-pathauto-d11"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if outcome.FreedBytes != 0 {
		t.Errorf("freed %d bytes from a teardown that did not happen", outcome.FreedBytes)
	}
	if len(outcome.Skipped) != 2 {
		t.Errorf("skipped %+v", outcome.Skipped)
	}
}

// aSnapshotOnDisk writes a materialised snapshot and its sidecar.
func aSnapshotOnDisk(t *testing.T, name string) (projectPath, artifact, meta string) {
	t.Helper()

	projectPath = filepath.Join(t.TempDir(), "upkeep-pathauto-d11")
	artifact = filepath.Join(projectPath, adapter.SnapshotMaterializedDir, name+".sql")
	meta = filepath.Join(projectPath, adapter.SnapshotMetaDir, name+".meta")

	for _, path := range []string{artifact, meta} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	return projectPath, artifact, meta
}

// A snapshot is a file removal: the artifact and its sidecar, which is
// bookkeeping the artifact's absence makes meaningless.
func TestASnapshotAndItsSidecarAreBothRemoved(t *testing.T) {
	_, artifact, meta := aSnapshotOnDisk(t, "baseline")

	executor, _ := anExecutor(newTeardown(), watchlist, nil)
	outcome, err := executor.Execute([]Item{{
		Path: artifact, Category: Snapshot, Size: 40_000_000, ProjectName: "upkeep-pathauto-d11",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, path := range []string{artifact, meta} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s survived", path)
		}
	}
	if outcome.FreedBytes != 40_000_000 || len(outcome.Deleted) != 1 {
		t.Errorf("got %+v", outcome)
	}
}

// A sidecar that will not go is worth saying and not worth failing the
// snapshot over: the bytes the operator asked to reclaim are already gone.
func TestASidecarThatWillNotGoDoesNotFailTheSnapshot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes files regardless of the directory's mode")
	}

	_, artifact, meta := aSnapshotOnDisk(t, "baseline")
	if err := os.Chmod(filepath.Dir(meta), 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(meta), 0o755) })

	executor, said := anExecutor(newTeardown(), watchlist, nil)
	outcome, err := executor.Execute([]Item{{
		Path: artifact, Category: Snapshot, Size: 40_000_000,
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(outcome.Deleted) != 1 || outcome.FreedBytes != 40_000_000 {
		t.Errorf("got %+v", outcome)
	}
	if !strings.Contains(strings.Join(*said, "\n"), "sidecar") {
		t.Errorf("the sidecar failure was silent: %v", *said)
	}
}

// Bytes are counted only after a confirmed removal: reporting them as
// reclaimed when the removal failed over-reports a destructive operation,
// which is the wrong direction to be wrong in.
func TestASnapshotThatWillNotGoIsNotCountedAsFreed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes files regardless of the directory's mode")
	}

	_, artifact, _ := aSnapshotOnDisk(t, "baseline")
	if err := os.Chmod(filepath.Dir(artifact), 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(artifact), 0o755) })

	executor, _ := anExecutor(newTeardown(), watchlist, nil)
	outcome, err := executor.Execute([]Item{{
		Path: artifact, Category: Snapshot, Size: 40_000_000,
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if outcome.FreedBytes != 0 || len(outcome.Deleted) != 0 {
		t.Errorf("it reported bytes it did not free: %+v", outcome)
	}
	if len(outcome.Skipped) != 1 || !strings.Contains(outcome.Skipped[0].Reason, "permissions") {
		t.Errorf("skipped %+v", outcome.Skipped)
	}
}

// A snapshot already gone is a snapshot removed: the operator wants it absent,
// and it is.
func TestASnapshotThatIsAlreadyGoneCountsAsRemoved(t *testing.T) {
	executor, _ := anExecutor(newTeardown(), watchlist, nil)

	outcome, err := executor.Execute([]Item{{
		Path:     filepath.Join(t.TempDir(), "materialized", "vanished.sql"),
		Category: Snapshot, Size: 1_000,
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(outcome.Deleted) != 1 || len(outcome.Skipped) != 0 {
		t.Errorf("got %+v", outcome)
	}
}

// Nothing to prune is not a failure.
func TestPruningNothingIsNotAFailure(t *testing.T) {
	executor, _ := anExecutor(newTeardown(), watchlist, nil)

	outcome, err := executor.Execute(nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if outcome.FreedBytes != 0 || len(outcome.Deleted) != 0 || len(outcome.Skipped) != 0 {
		t.Errorf("got %+v", outcome)
	}
}

// Trees go before the volumes that depend on them, whatever order the
// candidate list arrived in.
func TestTreesAreTornDownBeforeTheirVolumesAreAccountedFor(t *testing.T) {
	teardown := newTeardown()
	executor, _ := anExecutor(teardown, watchlist, nil)

	// The volume first in the list.
	outcome, err := executor.Execute([]Item{
		{Path: "upkeep-pathauto-d11-mariadb", Category: ProjectVolume, Size: 800_000_000,
			ProjectName: "upkeep-pathauto-d11"},
		aTree("upkeep-pathauto-d11", "pathauto", "11", 2_000_000_000),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if outcome.FreedBytes != 2_800_000_000 {
		t.Errorf("the volume was judged before its tree was torn down: freed %d", outcome.FreedBytes)
	}
	if len(outcome.Skipped) != 0 {
		t.Errorf("skipped %+v", outcome.Skipped)
	}
}

// A snapshot that is not where materialised snapshots live has no sidecar, so
// none is looked for: a path derived from the wrong shape would name some
// unrelated file, and this is a destructive path.
func TestASnapshotOutsideTheMaterialisedDirectoryHasNoSidecar(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "loose.sql")
	if err := os.WriteFile(artifact, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	executor, said := anExecutor(newTeardown(), watchlist, nil)
	outcome, err := executor.Execute([]Item{{Path: artifact, Category: Snapshot, Size: 10}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(outcome.Deleted) != 1 {
		t.Errorf("got %+v", outcome)
	}
	if strings.Contains(strings.Join(*said, "\n"), "sidecar") {
		t.Errorf("it went looking for a sidecar that cannot exist: %v", *said)
	}
}

// An executor with nowhere to report progress still prunes — a caller that
// does not want a running commentary is not a caller that wants a crash.
func TestAnExecutorWithNoLogStillPrunes(t *testing.T) {
	teardown := newTeardown()
	executor := NewExecutor(NewSelector(nil), teardown, watchlist, nil)

	outcome, err := executor.Execute([]Item{aTree("upkeep-pathauto-d11", "pathauto", "11", 10)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(outcome.Deleted) != 1 || len(teardown.tornDown) != 1 {
		t.Errorf("got %+v", outcome)
	}
}

// A tree with no project name is named by its path in the progress line: an
// operator watching a destructive command has to be able to tell which thing
// is going.
func TestATreeWithNoProjectNameIsNamedByItsPath(t *testing.T) {
	teardown := newTeardown()
	executor, said := anExecutor(teardown, watchlist, nil)

	_, err := executor.Execute([]Item{{
		Path: "/projects/upkeep-pathauto-d11", Category: ProjectTree,
		Module: "pathauto", CoreMajor: "11",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(strings.Join(*said, "\n"), "/projects/upkeep-pathauto-d11") {
		t.Errorf("it did not say what it was disposing of: %v", *said)
	}
}

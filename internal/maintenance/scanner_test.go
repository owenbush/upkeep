package maintenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cockpit"
)

// aSizer measures by named path, so a test can say what a tree costs without
// writing gigabytes.
func aSizer(sizes map[string]int64) Sizer {
	return func(path string) int64 { return sizes[filepath.Base(path)] }
}

// scene is a cockpit and a projects root laid out on disk.
type scene struct {
	t            *testing.T
	cockpit      *cockpit.Cockpit
	projectsRoot string
}

func aScene(t *testing.T) *scene {
	t.Helper()

	root := t.TempDir()

	return &scene{
		t:            t,
		cockpit:      &cockpit.Cockpit{Root: filepath.Join(root, "cockpit")},
		projectsRoot: filepath.Join(root, "projects"),
	}
}

func (s *scene) write(path, contents string) string {
	s.t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		s.t.Fatalf("write: %v", err)
	}

	return path
}

func (s *scene) mkdir(path string) string {
	s.t.Helper()

	if err := os.MkdirAll(path, 0o755); err != nil {
		s.t.Fatalf("mkdir: %v", err)
	}

	return path
}

// project lays out an engine project with a completion marker.
func (s *scene) project(name, module, coreMajor string, lastUsed time.Time) string {
	s.t.Helper()

	path := s.mkdir(filepath.Join(s.projectsRoot, name))
	meta := adapter.EnvironmentMeta{
		ModuleName: module, CoreMajor: coreMajor, SeedCoreVersion: coreMajor + ".4.6",
		AddOnVersion: adapter.EngineAddOnVersion,
		CreatedAt:    lastUsed.Add(-24 * time.Hour), LastUsedAt: lastUsed,
	}
	if err := meta.WriteTo(path); err != nil {
		s.t.Fatalf("meta: %v", err)
	}

	return path
}

// byPath indexes a scan for assertions.
func byPath(items []Item) map[string]Item {
	indexed := map[string]Item{}
	for _, item := range items {
		indexed[filepath.Base(item.Path)] = item
	}

	return indexed
}

// The three sources, each in its own category, attributed from the
// environment meta.
func TestAScanFindsArtifactsDumpsAndProjects(t *testing.T) {
	scene := aScene(t)
	lastUsed := time.Now().Add(-72 * time.Hour).Truncate(time.Second)

	scene.write(filepath.Join(scene.cockpit.BaseArtifactsPath(), "11", "meta.yml"), "core_version: 11.4.6\n")
	scene.write(filepath.Join(scene.cockpit.BaseArtifactsPath(), "12", "meta.yml"), "core_version: 12.0.0\n")
	scene.write(filepath.Join(scene.cockpit.FixturesPath(), "baseline.sql.gz"), "dump")
	project := scene.project("upkeep-pathauto-d11", "pathauto", "11", lastUsed)
	scene.write(filepath.Join(project, moduleFixturesDir, "committed.sql.gz"), "dump")

	items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(map[string]int64{
		"11": 4_000_000_000, "12": 5_000_000_000,
		"baseline.sql.gz": 2_000_000, "upkeep-pathauto-d11": 1_500_000_000,
		"committed.sql.gz": 900_000,
	})).Scan()

	found := byPath(items)
	if len(items) != 5 {
		t.Fatalf("found %d items: %+v", len(items), items)
	}

	for path, want := range map[string]Category{
		"11":                  BaseArtifact,
		"12":                  BaseArtifact,
		"baseline.sql.gz":     FixtureDump,
		"upkeep-pathauto-d11": ProjectTree,
		"committed.sql.gz":    FixtureDump,
	} {
		if found[path].Category != want {
			t.Errorf("%s is %q, want %q", path, found[path].Category, want)
		}
	}

	tree := found["upkeep-pathauto-d11"]
	if tree.Module != "pathauto" || tree.CoreMajor != "11" || tree.ProjectName != "upkeep-pathauto-d11" {
		t.Errorf("the tree was not attributed: %+v", tree)
	}
	if !tree.LastUsedAt.Equal(lastUsed) {
		t.Errorf("last used %s, want %s", tree.LastUsedAt, lastUsed)
	}
	if tree.Size != 1_500_000_000 {
		t.Errorf("size %d", tree.Size)
	}

	// A module's committed dump carries the project's attribution, because
	// that is what a prune narrowed to a module has to match on.
	if found["committed.sql.gz"].Module != "pathauto" {
		t.Errorf("the committed dump was not attributed: %+v", found["committed.sql.gz"])
	}
	// The library's is not attributed to anything: it is shared.
	if found["baseline.sql.gz"].Module != "" {
		t.Errorf("a library dump was attributed to a module: %+v", found["baseline.sql.gz"])
	}
}

// A partial provision is still disk usage to report, and it is exactly what
// prune exists to collect — so it is inventoried with unknown attribution
// rather than skipped.
func TestAPartialProvisionIsInventoriedWithUnknownAttribution(t *testing.T) {
	scene := aScene(t)
	scene.mkdir(filepath.Join(scene.projectsRoot, "upkeep-token-d11"))

	items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan()

	if len(items) != 1 {
		t.Fatalf("found %d items: %+v", len(items), items)
	}
	if items[0].Category != ProjectTree {
		t.Errorf("category %q", items[0].Category)
	}
	if items[0].Module != "" || items[0].CoreMajor != "" {
		t.Errorf("a directory with no meta was attributed: %+v", items[0])
	}
	// Unknown age, which the selector reads as "cannot tell" rather than
	// "old" — deleting on the strength of a question that could not be asked
	// is the one mistake a prune must not make.
	if !items[0].LastUsedAt.IsZero() {
		t.Errorf("it invented an age: %s", items[0].LastUsedAt)
	}
	// And the project name is still there, because teardown needs it.
	if items[0].ProjectName != "upkeep-token-d11" {
		t.Errorf("project name %q", items[0].ProjectName)
	}
}

// A malformed meta degrades the same way rather than failing the inventory.
func TestAMalformedMetaDegradesRatherThanFailingTheScan(t *testing.T) {
	scene := aScene(t)
	path := scene.mkdir(filepath.Join(scene.projectsRoot, "upkeep-token-d11"))
	scene.write(adapter.EnvMetaPath(path), "\tnot: [yaml")

	items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan()

	if len(items) != 1 || items[0].Module != "" {
		t.Fatalf("got %+v", items)
	}
}

// Only engine project directories, so nothing else under the projects root is
// offered to a prune.
func TestOnlyEngineProjectDirectoriesAreInventoried(t *testing.T) {
	scene := aScene(t)
	for _, name := range []string{
		"upkeep-pathauto-d11",     // yes
		"upkeep-field-tokens-d12", // yes: hyphens from a module's underscores
		"upkeep-pathauto",         // no core
		"upkeep-pathauto-d",       // no major
		"pathauto-d11",            // not ours
		"upkeep-Pathauto-d11",     // engine names are lower case
		".hidden",
		"notes.txt",
	} {
		scene.mkdir(filepath.Join(scene.projectsRoot, name))
	}

	items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan()

	found := byPath(items)
	if len(items) != 2 {
		t.Fatalf("found %d: %+v", len(items), items)
	}
	for _, name := range []string{"upkeep-pathauto-d11", "upkeep-field-tokens-d12"} {
		if _, ok := found[name]; !ok {
			t.Errorf("%s was not inventoried", name)
		}
	}
}

// A snapshot's age comes from the add-on's sidecar when there is one, because
// a file's mtime says when it was written and not when it was last used.
func TestASnapshotTakesItsAgeFromTheSidecar(t *testing.T) {
	scene := aScene(t)
	project := scene.project("upkeep-pathauto-d11", "pathauto", "11", time.Now())

	scene.write(filepath.Join(project, adapter.SnapshotMaterializedDir, "baseline.sql"), "dump")
	scene.write(
		filepath.Join(project, adapter.SnapshotMetaDir, "baseline.meta"),
		"name=baseline\nmaterialized_at=2026-03-01T09:30:00Z\nsomething=else\n",
	)
	// And one with no sidecar, which falls back to the file's own time.
	scene.write(filepath.Join(project, adapter.SnapshotMaterializedDir, "orphan.sql"), "dump")

	items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan()
	found := byPath(items)

	stamped := found["baseline.sql"]
	if stamped.Category != Snapshot {
		t.Fatalf("baseline is %q", stamped.Category)
	}
	if stamped.LastUsedAt.UTC().Format(time.RFC3339) != "2026-03-01T09:30:00Z" {
		t.Errorf("last used %s", stamped.LastUsedAt)
	}
	// A snapshot inherits the environment's attribution: it belongs to it.
	if stamped.Module != "pathauto" || stamped.ProjectName != "upkeep-pathauto-d11" {
		t.Errorf("the snapshot was not attributed: %+v", stamped)
	}

	if found["orphan.sql"].LastUsedAt.IsZero() {
		t.Error("a snapshot with no sidecar got no age at all")
	}
}

// A sidecar that does not record the time is the same as no sidecar.
func TestASidecarWithNoTimestampFallsBackToTheFile(t *testing.T) {
	scene := aScene(t)
	project := scene.project("upkeep-pathauto-d11", "pathauto", "11", time.Now())
	scene.write(filepath.Join(project, adapter.SnapshotMaterializedDir, "baseline.sql"), "dump")
	scene.write(filepath.Join(project, adapter.SnapshotMetaDir, "baseline.meta"), "name=baseline\n")

	found := byPath(NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan())

	if found["baseline.sql"].LastUsedAt.IsZero() {
		t.Error("it got no age at all")
	}
}

// Keep markers, at both levels.
func TestKeepMarkersAreReadAtBothLevels(t *testing.T) {
	scene := aScene(t)
	project := scene.project("upkeep-pathauto-d11", "pathauto", "11", time.Now())
	scene.write(filepath.Join(project, KeepMarker), "")
	scene.write(filepath.Join(project, adapter.SnapshotMaterializedDir, "kept.sql"), "dump")
	scene.write(filepath.Join(project, adapter.SnapshotMaterializedDir, "kept.sql"+KeepMarker), "")
	scene.write(filepath.Join(project, adapter.SnapshotMaterializedDir, "ordinary.sql"), "dump")

	found := byPath(NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan())

	if !found["upkeep-pathauto-d11"].KeepMarked {
		t.Error("the environment's keep marker was not read")
	}
	if !found["kept.sql"].KeepMarked {
		t.Error("the snapshot's keep marker was not read")
	}
	if found["ordinary.sql"].KeepMarked {
		t.Error("an unmarked snapshot read as kept")
	}
	// The marker file itself is not a snapshot.
	if _, inventoried := found["kept.sql"+KeepMarker]; inventoried {
		t.Error("the marker file was inventoried as a snapshot")
	}
}

// The tree's size excludes its materialised snapshots, so the inventory total
// never double-counts bytes.
func TestATreeDoesNotDoubleCountItsSnapshots(t *testing.T) {
	scene := aScene(t)
	scene.project("upkeep-pathauto-d11", "pathauto", "11", time.Now())

	items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(map[string]int64{
		"upkeep-pathauto-d11": 3_000_000_000,
		"materialized":        800_000_000,
		"snapshots":           1_000_000,
	})).Scan()

	if items[0].Size != 3_000_000_000-800_000_000-1_000_000 {
		t.Errorf("size %d", items[0].Size)
	}
}

// A measurement that comes back inconsistent must not make a tree read as
// negative bytes, which would show up as a reclaim total that grows when you
// delete something.
func TestATreeSmallerThanItsSnapshotsIsZeroRatherThanNegative(t *testing.T) {
	scene := aScene(t)
	scene.project("upkeep-pathauto-d11", "pathauto", "11", time.Now())

	items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(map[string]int64{
		"upkeep-pathauto-d11": 1_000, "materialized": 900_000_000,
	})).Scan()

	if items[0].Size != 0 {
		t.Errorf("size %d", items[0].Size)
	}
}

// A directory listing resolves through symlinked components, so a symlinked
// snapshot directory would enumerate files outside the environment — and prune
// deletes what the inventory reports.
func TestASymlinkedSnapshotDirectoryIsSkippedAndSaidSo(t *testing.T) {
	scene := aScene(t)
	project := scene.project("upkeep-pathauto-d11", "pathauto", "11", time.Now())

	elsewhere := scene.mkdir(filepath.Join(t.TempDir(), "somebody-elses"))
	scene.write(filepath.Join(elsewhere, "precious.sql"), "not ours")

	materialized := filepath.Join(project, adapter.SnapshotMaterializedDir)
	scene.mkdir(filepath.Dir(materialized))
	if err := os.Symlink(elsewhere, materialized); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	scanner := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil))
	items := scanner.Scan()

	for _, item := range items {
		if item.Category == Snapshot {
			t.Errorf("a file outside the environment was inventoried as its snapshot: %s", item.Path)
		}
	}
	if !strings.Contains(strings.Join(scanner.Warnings(), "\n"), "is a symlink") {
		t.Errorf("it skipped silently: %v", scanner.Warnings())
	}
}

// An unreadable directory is not an empty one: a scan never aborts on it, but
// the operator is told, so "there is nothing here" and "I could not look" stay
// distinguishable.
func TestAnUnreadableDirectoryIsWarnedAboutRatherThanReadAsEmpty(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory regardless of its mode")
	}

	scene := aScene(t)
	fixtures := scene.mkdir(scene.cockpit.FixturesPath())
	scene.write(filepath.Join(fixtures, "baseline.sql.gz"), "dump")
	if err := os.Chmod(fixtures, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(fixtures, 0o755) })

	scanner := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil))
	items := scanner.Scan()

	if len(items) != 0 {
		t.Errorf("it read a directory it cannot read: %+v", items)
	}
	if len(scanner.Warnings()) == 0 {
		t.Fatal("an unreadable directory read as an empty one")
	}
	if !strings.Contains(scanner.Warnings()[0], fixtures) {
		t.Errorf("the warning does not name the directory: %v", scanner.Warnings())
	}
}

// An unreadable projects root costs every environment, which is worth saying
// outright.
func TestAnUnreadableProjectsRootIsWarnedAbout(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory regardless of its mode")
	}

	scene := aScene(t)
	scene.project("upkeep-pathauto-d11", "pathauto", "11", time.Now())
	if err := os.Chmod(scene.projectsRoot, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(scene.projectsRoot, 0o755) })

	scanner := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil))
	if items := scanner.Scan(); len(items) != 0 {
		t.Errorf("got %+v", items)
	}
	if !strings.Contains(strings.Join(scanner.Warnings(), "\n"), "no environments are reported") {
		t.Errorf("warnings %v", scanner.Warnings())
	}
}

// A cockpit and a projects root that are simply not there yet are empty, not
// unreadable: that is an ordinary first run and there is nothing to report.
func TestAnAbsentCockpitScansEmptyWithoutComplaining(t *testing.T) {
	scene := aScene(t)

	scanner := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil))
	if items := scanner.Scan(); len(items) != 0 {
		t.Errorf("got %+v", items)
	}
	if len(scanner.Warnings()) != 0 {
		t.Errorf("a first run complained: %v", scanner.Warnings())
	}
}

// Scanning again reports this scan's warnings, not the last one's.
func TestWarningsBelongToTheScanThatProducedThem(t *testing.T) {
	scene := aScene(t)
	project := scene.project("upkeep-pathauto-d11", "pathauto", "11", time.Now())
	materialized := filepath.Join(project, adapter.SnapshotMaterializedDir)
	scene.mkdir(filepath.Dir(materialized))
	if err := os.Symlink(t.TempDir(), materialized); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	scanner := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil))
	scanner.Scan()
	first := len(scanner.Warnings())
	scanner.Scan()

	if len(scanner.Warnings()) != first {
		t.Errorf("warnings accumulated across scans: %d then %d", first, len(scanner.Warnings()))
	}
}

// The artifact library and the fixture library are never pruned from.
func TestTheProtectedRootsAreTheTwoLibraries(t *testing.T) {
	scene := aScene(t)

	roots := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).ProtectedRoots()
	want := []string{scene.cockpit.BaseArtifactsPath(), scene.cockpit.FixturesPath()}

	if len(roots) != len(want) {
		t.Fatalf("got %v", roots)
	}
	for i, root := range want {
		if roots[i] != root {
			t.Errorf("root %d is %q, want %q", i, roots[i], root)
		}
	}
}

// A volume belongs to the environment it was made for, and inherits its
// attribution and age: they are reclaimed together, and a volume has no age of
// its own.
func TestVolumesInheritTheirEnvironmentsAttribution(t *testing.T) {
	lastUsed := time.Now().Add(-48 * time.Hour)
	inventory := []Item{{
		Path: "/projects/upkeep-pathauto-d11", Category: ProjectTree,
		Module: "pathauto", CoreMajor: "11", ProjectName: "upkeep-pathauto-d11",
		LastUsedAt: lastUsed,
	}}

	items := ItemsForVolumes(inventory, []adapter.ProjectVolume{
		{Name: "upkeep-pathauto-d11-mariadb", ProjectName: "upkeep-pathauto-d11", SizeBytes: 700_000_000},
		// Somebody else's project: none of our business, and prune deletes
		// what the inventory reports.
		{Name: "someone-elses-db", ProjectName: "someone-elses", SizeBytes: 9_000_000_000},
	})

	if len(items) != 1 {
		t.Fatalf("got %+v", items)
	}
	if items[0].Category != ProjectVolume || items[0].Path != "upkeep-pathauto-d11-mariadb" {
		t.Errorf("got %+v", items[0])
	}
	if items[0].Module != "pathauto" || items[0].CoreMajor != "11" {
		t.Errorf("the volume was not attributed: %+v", items[0])
	}
	if !items[0].LastUsedAt.Equal(lastUsed) {
		t.Errorf("the volume did not inherit the tree's age: %s", items[0].LastUsedAt)
	}
	if items[0].Size != 700_000_000 {
		t.Errorf("size %d", items[0].Size)
	}
}

// A volume whose project has no tree here is not ours to report on, whatever
// its name looks like.
func TestAVolumeWithNoTreeInTheInventoryIsNotReported(t *testing.T) {
	items := ItemsForVolumes(
		[]Item{{Path: "/cockpit/base-artifacts/11", Category: BaseArtifact}},
		[]adapter.ProjectVolume{{Name: "upkeep-gone-d11-mariadb", ProjectName: "upkeep-gone-d11"}},
	)

	if len(items) != 0 {
		t.Errorf("got %+v", items)
	}
}

// An environment written by an older upkeep — one whose meta predates a field
// the strict reader requires — keeps its attribution and its age.
//
// Read strictly, it would lose both, which makes it invisible to
// `prune --module` and to `prune --older-than`: the two commands that exist to
// collect exactly this.
func TestAnEnvironmentFromAnOlderUpkeepKeepsItsAttribution(t *testing.T) {
	scene := aScene(t)
	path := scene.mkdir(filepath.Join(scene.projectsRoot, "upkeep-pathauto-d11"))
	// No addon_version and no seed_core_version: the shape before those
	// existed.
	scene.write(adapter.EnvMetaPath(path),
		"module: pathauto\ncore_major: '11'\ncreated_at: '2026-01-04T10:00:00+00:00'\n")

	items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan()

	if len(items) != 1 {
		t.Fatalf("got %+v", items)
	}
	if items[0].Module != "pathauto" || items[0].CoreMajor != "11" {
		t.Errorf("an older meta lost its attribution: %+v", items[0])
	}
	if items[0].LastUsedAt.UTC().Format(time.RFC3339) != "2026-01-04T10:00:00Z" {
		t.Errorf("an older meta lost its age: %s", items[0].LastUsedAt)
	}
}

// And a meta with no usable timestamp at all is unknown rather than wrong: the
// selector reads that as "cannot tell", which is what stops a prune deleting
// on the strength of a question it could not ask.
func TestAMetaWithNoUsableTimeHasNoAge(t *testing.T) {
	scene := aScene(t)
	path := scene.mkdir(filepath.Join(scene.projectsRoot, "upkeep-pathauto-d11"))
	scene.write(adapter.EnvMetaPath(path),
		"module: pathauto\ncore_major: '11'\ncreated_at: not-a-date\nlast_used_at: also-not\n")

	items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan()

	if items[0].Module != "pathauto" {
		t.Errorf("attribution was lost along with the age: %+v", items[0])
	}
	if !items[0].LastUsedAt.IsZero() {
		t.Errorf("it invented an age: %s", items[0].LastUsedAt)
	}
}

// A file named like an engine project is not one: prune tears a ProjectTree
// down through the adapter, which would be asked to dispose of something that
// was never an environment.
func TestAFileNamedLikeAProjectIsNotOne(t *testing.T) {
	scene := aScene(t)
	scene.mkdir(scene.projectsRoot)
	scene.write(filepath.Join(scene.projectsRoot, "upkeep-pathauto-d11"), "not a directory")

	if items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan(); len(items) != 0 {
		t.Errorf("a file was inventoried as an environment: %+v", items)
	}
}

// And a directory named like a dump is not a dump: prune deletes a FixtureDump
// as a file.
func TestADirectoryNamedLikeADumpIsNotOne(t *testing.T) {
	scene := aScene(t)
	scene.mkdir(filepath.Join(scene.cockpit.FixturesPath(), "baseline.sql.gz"))
	scene.write(filepath.Join(scene.cockpit.FixturesPath(), "real.sql.gz"), "dump")

	found := byPath(NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan())

	if _, inventoried := found["baseline.sql.gz"]; inventoried {
		t.Error("a directory was inventoried as a dump")
	}
	if _, inventoried := found["real.sql.gz"]; !inventoried {
		t.Error("the real dump was missed")
	}
}

// A meta whose fields are explicitly null has no attribution — not one to a
// module called "null", which is what reading the node's value without
// checking its tag produces.
func TestAnExplicitlyNullFieldIsUnknownRatherThanTheWordNull(t *testing.T) {
	scene := aScene(t)
	path := scene.mkdir(filepath.Join(scene.projectsRoot, "upkeep-pathauto-d11"))
	scene.write(adapter.EnvMetaPath(path), "module: null\ncore_major: ~\nlast_used_at:\n")

	items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan()

	if items[0].Module != "" || items[0].CoreMajor != "" {
		t.Errorf("a null field became an attribution: %+v", items[0])
	}
	if !items[0].LastUsedAt.IsZero() {
		t.Errorf("a null timestamp became an age: %s", items[0].LastUsedAt)
	}
}

// And a field holding a structure is unknown too, rather than whatever a
// scalar read off a mapping node happens to produce.
func TestAStructuredFieldIsUnknownAttribution(t *testing.T) {
	scene := aScene(t)
	path := scene.mkdir(filepath.Join(scene.projectsRoot, "upkeep-pathauto-d11"))
	scene.write(adapter.EnvMetaPath(path), "module:\n  - pathauto\ncore_major:\n  n: 11\n")

	items := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan()

	if items[0].Module != "" || items[0].CoreMajor != "" {
		t.Errorf("a structured field became an attribution: %+v", items[0])
	}
}

// An unreadable base-artifacts directory costs the artifacts, not the scan: a
// prune must still be able to run over the rest of the disk.
func TestAnUnreadableArtifactLibraryCostsOnlyTheArtifacts(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory regardless of its mode")
	}

	scene := aScene(t)
	artifacts := scene.mkdir(filepath.Join(scene.cockpit.BaseArtifactsPath(), "11"))
	scene.project("upkeep-pathauto-d11", "pathauto", "11", time.Now())
	if err := os.Chmod(filepath.Dir(artifacts), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(artifacts), 0o755) })

	scanner := NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil))
	items := scanner.Scan()

	for _, item := range items {
		if item.Category == BaseArtifact {
			t.Errorf("it read an artifact out of a directory it cannot read: %s", item.Path)
		}
	}
	// The environment is still reported, which is the point.
	if len(items) != 1 || items[0].Category != ProjectTree {
		t.Errorf("the rest of the disk went with it: %+v", items)
	}
	if !strings.Contains(strings.Join(scanner.Warnings(), "\n"), "not readable") {
		t.Errorf("warnings %v", scanner.Warnings())
	}
}

// A sidecar whose timestamp cannot be read falls back to the file, rather than
// to a time parsed out of whatever was there.
func TestAnUnparseableSidecarTimestampFallsBackToTheFile(t *testing.T) {
	scene := aScene(t)
	project := scene.project("upkeep-pathauto-d11", "pathauto", "11", time.Now())
	scene.write(filepath.Join(project, adapter.SnapshotMaterializedDir, "baseline.sql"), "dump")
	scene.write(
		filepath.Join(project, adapter.SnapshotMetaDir, "baseline.meta"),
		"materialized_at=whenever it was\n",
	)

	found := byPath(NewScanner(scene.cockpit, scene.projectsRoot, aSizer(nil)).Scan())

	if found["baseline.sql"].LastUsedAt.IsZero() {
		t.Error("it got no age at all")
	}
	if found["baseline.sql"].LastUsedAt.Year() < 2020 {
		t.Errorf("it parsed nonsense into a time: %s", found["baseline.sql"].LastUsedAt)
	}
}

// A file that is gone by the time it is measured has no time, rather than the
// zero instant read as a real one: the selector treats an unknown age as
// "cannot tell".
func TestAFileThatIsNotThereHasNoModificationTime(t *testing.T) {
	if at := modifiedAt(filepath.Join(t.TempDir(), "vanished.sql")); !at.IsZero() {
		t.Errorf("got %s", at)
	}
}

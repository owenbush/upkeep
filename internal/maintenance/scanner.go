package maintenance

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/cockpit"
)

// KeepMarker marks something as kept: <project>/.keep for a whole
// environment, <artifact>.keep for one snapshot.
const KeepMarker = ".keep"

// projectDir is what an engine project directory is named.
var projectDir = regexp.MustCompile(`^upkeep-[a-z0-9-]+-d\d+$`)

// moduleFixturesDir is where a module's committed fixture dumps live inside
// its working copy.
const moduleFixturesDir = "module/tests/fixtures"

// Sizer measures real on-disk usage for one path, in bytes.
type Sizer func(string) int64

// Scanner walks the cockpit and the projects root and produces the typed disk
// inventory that `status --disk` renders and `prune` selects from.
//
// Sources:
//
//   - <cockpit>/base-artifacts/<major>/     one BaseArtifact item each
//   - <cockpit>/fixtures/*.sql.gz           FixtureDump items (the library)
//   - <projects-root>/upkeep-*-d<major>/    one ProjectTree item each,
//     attributed from its environment meta — read leniently, so a partial
//     provision with no completion marker is still inventoried, with unknown
//     age — plus, per project, its materialised snapshots and the module's
//     own committed fixture dumps.
//
// Volumes are not scanned here: they exist only engine-side, so they come from
// the adapter's volume probe and are folded in by ItemsForVolumes.
//
// An unreadable directory is deliberately not the same thing as an empty one.
// For prune, under-reporting fails safe — nothing is over-deleted — so a scan
// never aborts on one; but it records a warning naming the directory, so the
// operator can tell "there is nothing here" from "I could not look".
type Scanner struct {
	cockpit      *cockpit.Cockpit
	projectsRoot string
	sizer        Sizer

	warnings []string
}

// NewScanner builds a scanner over a way of measuring the disk —
// DiskSizer(runner) in production.
func NewScanner(where *cockpit.Cockpit, projectsRoot string, sizer Sizer) *Scanner {
	return &Scanner{cockpit: where, projectsRoot: projectsRoot, sizer: sizer}
}

// Scan is the whole inventory.
func (s *Scanner) Scan() []Item {
	s.warnings = nil

	items := s.baseArtifacts()
	items = append(items, s.libraryDumps()...)

	return append(items, s.projects()...)
}

// Warnings names the directories the last scan could not read, and so may have
// under-reported.
func (s *Scanner) Warnings() []string { return s.warnings }

// ProtectedRoots is the path prefixes nothing may ever be pruned from, for
// this cockpit.
func (s *Scanner) ProtectedRoots() []string {
	return []string{s.cockpit.BaseArtifactsPath(), s.cockpit.FixturesPath()}
}

// baseArtifacts is one item per core's artifact set.
func (s *Scanner) baseArtifacts() []Item {
	layout := baseartifact.NewLayout(s.cockpit.BaseArtifactsPath())

	versions, err := layout.VersionsOnDisk()
	if err != nil {
		// A prune must still be able to run over the rest of the disk.
		s.warnings = append(s.warnings, err.Error())

		return nil
	}

	items := make([]Item, 0, len(versions))
	for _, version := range versions {
		// Joined rather than asked for through VersionDir, which validates the
		// version and can therefore fail. Every name here came out of
		// VersionsOnDisk, which only returns ones that already passed that
		// validation — so the failure branch would be unreachable, and an
		// unreachable branch is one nothing can ever hold to being right.
		path := filepath.Join(layout.Dir, version)
		items = append(items, Item{
			Path:      path,
			Category:  BaseArtifact,
			Size:      s.sizer(path),
			CoreMajor: version,
		})
	}

	return items
}

// libraryDumps is the shared fixture library.
func (s *Scanner) libraryDumps() []Item {
	var items []Item
	for _, dump := range s.globIn(s.cockpit.FixturesPath(), "*.sql.gz") {
		items = append(items, Item{
			Path: dump, Category: FixtureDump, Size: s.sizer(dump),
		})
	}

	return items
}

// globIn matches a pattern in a directory, distinguishing "nothing matched"
// from "could not read the directory" — which the glob itself reports
// identically.
func (s *Scanner) globIn(dir, pattern string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			s.warnings = append(s.warnings, fmt.Sprintf(
				"Directory %q is not readable — its contents are missing from this inventory.", dir,
			))
		}

		return nil
	}

	var matched []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if ok, _ := filepath.Match(pattern, entry.Name()); ok {
			matched = append(matched, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(matched)

	return matched
}

// projects is every engine project tree under the projects root.
func (s *Scanner) projects() []Item {
	root := strings.TrimRight(s.projectsRoot, "/")

	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			s.warnings = append(s.warnings, fmt.Sprintf(
				"Projects root %q is not readable — no environments are reported from it.", root,
			))
		}

		return nil
	}

	var items []Item
	for _, entry := range entries {
		projectPath := filepath.Join(root, entry.Name())
		if !projectDir.MatchString(entry.Name()) || !isDirectory(projectPath) {
			continue
		}
		items = append(items, s.projectItems(entry.Name(), projectPath)...)
	}

	return items
}

// projectItems is one environment: its tree, its snapshots, and the module's
// own committed fixture dumps.
func (s *Scanner) projectItems(projectName, projectPath string) []Item {
	module, coreMajor, lastUsedAt := s.readEnvMeta(projectPath)

	snapshots := s.snapshots(projectName, projectPath, module, coreMajor)

	// The tree's size excludes its materialised snapshots, so the inventory
	// total never double-counts bytes.
	overhead := s.sizer(filepath.Join(projectPath, adapter.SnapshotMaterializedDir)) +
		s.sizer(filepath.Join(projectPath, adapter.SnapshotMetaDir))
	size := s.sizer(projectPath) - overhead
	if size < 0 {
		size = 0
	}

	items := []Item{{
		Path:        projectPath,
		Category:    ProjectTree,
		Size:        size,
		Module:      module,
		CoreMajor:   coreMajor,
		ProjectName: projectName,
		LastUsedAt:  lastUsedAt,
		KeepMarked:  isRegularFile(filepath.Join(projectPath, KeepMarker)),
	}}
	items = append(items, snapshots...)

	for _, dump := range s.globIn(filepath.Join(projectPath, moduleFixturesDir), "*.sql.gz") {
		items = append(items, Item{
			Path:        dump,
			Category:    FixtureDump,
			Size:        s.sizer(dump),
			Module:      module,
			CoreMajor:   coreMajor,
			ProjectName: projectName,
		})
	}

	return items
}

// snapshots is one environment's materialised fixture snapshots.
func (s *Scanner) snapshots(projectName, projectPath, module, coreMajor string) []Item {
	materialized := filepath.Join(projectPath, adapter.SnapshotMaterializedDir)

	// A directory listing resolves through symlinked components, so a
	// symlinked materialised directory would enumerate files outside the
	// environment — and prune deletes what the inventory reports. Nothing
	// outside the tree is ever this environment's snapshot store.
	if info, err := os.Lstat(materialized); err == nil && info.Mode()&os.ModeSymlink != 0 {
		s.warnings = append(s.warnings, fmt.Sprintf(
			"Snapshot directory %q is a symlink — skipped: only files inside the environment tree "+
				"are inventoried as its snapshots.",
			materialized,
		))

		return nil
	}

	var items []Item
	for _, artifact := range s.globIn(materialized, "*"+adapter.SnapshotArtifactSuffix) {
		name := strings.TrimSuffix(filepath.Base(artifact), adapter.SnapshotArtifactSuffix)

		lastUsedAt := s.snapshotMaterializedAt(projectPath, name)
		if lastUsedAt.IsZero() {
			lastUsedAt = modifiedAt(artifact)
		}

		items = append(items, Item{
			Path:        artifact,
			Category:    Snapshot,
			Size:        s.sizer(artifact),
			Module:      module,
			CoreMajor:   coreMajor,
			ProjectName: projectName,
			LastUsedAt:  lastUsedAt,
			KeepMarked:  isRegularFile(artifact + KeepMarker),
		})
	}

	return items
}

// readEnvMeta is the lenient read of the environment meta dotfile — see
// adapter.EnvMetaAttribution for why the strict one would be wrong here.
func (s *Scanner) readEnvMeta(projectPath string) (module, coreMajor string, lastUsedAt time.Time) {
	contents, err := os.ReadFile(adapter.EnvMetaPath(projectPath))
	if err != nil {
		return "", "", time.Time{}
	}

	return adapter.EnvMetaAttribution(string(contents))
}

// materializedAt reads the sidecar's timestamp line.
var materializedAt = regexp.MustCompile(`(?m)^materialized_at=(.+)$`)

// snapshotMaterializedAt is when the add-on recorded materialising a snapshot,
// or the zero time when it did not say.
func (s *Scanner) snapshotMaterializedAt(projectPath, name string) time.Time {
	metaPath := filepath.Join(
		projectPath, adapter.SnapshotMetaDir, name+adapter.SnapshotMetaSuffix,
	)

	contents, err := os.ReadFile(metaPath)
	if err != nil {
		return time.Time{}
	}

	match := materializedAt.FindSubmatch(contents)
	if match == nil {
		return time.Time{}
	}

	return parseTimestamp(strings.TrimSpace(string(match[1])))
}

// ItemsForVolumes turns the engine's volumes into inventory items, keeping
// only the ones belonging to a project tree this inventory found.
//
// Other projects' volumes are none of our business, and prune deletes what the
// inventory reports. A volume inherits its tree's attribution and age, because
// they are reclaimed together and a volume has no age of its own.
func ItemsForVolumes(inventory []Item, volumes []adapter.ProjectVolume) []Item {
	trees := map[string]Item{}
	for _, item := range inventory {
		if item.Category == ProjectTree && item.ProjectName != "" {
			trees[item.ProjectName] = item
		}
	}

	var items []Item
	for _, volume := range volumes {
		tree, known := trees[volume.ProjectName]
		if !known {
			continue
		}
		items = append(items, Item{
			Path:        volume.Name,
			Category:    ProjectVolume,
			Size:        volume.SizeBytes,
			Module:      tree.Module,
			CoreMajor:   tree.CoreMajor,
			ProjectName: volume.ProjectName,
			LastUsedAt:  tree.LastUsedAt,
		})
	}

	return items
}

// parseTimestamp reads the shapes a recorded time can arrive in.
func parseTimestamp(raw string) time.Time {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05-0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if at, err := time.Parse(layout, raw); err == nil {
			return at
		}
	}

	return time.Time{}
}

// modifiedAt is a file's modification time, or the zero time when it cannot be
// read.
func modifiedAt(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}

	return info.ModTime()
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

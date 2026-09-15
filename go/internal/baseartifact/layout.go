package baseartifact

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/owenbush/upkeep/internal/naming"
)

// The canonical on-disk layout of per-core-version base artifacts:
//
//	<cockpit>/base-artifacts/<core-major>/
//	    tree/                  resolved drupal/recommended-project base tree
//	    clean-install.sql.gz   gzipped module-free clean-install DB dump
//	    meta.yml               build metadata (exact core, PHP, DB, timestamp)
//	    canonical              marker: this artifact set is canonical and must
//	                           be excluded from pruning
const (
	TreeDir         = "tree"
	DumpFilename    = "clean-install.sql.gz"
	MetaFilename    = "meta.yml"
	CanonicalMarker = "canonical"
)

// Layout resolves paths inside the base-artifacts directory.
type Layout struct {
	Dir string
}

// NewLayout roots a layout at the base-artifacts directory.
func NewLayout(dir string) *Layout { return &Layout{Dir: dir} }

// VersionDir is one core's artifact set.
func (l *Layout) VersionDir(coreMajor string) (string, error) {
	if err := assertVersion(coreMajor); err != nil {
		return "", err
	}

	return filepath.Join(l.Dir, coreMajor), nil
}

// TreePath is the resolved base tree.
func (l *Layout) TreePath(coreMajor string) (string, error) {
	return l.under(coreMajor, TreeDir)
}

// DumpPath is the clean-install database dump.
func (l *Layout) DumpPath(coreMajor string) (string, error) {
	return l.under(coreMajor, DumpFilename)
}

// MetaPath is the build-metadata sidecar.
func (l *Layout) MetaPath(coreMajor string) (string, error) {
	return l.under(coreMajor, MetaFilename)
}

// CanonicalMarkerPath is the marker that excludes this set from pruning.
func (l *Layout) CanonicalMarkerPath(coreMajor string) (string, error) {
	return l.under(coreMajor, CanonicalMarker)
}

func (l *Layout) under(coreMajor, name string) (string, error) {
	dir, err := l.VersionDir(coreMajor)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, name), nil
}

// VersionsOnDisk is the core-major directories present, sorted numerically.
//
// An unreadable directory is NOT reported as an empty one: "no base artifacts"
// and "cannot tell what base artifacts there are" lead to different, and
// differently dangerous, decisions downstream.
//
// The match is on whole numbers, which is what makes a build in progress
// invisible here: a staging directory is named .building-d<major>-<hex>, so a
// half-built tree can never read as a core somebody can be offered.
func (l *Layout) VersionsOnDisk() ([]string, error) {
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}

		return nil, fmt.Errorf(
			"cannot list the base artifacts in %q — the directory is not readable: %w", l.Dir, err,
		)
	}

	versions := []string{}
	for _, entry := range entries {
		if !naming.IsCoreMajor(entry.Name()) {
			continue
		}
		// Stat rather than the directory entry's own type, so a symlinked
		// artifact set counts. These trees are gigabytes, and moving one to
		// another disk and linking it back is a reasonable thing to do; PHP's
		// is_dir follows the link, and DirEntry.IsDir does not.
		info, err := os.Stat(filepath.Join(l.Dir, entry.Name()))
		if err == nil && info.IsDir() {
			versions = append(versions, entry.Name())
		}
	}
	sort.Slice(versions, func(a, b int) bool {
		left, _ := strconv.Atoi(versions[a])
		right, _ := strconv.Atoi(versions[b])

		return left < right
	})

	return versions, nil
}

func assertVersion(coreMajor string) error {
	if !naming.IsCoreMajor(coreMajor) {
		return fmt.Errorf("core version must be a whole major version number, got %q", coreMajor)
	}

	return nil
}

// Paths is every path in one core's artifact set.
type Paths struct {
	VersionDir      string
	Tree            string
	Dump            string
	Meta            string
	CanonicalMarker string
}

// PathsFor resolves the whole set at once, validating the core major once.
//
// The individual accessors each validate, which means a caller that needs
// several of them gets several identical error branches that cannot all be
// reached — the first one already refused. One call, one branch, and the rest
// of the code reads as the sequence it is.
func (l *Layout) PathsFor(coreMajor string) (Paths, error) {
	versionDir, err := l.VersionDir(coreMajor)
	if err != nil {
		return Paths{}, err
	}

	return Paths{
		VersionDir:      versionDir,
		Tree:            filepath.Join(versionDir, TreeDir),
		Dump:            filepath.Join(versionDir, DumpFilename),
		Meta:            filepath.Join(versionDir, MetaFilename),
		CanonicalMarker: filepath.Join(versionDir, CanonicalMarker),
	}, nil
}

package baseartifact

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Record is the observed state of one per-core-version artifact set on disk.
type Record struct {
	Version   string
	Complete  bool
	Meta      *Meta
	TreeBytes int64
	DumpBytes int64
	// Missing names the pieces absent or unreadable; empty when complete.
	Missing []string
}

// Scanner reads the base-artifacts directory and reports, per core version,
// whether the artifact set is complete (tree, dump, parseable meta, canonical
// marker), its build metadata, and its on-disk sizes.
type Scanner struct {
	layout *Layout
}

// NewScanner reads through the given layout.
func NewScanner(layout *Layout) *Scanner { return &Scanner{layout: layout} }

// Scan reports every artifact set found.
func (s *Scanner) Scan() ([]Record, error) {
	versions, err := s.layout.VersionsOnDisk()
	if err != nil {
		return nil, err
	}

	records := make([]Record, 0, len(versions))
	for _, version := range versions {
		record, err := s.scanVersion(version)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, nil
}

func (s *Scanner) scanVersion(version string) (Record, error) {
	record := Record{Version: version, Missing: []string{}}

	treePath, err := s.layout.TreePath(version)
	if err != nil {
		return Record{}, err
	}
	treeIsDir := isDir(treePath)
	if !treeIsDir {
		record.Missing = append(record.Missing, TreeDir+"/")
	}

	dumpPath, err := s.layout.DumpPath(version)
	if err != nil {
		return Record{}, err
	}
	dumpInfo, dumpErr := os.Stat(dumpPath)
	if dumpErr != nil || !dumpInfo.Mode().IsRegular() {
		record.Missing = append(record.Missing, DumpFilename)
	}

	metaPath, err := s.layout.MetaPath(version)
	if err != nil {
		return Record{}, err
	}
	contents, metaErr := os.ReadFile(metaPath)
	switch {
	case metaErr != nil:
		record.Missing = append(record.Missing, MetaFilename)
	default:
		meta, parseErr := MetaFromYAML(string(contents))
		if parseErr != nil {
			record.Missing = append(record.Missing, MetaFilename+" (unreadable)")
		} else {
			record.Meta = &meta
		}
	}

	markerPath, err := s.layout.CanonicalMarkerPath(version)
	if err != nil {
		return Record{}, err
	}
	if !isFile(markerPath) {
		record.Missing = append(record.Missing, CanonicalMarker)
	}

	record.Complete = len(record.Missing) == 0
	if treeIsDir {
		record.TreeBytes = directorySize(treePath)
	}
	if dumpErr == nil && dumpInfo.Mode().IsRegular() {
		record.DumpBytes = dumpInfo.Size()
	}

	return record, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

// directorySize measures a tree.
//
// No symlink is followed — neither to a directory nor to a file — so the
// measure stays inside the artifact tree. WalkDir gives that for directories;
// the file half is this reading the entry's own size rather than its target's.
//
// This diverges from PHP deliberately. There, SplFileInfo::getSize follows a
// link, so every vendor/bin/* entry is counted a second time on top of the
// real file it points at, and a link out of the tree is counted as though it
// were in it — measured: a tree holding 1000 bytes and one link to a 5000-byte
// file outside reports 7000. The doc comment on the PHP side claims the
// measure stays inside the tree, and for symlinked files it does not.
//
// An unreadable subtree is an under-count rather than a failure: a size report
// is not worth failing `base-artifacts:status` over. A file that vanishes
// mid-walk contributes nothing for the same reason.
func directorySize(dir string) int64 {
	var bytes int64
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// Deliberately swallowed. An unreadable subtree makes this an
			// under-count, which is the documented failure mode; returning the
			// error would abort a size report over a directory nobody can
			// read anyway.
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		bytes += info.Size()

		return nil
	})

	return bytes
}

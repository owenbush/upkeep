package baseartifact

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// completeSet writes a whole artifact set for one core, and returns its
// directory.
func completeSet(t *testing.T, root, version string) string {
	t.Helper()

	dir := filepath.Join(root, version)
	tree := filepath.Join(dir, TreeDir, "web", "core")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tree, "core.api.php"), []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, DumpFilename), []byte("gzipped-dump"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	meta := Meta{
		CoreVersion: version + ".4.6", CoreMajor: version, PHPVersion: "8.3.14",
		DBEngine: "mariadb:10.11", BuiltAt: time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC),
	}
	rendered, err := meta.ToYAML()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, MetaFilename), []byte(rendered), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, CanonicalMarker), nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return dir
}

func TestACompleteSetIsReportedComplete(t *testing.T) {
	root := t.TempDir()
	completeSet(t, root, "11")

	records, err := NewScanner(NewLayout(root)).Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records", len(records))
	}

	record := records[0]
	if !record.Complete {
		t.Errorf("incomplete, missing %v", record.Missing)
	}
	if record.Meta == nil {
		t.Fatal("no meta read")
	}
	if record.Meta.CoreVersion != "11.4.6" {
		t.Errorf("core version %q", record.Meta.CoreVersion)
	}
	if record.TreeBytes != 10 {
		t.Errorf("tree size %d, want the file it holds", record.TreeBytes)
	}
	if record.DumpBytes != int64(len("gzipped-dump")) {
		t.Errorf("dump size %d", record.DumpBytes)
	}
}

// Every piece is named individually, because "incomplete" alone does not say
// what to do about it.
func TestEachMissingPieceIsNamed(t *testing.T) {
	root := t.TempDir()
	dir := completeSet(t, root, "11")

	for _, tc := range []struct {
		remove string
		want   string
	}{
		{TreeDir, TreeDir + "/"},
		{DumpFilename, DumpFilename},
		{MetaFilename, MetaFilename},
		{CanonicalMarker, CanonicalMarker},
	} {
		fresh := t.TempDir()
		dir = completeSet(t, fresh, "11")
		if err := os.RemoveAll(filepath.Join(dir, tc.remove)); err != nil {
			t.Fatalf("remove: %v", err)
		}

		records, err := NewScanner(NewLayout(fresh)).Scan()
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if records[0].Complete {
			t.Errorf("removing %s still read as complete", tc.remove)
		}
		if !slices.Contains(records[0].Missing, tc.want) {
			t.Errorf("removing %s: missing %v, want %q named", tc.remove, records[0].Missing, tc.want)
		}
	}
}

// A meta that is there but unreadable is a different problem from one that is
// absent, and the report says which.
func TestAnUnreadableMetaIsDistinguishedFromAMissingOne(t *testing.T) {
	root := t.TempDir()
	dir := completeSet(t, root, "11")
	if err := os.WriteFile(filepath.Join(dir, MetaFilename), []byte("not: [valid"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	records, err := NewScanner(NewLayout(root)).Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if records[0].Meta != nil {
		t.Error("a broken meta was read as metadata")
	}
	if !slices.Contains(records[0].Missing, MetaFilename+" (unreadable)") {
		t.Errorf("missing %v, want the unreadable meta named as such", records[0].Missing)
	}
	if slices.Contains(records[0].Missing, MetaFilename) {
		t.Errorf("missing %v reports it as absent as well", records[0].Missing)
	}
}

// A size report is not worth failing the command over, so an unreadable
// subtree is an under-count.
func TestAnUnreadableSubtreeUnderCountsRatherThanFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 directory regardless of its mode")
	}

	root := t.TempDir()
	dir := completeSet(t, root, "11")
	locked := filepath.Join(dir, TreeDir, "vendor")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(locked, "big"), make([]byte, 5000), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	records, err := NewScanner(NewLayout(root)).Scan()
	if err != nil {
		t.Fatalf("an unreadable subtree failed the scan: %v", err)
	}
	if records[0].TreeBytes != 10 {
		t.Errorf("tree size %d, want the readable part only", records[0].TreeBytes)
	}
}

// The measure stays inside the artifact tree: a symlink out of it must not be
// counted, or a link to / would try to measure the machine.
func TestTheMeasureDoesNotFollowSymlinksOutOfTheTree(t *testing.T) {
	root := t.TempDir()
	dir := completeSet(t, root, "11")

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "huge"), make([]byte, 9000), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, TreeDir, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	records, err := NewScanner(NewLayout(root)).Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if records[0].TreeBytes != 10 {
		t.Errorf("tree size %d — the measure followed a link out of the tree", records[0].TreeBytes)
	}
}

// A symlinked *file* is not counted as its target either, which is the half
// PHP gets wrong: there, a link out of the tree is counted as though it were
// in it, and every vendor/bin/* entry is counted twice on top of the real file
// it points at.
func TestASymlinkedFileIsCountedAsALinkNotAsItsTarget(t *testing.T) {
	root := t.TempDir()
	dir := completeSet(t, root, "11")

	outside := filepath.Join(t.TempDir(), "huge")
	if err := os.WriteFile(outside, make([]byte, 9000), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, TreeDir, "out")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// And one pointing back inside, the shape vendor/bin/* has.
	if err := os.Symlink(filepath.Join(dir, TreeDir, "web", "core", "core.api.php"),
		filepath.Join(dir, TreeDir, "in")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	records, err := NewScanner(NewLayout(root)).Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if records[0].TreeBytes >= 9000 {
		t.Errorf("tree size %d — a link out of the tree was counted as its target", records[0].TreeBytes)
	}
	if records[0].TreeBytes >= 20+int64(len(outside)) {
		t.Errorf("tree size %d — the in-tree link was counted as a second copy", records[0].TreeBytes)
	}
}

func TestSetsAreReportedInNumericOrder(t *testing.T) {
	root := t.TempDir()
	for _, version := range []string{"12", "9", "11", "10"} {
		completeSet(t, root, version)
	}

	records, err := NewScanner(NewLayout(root)).Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	versions := []string{}
	for _, record := range records {
		versions = append(versions, record.Version)
	}
	if !slices.Equal(versions, []string{"9", "10", "11", "12"}) {
		t.Errorf("got %v", versions)
	}
}

func TestAnEmptyDirectoryScansToNothing(t *testing.T) {
	records, err := NewScanner(NewLayout(t.TempDir())).Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("got %d records", len(records))
	}
}

// A version directory holding nothing at all is a set with everything missing,
// not an absent one — which is what a killed build leaves behind.
func TestAnEmptyVersionDirectoryIsAnIncompleteSet(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "11"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	records, err := NewScanner(NewLayout(root)).Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(records) != 1 || records[0].Complete {
		t.Fatalf("got %+v", records)
	}
	if len(records[0].Missing) != 4 {
		t.Errorf("missing %v, want all four pieces named", records[0].Missing)
	}
	if strings.Join(records[0].Missing, " ") == "" {
		t.Error("nothing was named")
	}
}

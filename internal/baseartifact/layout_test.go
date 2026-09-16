package baseartifact

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTheLayoutIsRootedAtTheVersionDirectory(t *testing.T) {
	layout := NewLayout("/cockpit/base-artifacts")

	paths := map[string]func(string) (string, error){
		"tree":   layout.TreePath,
		"dump":   layout.DumpPath,
		"meta":   layout.MetaPath,
		"marker": layout.CanonicalMarkerPath,
	}

	seen := map[string]string{}
	for name, path := range paths {
		got, err := path("11")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(got, "/cockpit/base-artifacts/11/") {
			t.Errorf("%s path %q is not under the version directory", name, got)
		}
		if other, clash := seen[got]; clash {
			t.Errorf("%s and %s both resolve to %q", name, other, got)
		}
		seen[got] = name
	}
}

// Every one of these becomes a path segment.
func TestAVersionThatIsNotAWholeMajorIsRefused(t *testing.T) {
	layout := NewLayout(t.TempDir())

	for _, version := range []string{"", "11.2", "../11", "11/tree", "eleven", "-11"} {
		if _, err := layout.VersionDir(version); err == nil {
			t.Errorf("%q was accepted as a core version", version)
		}
		if _, err := layout.TreePath(version); err == nil {
			t.Errorf("%q was accepted as a core version for a tree path", version)
		}
	}
}

func TestVersionsOnDiskAreSortedNumerically(t *testing.T) {
	root := t.TempDir()
	for _, version := range []string{"9", "10", "11", "12"} {
		if err := os.MkdirAll(filepath.Join(root, version), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	versions, err := NewLayout(root).VersionsOnDisk()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(versions, []string{"9", "10", "11", "12"}) {
		t.Errorf("got %v", versions)
	}
}

// The leading dot is load-bearing: a half-built tree must never read as a core
// somebody can be offered, so a build in progress is invisible to status, to
// prune, and to the core inference that gives an unregistered module its
// versions.
func TestABuildInProgressIsInvisible(t *testing.T) {
	root := t.TempDir()
	for _, entry := range []string{"11", ".building-d12-a1b2c3", "retired", "12.x", "notes.txt"} {
		if err := os.MkdirAll(filepath.Join(root, entry), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	versions, err := NewLayout(root).VersionsOnDisk()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(versions, []string{"11"}) {
		t.Errorf("got %v, want only the finished set", versions)
	}
}

// A file named like a version is not an artifact set.
func TestAFileNamedLikeAVersionIsNotCounted(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "11"), []byte("not a tree"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	versions, err := NewLayout(root).VersionsOnDisk()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("got %v", versions)
	}
}

// These trees are gigabytes, and moving one to another disk and linking it
// back is a reasonable thing to do.
func TestASymlinkedArtifactSetStillCounts(t *testing.T) {
	root := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "core-11")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(root, "11")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	versions, err := NewLayout(root).VersionsOnDisk()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(versions, []string{"11"}) {
		t.Errorf("got %v, want the symlinked set", versions)
	}
}

// A dangling link points at nothing, so there is no artifact set there.
func TestADanglingSymlinkIsNotAnArtifactSet(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(root, "11")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	versions, err := NewLayout(root).VersionsOnDisk()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("got %v", versions)
	}
}

func TestNoBaseArtifactsDirectoryIsAnEmptyListNotAFailure(t *testing.T) {
	versions, err := NewLayout(filepath.Join(t.TempDir(), "never-built")).VersionsOnDisk()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("got %v", versions)
	}
}

// "No base artifacts" and "cannot tell what base artifacts there are" lead to
// different, and differently dangerous, decisions downstream.
func TestAnUnreadableDirectoryIsSaidRatherThanReadAsEmpty(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 directory regardless of its mode")
	}

	root := filepath.Join(t.TempDir(), "base-artifacts")
	if err := os.MkdirAll(filepath.Join(root, "11"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	versions, err := NewLayout(root).VersionsOnDisk()
	if err == nil {
		t.Fatalf("an unreadable directory read as %v with no complaint", versions)
	}
	if !strings.Contains(err.Error(), "not readable") {
		t.Errorf("error %q does not say why", err)
	}
}

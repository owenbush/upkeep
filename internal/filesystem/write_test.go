package filesystem

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCreatesTheFileWithTheRequestedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.yml")

	if err := Write(path, []byte("modules: {}\n"), ModePrivate); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != ModePrivate {
		t.Errorf("mode = %o, want %o", got, ModePrivate)
	}
	if contents, _ := os.ReadFile(path); string(contents) != "modules: {}\n" {
		t.Errorf("contents = %q", contents)
	}
}

// The temporary file is a sibling, so it must not survive the write.
func TestWriteLeavesNoTemporarySiblingBehind(t *testing.T) {
	dir := t.TempDir()

	if err := Write(filepath.Join(dir, "meta.yml"), []byte("x"), ModeShared); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "meta.yml" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want only meta.yml", names)
	}
}

// The target is replaced in place and never stops existing: several files here
// double as completion markers, and a moment of absence reads as an
// interrupted provision.
func TestWriteReplacesTheTargetInPlaceRatherThanUnlinkingItFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marker")
	if err := os.WriteFile(path, []byte("old"), ModeShared); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)

	if err := Write(path, []byte("new"), KeepMode); err != nil {
		t.Fatalf("write: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the target stopped existing: %v", err)
	}
	if contents, _ := os.ReadFile(path); string(contents) != "new" {
		t.Errorf("contents = %q", contents)
	}
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Errorf("mode changed to %o without being asked", after.Mode().Perm())
	}
}

func TestWritePreservesTheExistingModeWhenNoModeIsRequested(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(path, []byte("{}"), ModePrivate); err != nil {
		t.Fatal(err)
	}

	if err := Write(path, []byte(`{"a":1}`), KeepMode); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, _ := os.Stat(path)
	if got := info.Mode().Perm(); got != ModePrivate {
		t.Errorf("mode = %o, want the 0600 it already had", got)
	}
}

func TestWriteFailsLoudlyWhenTheTargetDirectoryDoesNotExist(t *testing.T) {
	err := Write(filepath.Join(t.TempDir(), "nope", "file"), []byte("x"), ModeShared)

	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want a refusal naming the missing directory", err)
	}
}

func TestWriteFailsLoudlyWhenTheTargetPathIsADirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "occupied"), ModeSharedDir); err != nil {
		t.Fatal(err)
	}

	err := Write(filepath.Join(dir, "occupied"), []byte("x"), ModeShared)

	if err == nil || !strings.Contains(err.Error(), "a directory exists") {
		t.Fatalf("err = %v, want a refusal naming the directory", err)
	}
}

func TestWriteFailsLoudlyWhenTheDirectoryIsNotWritable(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, ModeSharedDir) })

	err := Write(filepath.Join(dir, "file"), []byte("x"), ModeShared)

	if err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("err = %v, want a refusal naming the directory", err)
	}
}

// The seam the package is not final for: a short write cannot be provoked on a
// working filesystem, and reporting success over one is the failure this
// package exists to prevent.
func TestAShortWriteIsReportedAndLeavesNeitherTargetNorTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "truncated")

	restore := writeBytes
	writeBytes = func(f *os.File, contents []byte) (int, error) { return f.Write(contents[:len(contents)/2]) }
	t.Cleanup(func() { writeBytes = restore })

	err := Write(path, []byte("the whole thing"), ModeShared)

	if err == nil || !strings.Contains(err.Error(), "wrote 7") {
		t.Fatalf("err = %v, want a report of how much landed", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
		t.Error("the target was created from a short write")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Error("a temporary file was left behind")
	}
}

func TestAModeThatCannotBeAppliedFailsTheWriteRatherThanPublishingAWorldReadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.json")

	restore := setMode
	setMode = func(string, fs.FileMode) error { return errors.New("refused") }
	t.Cleanup(func() { setMode = restore })

	err := Write(path, []byte("token"), ModePrivate)

	if err == nil || !strings.Contains(err.Error(), "cannot set mode") {
		t.Fatalf("err = %v, want the write refused", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
		t.Error("a file was published without the mode it asked for")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Error("a temporary file was left behind")
	}
}

func TestCommitFailsLoudlyAndDiscardsTheTempFileWhenTheRenameCannotHappen(t *testing.T) {
	dir := t.TempDir()
	temp, err := WriteTemporary(filepath.Join(dir, "target"), []byte("x"), ModeShared)
	if err != nil {
		t.Fatal(err)
	}

	// A directory at the target: rename(2) refuses to replace one with a file.
	if err := os.Mkdir(filepath.Join(dir, "target"), ModeSharedDir); err != nil {
		t.Fatal(err)
	}

	err = Commit(temp, filepath.Join(dir, "target"))

	if err == nil || !strings.Contains(err.Error(), "move the completed write") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if _, statErr := os.Stat(temp); !errors.Is(statErr, fs.ErrNotExist) {
		t.Error("the temporary file was left behind")
	}
}

func TestEnsureDirectoryCreatesTheTreeWithTheRequestedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "c")

	if err := EnsureDirectory(path, ModePrivateDir); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != ModePrivateDir {
		t.Errorf("mode = %o, want %o", got, ModePrivateDir)
	}
}

func TestEnsureDirectoryIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twice")

	if err := EnsureDirectory(path, ModeSharedDir); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := EnsureDirectory(path, ModeSharedDir); err != nil {
		t.Fatalf("second: %v", err)
	}
}

func TestEnsureDirectoryFailsLoudlyWhenThePathIsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), ModeShared); err != nil {
		t.Fatal(err)
	}

	err := EnsureDirectory(path, ModeSharedDir)

	if err == nil || !strings.Contains(err.Error(), "could not create directory") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

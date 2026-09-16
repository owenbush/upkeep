// Package filesystem is the single write path for every file upkeep generates.
package filesystem

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Modes are explicit because the umask default is wrong for the caches that
// carry token-scoped remote data and raw check output.
const (
	// ModePrivate is owner-only, for anything holding remote data fetched with
	// a credential, or raw check output.
	ModePrivate    fs.FileMode = 0o600
	ModePrivateDir fs.FileMode = 0o700

	// ModeShared is ordinary cockpit content: registries, markers, metadata.
	ModeShared    fs.FileMode = 0o644
	ModeSharedDir fs.FileMode = 0o755

	// KeepMode preserves the mode a file already has, and is ModeShared when
	// the file is new.
	KeepMode fs.FileMode = 0
)

// Seams, so the failures this package exists to detect — a short write, a
// refused chmod — can be simulated. They cannot be provoked on a working
// filesystem, and an untested error path is exactly the kind that reports
// success over a write that never landed.
var (
	writeBytes = func(f *os.File, contents []byte) (int, error) { return f.Write(contents) }
	setMode    = os.Chmod
)

// Write puts contents at path atomically.
//
// The bytes land in a temporary file in the *same* directory and are then
// renamed over the target. rename(2) is atomic within a filesystem, so a
// concurrent reader sees either the whole old file or the whole new one, never
// a truncated one — and the target never stops existing, which matters for the
// files that double as completion markers.
//
// Every step is checked. A write that does not land returns an error naming
// the path, rather than a discarded false while the caller reports success.
func Write(path string, contents []byte, mode fs.FileMode) error {
	temp, err := WriteTemporary(path, contents, mode)
	if err != nil {
		return err
	}

	return Commit(temp, path)
}

// WriteTemporary writes contents to a sibling temporary file of path and
// returns that path, without touching path itself.
//
// For callers that must inspect the exact final bytes before they become the
// real file. Finish with Commit, or remove the temporary file on rejection.
func WriteTemporary(path string, contents []byte, mode fs.FileMode) (string, error) {
	dir := filepath.Dir(path)

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("cannot write %q: its directory %q does not exist", path, dir)
	}
	if target, err := os.Stat(path); err == nil && target.IsDir() {
		return "", fmt.Errorf("cannot write %q: a directory exists at that path", path)
	}

	effective, err := effectiveMode(path, mode)
	if err != nil {
		return "", err
	}

	// Created in the target's own directory: a temporary file anywhere else
	// could not be renamed onto the target atomically.
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return "", fmt.Errorf(
			"cannot create a temporary file next to %q — the directory %q is not writable: %w", path, dir, err,
		)
	}

	written, writeErr := writeBytes(temp, contents)
	closeErr := temp.Close()
	if writeErr != nil || written != len(contents) {
		os.Remove(temp.Name())

		return "", fmt.Errorf("failed to write %d byte(s) to %q (wrote %d)", len(contents), path, written)
	}
	if closeErr != nil {
		os.Remove(temp.Name())

		return "", fmt.Errorf("failed to complete the write to %q: %w", path, closeErr)
	}

	if err := setMode(temp.Name(), effective); err != nil {
		os.Remove(temp.Name())

		return "", fmt.Errorf("cannot set mode %o on %q: %w", effective, path, err)
	}

	return temp.Name(), nil
}

// Commit renames a temporary file produced by WriteTemporary over its target.
func Commit(temp, path string) error {
	if err := os.Rename(temp, path); err != nil {
		os.Remove(temp)

		return fmt.Errorf("failed to move the completed write into place at %q: %w", path, err)
	}

	return nil
}

// EnsureDirectory creates a directory tree if it is not already there,
// tolerating a concurrent creator.
func EnsureDirectory(path string, mode fs.FileMode) error {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil
	}

	if err := os.MkdirAll(path, mode); err != nil {
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			return nil
		}

		return fmt.Errorf("could not create directory %q: %w", path, err)
	}

	return nil
}

func effectiveMode(path string, mode fs.FileMode) (fs.FileMode, error) {
	if mode != KeepMode {
		return mode, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return ModeShared, nil // new file
	}

	return info.Mode().Perm(), nil
}

// Package invariant holds checks over the source itself, for properties no
// single code path can demonstrate.
package invariant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every child process upkeep starts must go through internal/proc.
//
// That package is where the credential is stripped from the environment and
// where output is redacted on its way out. A child started anywhere else
// inherits the token and prints unredacted, and the defence is only complete
// if it holds at every construction site — so this asserts the property over
// the source rather than over one code path.
//
// The PHP has the same test for `new Process(`. This is its port, and it is
// stricter: there, one file was allowed to forward the credential deliberately
// and the exemption list is now empty. Here there are none from the start.
func TestOnlyTheProcessPackageStartsChildProcesses(t *testing.T) {
	root := ".."

	var offenders []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// internal/proc is the seam itself, and tests everywhere run helper
		// commands to set a scene.
		if strings.Contains(filepath.ToSlash(path), "/proc/") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range []string{"exec.Command(", "exec.CommandContext(", "os.StartProcess("} {
			if strings.Contains(string(source), forbidden) {
				offenders = append(offenders, path+": "+forbidden)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking the source: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf(
			"these start a child outside internal/proc, so it would inherit the credential "+
				"and print unredacted:\n  %s",
			strings.Join(offenders, "\n  "),
		)
	}
}

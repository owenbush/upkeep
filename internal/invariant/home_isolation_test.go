package invariant_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A test that redirects HOME must redirect XDG_CONFIG_HOME with it.
//
// `gitlab.DefaultConfigFile()` prefers XDG_CONFIG_HOME and falls back to
// $HOME/.config, so setting HOME alone does not move the token file. On a
// machine with XDG_CONFIG_HOME set — every GitHub runner — a test that
// isolates only HOME reads the *developer's* config instead of its own.
//
// It cost six red CI runs to find. `TestAWorldReadableTokenFileIsWarnedAbout`
// wrote a world-readable token under its temporary HOME and asserted a warning;
// on CI the resolver looked somewhere else entirely, found nothing, warned
// nothing, and the test failed — while passing on every machine where
// XDG_CONFIG_HOME happens to be unset.
//
// The worse half is what it means when the file *is* found: a suite that reads
// a real `~/.config/upkeep/drupal-pat` is a suite whose behaviour depends on
// whether the person running it has a token. That is the same class of leak as
// a test making a live API call, which this port has already had once.
//
// The PHP harness gets this right and says why — `CliHarness::isolateEnvironment`
// sets both, noting that "$XDG_CONFIG_HOME covers the token file". The port
// dropped that half. Asserted over the source because it is a property of every
// test file, present and future, rather than of any one of them.
func TestATestThatIsolatesHomeAlsoIsolatesTheConfigDirectory(t *testing.T) {
	const (
		home = `Setenv("HOME"`
		xdg  = `Setenv("XDG_CONFIG_HOME"`
	)

	var offenders []string
	err := filepath.WalkDir("..", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}

		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(source), home) && !strings.Contains(string(source), xdg) {
			offenders = append(offenders, path)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf(
			"these tests redirect HOME without redirecting XDG_CONFIG_HOME, so they read "+
				"the config of whoever runs them:\n  %s\nSet both, or neither.",
			strings.Join(offenders, "\n  "),
		)
	}
}

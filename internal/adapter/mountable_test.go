package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// homeAt points $HOME at a real directory for the duration of a test.
func homeAt(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	// Canonical, because macOS resolves /var to /private/var and the
	// containment comparison is on canonical paths.
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	t.Setenv("HOME", real)

	return real
}

// A functional requirement first and a hardening measure second: macOS Docker
// providers share only the home directory, so a tree created outside it can
// never start.
func TestAPathOutsideHomeIsRefusedWithTheReasonAndTheFix(t *testing.T) {
	home := homeAt(t)
	outside := t.TempDir()

	_, err := RequireUnderHome(outside, "projects root", "--projects-root")
	if err == nil {
		t.Fatal("a path outside $HOME was accepted")
	}

	for _, want := range []string{"outside your home directory", "bind-mounted", "can never start", "--projects-root"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
	if !strings.Contains(err.Error(), home) {
		t.Errorf("error %q does not name the home directory it wants", err)
	}
}

// Containment is decided on the canonicalised path, so neither a ".."
// sequence nor a symlink pointing out of $HOME satisfies it.
func TestNeitherATraversalNorASymlinkEscapesHome(t *testing.T) {
	home := homeAt(t)
	outside := t.TempDir()

	traversal := filepath.Join(home, "..", filepath.Base(outside))
	if _, err := RequireUnderHome(traversal, "projects root", "--projects-root"); err == nil {
		t.Errorf("%q was accepted", traversal)
	}

	link := filepath.Join(home, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := RequireUnderHome(link, "projects root", "--projects-root"); err == nil {
		t.Errorf("a symlink out of $HOME was accepted")
	}
	// And so is a path *through* the symlink.
	if _, err := RequireUnderHome(filepath.Join(link, "projects"), "projects root", "-p"); err == nil {
		t.Error("a path through a symlink out of $HOME was accepted")
	}
}

// There is deliberately no escape hatch: the rule applies to the explicit flag
// and the environment variable exactly as it applies to the default.
func TestTheRuleAppliesToTheFlagAndTheEnvironmentToo(t *testing.T) {
	homeAt(t)
	outside := t.TempDir()

	if _, err := ResolveProjectsRoot(outside, ""); err == nil {
		t.Error("--projects-root escaped the rule")
	}

	t.Setenv(ProjectsRootEnvVar, outside)
	if _, err := ResolveProjectsRoot("", ""); err == nil {
		t.Error("the environment variable escaped the rule")
	}
}

// A path that does not exist yet is fine — the projects root is created on
// first use.
func TestAPathUnderHomeIsAcceptedWhetherOrNotItExists(t *testing.T) {
	home := homeAt(t)

	for _, candidate := range []string{
		home,
		filepath.Join(home, "existing"),
		filepath.Join(home, "not", "made", "yet"),
	} {
		if strings.HasSuffix(candidate, "existing") {
			if err := os.MkdirAll(candidate, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}
		got, err := RequireUnderHome(candidate, "projects root", "--projects-root")
		if err != nil {
			t.Errorf("%q: %v", candidate, err)

			continue
		}
		if !strings.HasPrefix(got, home) {
			t.Errorf("%q resolved to %q, outside the home it came from", candidate, got)
		}
	}
}

// The flag outranks the environment, which outranks the cockpit's own
// projects/ directory, which outranks the default.
func TestTheResolutionOrderIsFlagEnvironmentCockpitDefault(t *testing.T) {
	home := homeAt(t)

	flagged := filepath.Join(home, "flagged")
	fromEnv := filepath.Join(home, "from-env")
	cockpit := filepath.Join(home, "cockpit")
	cockpitProjects := filepath.Join(cockpit, "projects")
	if err := os.MkdirAll(cockpitProjects, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	t.Setenv(ProjectsRootEnvVar, fromEnv)

	got, err := ResolveProjectsRoot(flagged, cockpit)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != flagged {
		t.Errorf("got %q, want the flag's", got)
	}

	got, err = ResolveProjectsRoot("", cockpit)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != fromEnv {
		t.Errorf("got %q, want the environment's", got)
	}

	t.Setenv(ProjectsRootEnvVar, "")
	got, err = ResolveProjectsRoot("", cockpit)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != cockpitProjects {
		t.Errorf("got %q, want the cockpit's", got)
	}

	got, err = ResolveProjectsRoot("", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != filepath.Join(home, ".upkeep", "projects") {
		t.Errorf("got %q, want the default", got)
	}
}

// A cockpit with no projects/ directory of its own falls through to the
// default rather than naming one that is not there.
func TestACockpitWithoutAProjectsDirectoryFallsThrough(t *testing.T) {
	home := homeAt(t)
	cockpit := filepath.Join(home, "cockpit")
	if err := os.MkdirAll(cockpit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv(ProjectsRootEnvVar, "")

	got, err := ResolveProjectsRoot("", cockpit)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != filepath.Join(home, ".upkeep", "projects") {
		t.Errorf("got %q, want the default", got)
	}
}

// A file where projects/ should be is not a projects directory.
func TestAFileNamedProjectsIsNotTheCockpitsProjectsRoot(t *testing.T) {
	home := homeAt(t)
	cockpit := filepath.Join(home, "cockpit")
	if err := os.MkdirAll(cockpit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cockpit, "projects"), nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(ProjectsRootEnvVar, "")

	got, err := ResolveProjectsRoot("", cockpit)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != filepath.Join(home, ".upkeep", "projects") {
		t.Errorf("got %q, want the default", got)
	}
}

// Refusing to fall back to a temp dir: Docker providers only mount the home
// directory, so anything created outside it can never start.
func TestWithNoHomeThereIsNoDefaultAndNothingToVerifyAgainst(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv(ProjectsRootEnvVar, "")

	_, err := ResolveProjectsRoot("", "")
	if err == nil {
		t.Fatal("a projects root was resolved with no $HOME")
	}
	for _, want := range []string{"$HOME is not set", "Refusing to fall back to a temp dir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}

	// And an explicit path is refused too, because there is nothing to check
	// it against.
	if _, err := ResolveProjectsRoot("/tmp/anywhere", ""); err == nil {
		t.Error("an explicit path was accepted with no $HOME to verify it against")
	}
}

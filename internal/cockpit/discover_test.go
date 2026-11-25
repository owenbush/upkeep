package cockpit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scaffoldCockpit makes root a cockpit (a registry file) with a working copy
// nested under its projects directory, and returns that working copy.
func scaffoldCockpit(t *testing.T, root string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, RegistryFilename), []byte("modules: {}\n"), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	nested := filepath.Join(root, ProjectsDir, "token-d11", "web", "modules", "contrib", "token")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	return nested
}

func canonicalRoot(t *testing.T, root string) string {
	t.Helper()
	want, err := New(root)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	return want.Root
}

// Issue #77: a command run from inside a module's working copy finds the
// cockpit that working copy lives under, the way git finds .git.
func TestTheWorkingDirectoryFallbackWalksUpToTheCockpit(t *testing.T) {
	t.Setenv(EnvVar, "")
	root := t.TempDir()
	t.Chdir(scaffoldCockpit(t, root))

	cockpit, err := Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := canonicalRoot(t, root); cockpit.Root != want {
		t.Errorf("got %q, want the cockpit above the working copy %q", cockpit.Root, want)
	}
	if _, err := cockpit.LoadRegistry(); err != nil {
		t.Errorf("the found cockpit's registry did not load: %v", err)
	}
}

// The nearest registry wins, so a cockpit nested inside another is its own.
func TestTheNearestCockpitWins(t *testing.T) {
	t.Setenv(EnvVar, "")
	outer := t.TempDir()
	scaffoldCockpit(t, outer)
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(scaffoldCockpit(t, inner))

	cockpit, err := Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := canonicalRoot(t, inner); cockpit.Root != want {
		t.Errorf("got %q, want the nearer cockpit %q", cockpit.Root, want)
	}
}

// A directory named registry.yml is not a cockpit, so the walk passes it by.
func TestADirectoryNamedLikeTheRegistryIsWalkedPast(t *testing.T) {
	t.Setenv(EnvVar, "")
	root := t.TempDir()
	nested := scaffoldCockpit(t, root)
	if err := os.Mkdir(filepath.Join(root, ProjectsDir, RegistryFilename), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(nested)

	cockpit, err := Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := canonicalRoot(t, root); cockpit.Root != want {
		t.Errorf("got %q, want %q past the directory named %s", cockpit.Root, want, RegistryFilename)
	}
}

// A projects directory linked in from elsewhere is still under its cockpit:
// the spelling the shell gave is walked, not only the canonical one.
func TestTheWalkFollowsTheSymlinkedSpelling(t *testing.T) {
	t.Setenv(EnvVar, "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, RegistryFilename), []byte("modules: {}\n"), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	elsewhere := filepath.Join(t.TempDir(), "projects")
	if err := os.MkdirAll(filepath.Join(elsewhere, "token-d11"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(root, ProjectsDir)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Chdir(filepath.Join(root, ProjectsDir, "token-d11"))

	cockpit, err := Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := canonicalRoot(t, root); cockpit.Root != want {
		t.Errorf("got %q, want the cockpit the link lives in %q", cockpit.Root, want)
	}
}

// And the canonical spelling is walked too, for a working directory reached
// through a link that sits outside any cockpit.
func TestTheWalkFallsBackToTheCanonicalSpelling(t *testing.T) {
	t.Setenv(EnvVar, "")
	root := t.TempDir()
	nested := scaffoldCockpit(t, root)
	link := filepath.Join(t.TempDir(), "shortcut")
	if err := os.Symlink(nested, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Chdir(link)

	cockpit, err := Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := canonicalRoot(t, root); cockpit.Root != want {
		t.Errorf("got %q, want the cockpit behind the link %q", cockpit.Root, want)
	}
}

// Nothing found is not a resolution failure — the working directory stands —
// but loading its registry says every parent was searched and names the ways
// out, so the exit 2 is actionable.
func TestAWalkThatFindsNothingNamesTheWaysOut(t *testing.T) {
	t.Setenv(EnvVar, "")
	cwd := t.TempDir()
	t.Chdir(cwd)

	cockpit, err := Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := canonicalRoot(t, cwd); cockpit.Root != want {
		t.Errorf("got %q, want the working directory %q", cockpit.Root, want)
	}

	_, err = cockpit.LoadRegistry()
	if err == nil {
		t.Fatal("a cockpit with no registry anywhere above loaded one")
	}
	for _, want := range []string{"any directory above", "--cockpit", "$" + EnvVar, "upkeep init", cockpit.Root} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

// A named cockpit is taken as named: neither the flag nor the environment
// walks, so pointing at the wrong directory is reported, never guessed past.
func TestANamedCockpitIsNotWalkedUpFrom(t *testing.T) {
	root := t.TempDir()
	nested := scaffoldCockpit(t, root)
	t.Setenv(EnvVar, nested)

	for name, option := range map[string]*string{"flag": &nested, "environment": nil} {
		cockpit, err := Resolve(option)
		if err != nil {
			t.Fatalf("%s: resolve: %v", name, err)
		}
		if want := canonicalRoot(t, nested); cockpit.Root != want {
			t.Errorf("%s: got %q, want exactly the named %q", name, cockpit.Root, want)
		}
		_, err = cockpit.LoadRegistry()
		if err == nil || !strings.Contains(err.Error(), "module registry not found") {
			t.Errorf("%s: a named cockpit without a registry reported %v", name, err)
		}
	}
}

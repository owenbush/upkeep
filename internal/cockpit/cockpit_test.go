package cockpit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two spellings of one cockpit must compare equal, which the prune surface's
// protected-root check depends on.
func TestTheRootIsCanonicalisedOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	direct, err := New(root)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	roundabout, err := New(filepath.Join(root, "sub", ".."))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if direct.Root != roundabout.Root {
		t.Errorf("%q and %q are the same directory spelled two ways", direct.Root, roundabout.Root)
	}
	if strings.Contains(direct.Root, "..") {
		t.Errorf("a traversal sequence survived into the root: %q", direct.Root)
	}
}

func TestATrailingSlashDoesNotSurvive(t *testing.T) {
	cockpit, err := New(t.TempDir() + "/")
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if strings.HasSuffix(cockpit.Root, "/") {
		t.Errorf("root %q", cockpit.Root)
	}
	// And every derived path is still a single-slash join.
	if strings.Contains(cockpit.RegistryPath(), "//") {
		t.Errorf("registry path %q", cockpit.RegistryPath())
	}
}

// The flag outranks the environment, which outranks the working directory.
func TestResolutionPrefersTheFlagThenTheEnvironment(t *testing.T) {
	flagged := t.TempDir()
	fromEnv := t.TempDir()
	t.Setenv(EnvVar, fromEnv)

	cockpit, err := Resolve(&flagged)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.HasSuffix(cockpit.Root, filepath.Base(flagged)) {
		t.Errorf("root %q, want the flag's", cockpit.Root)
	}

	cockpit, err = Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.HasSuffix(cockpit.Root, filepath.Base(fromEnv)) {
		t.Errorf("root %q, want the environment's", cockpit.Root)
	}
}

func TestAnUnsetEnvironmentFallsBackToTheWorkingDirectory(t *testing.T) {
	t.Setenv(EnvVar, "")

	cockpit, err := Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	want, err := New(cwd)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if cockpit.Root != want.Root {
		t.Errorf("got %q, want %q", cockpit.Root, want.Root)
	}
}

// An empty --cockpit is a mistake, not a request for the default: silently
// falling back would point the run at whatever directory it happened to be in.
func TestAnEmptyFlagIsRefusedRatherThanDefaulted(t *testing.T) {
	empty := ""

	_, err := Resolve(&empty)
	if err == nil {
		t.Fatal("an empty --cockpit resolved to something")
	}
	if !strings.Contains(err.Error(), EnvVar) {
		t.Errorf("error %q does not name the alternatives", err)
	}
}

// Every path the tool writes to hangs off the root, and none of them may be
// spelled twice.
func TestTheDerivedPathsAreAllUnderTheRoot(t *testing.T) {
	cockpit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	paths := map[string]string{
		"registry":        cockpit.RegistryPath(),
		"base artifacts":  cockpit.BaseArtifactsPath(),
		"fixtures":        cockpit.FixturesPath(),
		"projects":        cockpit.ProjectsPath(),
		"results":         cockpit.ResultsPath(),
		"dashboard cache": cockpit.DashboardCachePath(),
		"patch cache":     cockpit.PatchCachePath(),
	}

	seen := map[string]string{}
	for name, path := range paths {
		if !strings.HasPrefix(path, cockpit.Root+"/") {
			t.Errorf("%s path %q is not under the root", name, path)
		}
		if other, clash := seen[path]; clash {
			t.Errorf("%s and %s both resolve to %q", name, other, path)
		}
		seen[path] = name
	}
}

func TestTheCachesAreNestedUnderOneDirectory(t *testing.T) {
	cockpit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	for _, path := range []string{cockpit.DashboardCachePath(), cockpit.PatchCachePath()} {
		if !strings.HasPrefix(path, filepath.Join(cockpit.Root, "cache")+"/") {
			t.Errorf("%q is not under the cache directory", path)
		}
	}
}

func TestLoadRegistryReadsTheCockpitsOwnFile(t *testing.T) {
	root := t.TempDir()
	contents := "modules:\n  pathauto:\n    project: project/pathauto\n    core_versions: ['11']\n"
	if err := os.WriteFile(filepath.Join(root, RegistryFilename), []byte(contents), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cockpit, err := New(root)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	registry, err := cockpit.LoadRegistry()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, found := registry.Find("pathauto"); !found {
		t.Error("the cockpit's own registry was not read")
	}
}

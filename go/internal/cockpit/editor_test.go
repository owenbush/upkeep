package cockpit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func registryHolding(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "registry.yml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return path
}

func TestAddingWritesAModuleTheLoaderReadsBack(t *testing.T) {
	path := registryHolding(t, "modules:\n")

	added, err := NewEditor(path).Add([]Module{
		{Name: "pathauto", Project: "project/pathauto", CoreVersions: []string{"10", "11"}},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !slices.Equal(added, []string{"pathauto"}) {
		t.Errorf("added %v", added)
	}

	registry, err := RegistryFromFile(path)
	if err != nil {
		t.Fatalf("the registry it wrote does not load: %v", err)
	}
	module, found := registry.Find("pathauto")
	if !found {
		t.Fatal("pathauto is not in the registry it wrote")
	}
	if !slices.Equal(module.CoreVersions, []string{"10", "11"}) {
		t.Errorf("cores %v", module.CoreVersions)
	}
}

// Existing definitions always win: an add is not an edit.
func TestAnAlreadyRegisteredModuleIsLeftAlone(t *testing.T) {
	path := registryHolding(t,
		"modules:\n  pathauto:\n    project: someone/fork-of-pathauto\n    core_versions: ['10']\n")

	added, err := NewEditor(path).Add([]Module{
		{Name: "pathauto", Project: "project/pathauto", CoreVersions: []string{"11", "12"}},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added) != 0 {
		t.Errorf("added %v, want nothing", added)
	}

	registry, err := RegistryFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	module, _ := registry.Find("pathauto")
	if module.Project != "someone/fork-of-pathauto" {
		t.Errorf("project %q — the existing definition was overwritten", module.Project)
	}
	if !slices.Equal(module.CoreVersions, []string{"10"}) {
		t.Errorf("cores %v — the existing definition was overwritten", module.CoreVersions)
	}
}

// Nothing to add is nothing to write: the file must not be rewritten for it.
func TestAddingNothingLeavesTheFileUntouched(t *testing.T) {
	original := "# hand-written\nmodules:\n  pathauto:\n    project: project/pathauto\n    core_versions: ['11']\n"
	path := registryHolding(t, original)

	added, err := NewEditor(path).Add([]Module{
		{Name: "pathauto", Project: "project/pathauto", CoreVersions: []string{"11"}},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added) != 0 {
		t.Errorf("added %v", added)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != original {
		t.Errorf("the file was rewritten for an add that added nothing:\n%s", after)
	}
}

// Existing entries keep their positions and new ones are appended, because the
// registry is a file a person reads.
func TestExistingEntriesKeepTheirOrderAndNewOnesAreAppended(t *testing.T) {
	path := registryHolding(t,
		"modules:\n  token:\n    project: project/token\n    core_versions: ['11']\n"+
			"  pathauto:\n    project: project/pathauto\n    core_versions: ['11']\n")

	if _, err := NewEditor(path).Add([]Module{
		{Name: "webform", Project: "project/webform", CoreVersions: []string{"11"}},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	registry, err := RegistryFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if names := registry.Names(); !slices.Equal(names, []string{"token", "pathauto", "webform"}) {
		t.Errorf("order %v", names)
	}
}

// Validation stops a semantically bad entry before it reaches the file the
// whole tool trusts.
func TestABadEntryNeverReachesTheRegistry(t *testing.T) {
	original := "modules:\n  pathauto:\n    project: project/pathauto\n    core_versions: ['11']\n"
	path := registryHolding(t, original)

	_, err := NewEditor(path).Add([]Module{
		{Name: "webform", Project: "project/webform", CoreVersions: []string{"11.2"}},
	})
	if err == nil {
		t.Fatal("a non-major core version was written into the registry")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != original {
		t.Errorf("the registry was changed by a rejected add:\n%s", after)
	}
}

// The complaint has to name the registry the user asked to change, not the
// temporary file they have never heard of.
func TestARejectionNamesTheRealRegistry(t *testing.T) {
	path := registryHolding(t, "modules:\n")

	_, err := NewEditor(path).Add([]Module{
		{Name: "webform", Project: "project/webform", CoreVersions: []string{"../11"}},
	})
	if err == nil {
		t.Fatal("a traversal sequence was accepted as a core version")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the registry", err)
	}
	if strings.Contains(err.Error(), ".tmp") || strings.Contains(err.Error(), path+".") {
		t.Errorf("error %q names the temporary file", err)
	}
}

// Anything that stops the temporary file becoming the registry must also stop
// it being left beside the registry as debris.
func TestARejectedAddLeavesNoDebris(t *testing.T) {
	path := registryHolding(t, "modules:\n")
	dir := filepath.Dir(path)

	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	if _, err := NewEditor(path).Add([]Module{
		{Name: "webform", Project: "project/webform", CoreVersions: []string{"nope"}},
	}); err == nil {
		t.Fatal("the bad entry was accepted")
	}

	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(after) != len(before) {
		names := []string{}
		for _, entry := range after {
			names = append(names, entry.Name())
		}
		t.Errorf("left %v behind, was %d files", names, len(before))
	}
}

// An add against a registry that does not load is refused rather than being
// allowed to replace it.
func TestAddingToAnUnreadableRegistryRefuses(t *testing.T) {
	path := registryHolding(t, "modules:\n  PathAuto:\n    project: x\n    core_versions: ['11']\n")

	if _, err := NewEditor(path).Add([]Module{
		{Name: "webform", Project: "project/webform", CoreVersions: []string{"11"}},
	}); err == nil {
		t.Fatal("a broken registry was quietly replaced")
	}
}

// Two modules in one call, both new.
func TestSeveralModulesAreAddedInOneWrite(t *testing.T) {
	path := registryHolding(t, "modules:\n")

	added, err := NewEditor(path).Add([]Module{
		{Name: "pathauto", Project: "project/pathauto", CoreVersions: []string{"11"}},
		{Name: "token", Project: "project/token", CoreVersions: []string{"11"}},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !slices.Equal(added, []string{"pathauto", "token"}) {
		t.Errorf("added %v", added)
	}
}

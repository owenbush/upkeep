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

// twoModules is a registry with an entry either side of the one being changed,
// so the edit's effect on its neighbours is observable.
const twoModules = "modules:\n" +
	"  jumplinks:\n    project: project/jumplinks\n    core_versions: [\"11\"]\n" +
	"  pathauto:\n    project: project/pathauto\n    core_versions: [\"10\", \"11\"]\n"

func TestSettingCoreVersionsWritesWhatTheLoaderReadsBack(t *testing.T) {
	path := registryHolding(t, twoModules)

	updated, err := NewEditor(path).SetCoreVersions("jumplinks", []string{"11", "12"})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !slices.Equal(updated.CoreVersions, []string{"11", "12"}) {
		t.Errorf("returned %v", updated.CoreVersions)
	}

	registry, err := RegistryFromFile(path)
	if err != nil {
		t.Fatalf("the registry it wrote does not load: %v", err)
	}
	module, _ := registry.Find("jumplinks")
	if !slices.Equal(module.CoreVersions, []string{"11", "12"}) {
		t.Errorf("cores %v", module.CoreVersions)
	}
	// Its project is untouched: only the core list was named.
	if module.Project != "project/jumplinks" {
		t.Errorf("project %q", module.Project)
	}
}

// The order given is the order written, never sorted: core_versions[0] is the
// core a command targets when --version is omitted, so sorting would retarget
// every check of the module without saying so.
func TestSettingCoreVersionsKeepsTheOrderGiven(t *testing.T) {
	path := registryHolding(t, twoModules)

	if _, err := NewEditor(path).SetCoreVersions("pathauto", []string{"12", "10"}); err != nil {
		t.Fatalf("set: %v", err)
	}

	registry, _ := RegistryFromFile(path)
	module, _ := registry.Find("pathauto")
	if !slices.Equal(module.CoreVersions, []string{"12", "10"}) {
		t.Errorf("cores %v, want [12 10]", module.CoreVersions)
	}
}

// Every other entry keeps its definition and its place in the file, because
// the registry is a file somebody reads and an edit it did not ask for is
// noise in a diff.
func TestSettingCoreVersionsLeavesTheRestOfTheFileAlone(t *testing.T) {
	path := registryHolding(t, twoModules)

	if _, err := NewEditor(path).SetCoreVersions("pathauto", []string{"11"}); err != nil {
		t.Fatalf("set: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Index(string(written), "jumplinks") > strings.Index(string(written), "pathauto") {
		t.Errorf("the entries were reordered:\n%s", written)
	}

	registry, _ := RegistryFromFile(path)
	other, found := registry.Find("jumplinks")
	if !found {
		t.Fatalf("jumplinks was dropped:\n%s", written)
	}
	if !slices.Equal(other.CoreVersions, []string{"11"}) || other.Project != "project/jumplinks" {
		t.Errorf("jumplinks changed: %+v", other)
	}
}

// An unregistered name is a refusal, not an insert. Inventing an entry would
// hand every survey command a project path nobody chose; modules:add is where
// a new entry comes from.
func TestSettingCoreVersionsRefusesAModuleWithNoEntry(t *testing.T) {
	path := registryHolding(t, twoModules)

	if _, err := NewEditor(path).SetCoreVersions("token", []string{"11"}); err == nil {
		t.Fatal("it accepted a module that is not registered")
	} else if !strings.Contains(err.Error(), "token") {
		t.Errorf("the refusal does not name the module: %v", err)
	}

	registry, _ := RegistryFromFile(path)
	if _, found := registry.Find("token"); found {
		t.Error("it registered the module anyway")
	}
}

// A core list the loader would reject never becomes the registry: it is
// validated from the temporary file, so the real one is still readable after.
func TestABadCoreListNeverReachesTheRegistry(t *testing.T) {
	path := registryHolding(t, twoModules)

	if _, err := NewEditor(path).SetCoreVersions("jumplinks", []string{"not-a-core"}); err == nil {
		t.Fatal("it accepted a core version that is not a major")
	}

	registry, err := RegistryFromFile(path)
	if err != nil {
		t.Fatalf("the registry was corrupted: %v", err)
	}
	module, _ := registry.Find("jumplinks")
	if !slices.Equal(module.CoreVersions, []string{"11"}) {
		t.Errorf("cores %v — the rejected list was written", module.CoreVersions)
	}
}

// An empty list is rejected the same way, by the loader's own rule rather than
// by a second copy of it here.
func TestAnEmptyCoreListNeverReachesTheRegistry(t *testing.T) {
	path := registryHolding(t, twoModules)

	if _, err := NewEditor(path).SetCoreVersions("jumplinks", []string{}); err == nil {
		t.Fatal("it accepted an empty core list")
	}

	if _, err := RegistryFromFile(path); err != nil {
		t.Fatalf("the registry was corrupted: %v", err)
	}
}

// A rejected edit leaves no temporary file beside the registry.
func TestARejectedSetLeavesNoDebris(t *testing.T) {
	path := registryHolding(t, twoModules)

	if _, err := NewEditor(path).SetCoreVersions("jumplinks", []string{"nope"}); err == nil {
		t.Fatal("it accepted a bad core version")
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(path) {
			t.Errorf("left %q beside the registry", entry.Name())
		}
	}
}

// An unreadable registry is reported rather than replaced with one holding
// only this edit.
func TestSettingCoreVersionsOnAnUnreadableRegistryRefuses(t *testing.T) {
	path := registryHolding(t, "\tnot: [yaml")

	if _, err := NewEditor(path).SetCoreVersions("jumplinks", []string{"11"}); err == nil {
		t.Fatal("it wrote over a registry it could not parse")
	}
}

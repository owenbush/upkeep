package cockpit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const fixtureDir = "../../testdata/registries"

type expectedRegistry struct {
	Accepted bool `json:"accepted"`
	Modules  map[string]struct {
		Name         string   `json:"name"`
		Project      string   `json:"project"`
		CoreVersions []string `json:"core_versions"`
		Watched      bool     `json:"watched"`
	} `json:"modules"`
}

// registry.yml is a file both implementations read and write, so a
// disagreement about which ones are valid is a disagreement about the user's
// own config.
//
// YAML parsers differ at exactly the edges this file lives on — an unquoted
// number, a null mapping, a scalar where a list belongs — so the fixtures go
// through the real PHP loader and the answers are committed. Regenerate with:
// php registry_expect.php.
//
// Only accept/reject and the parsed content are compared. The two word their
// refusals differently, and a refusal is judged by whether it happens.
func TestRegistryLoadingMatchesPhp(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(fixtureDir, "expected.json"))
	if err != nil {
		t.Fatalf("answers: %v (regenerate with: php registry_expect.php)", err)
	}

	var expected map[string]expectedRegistry
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatalf("answers: %v", err)
	}
	if len(expected) == 0 {
		t.Fatal("no answers recorded")
	}

	fixtures, err := filepath.Glob(filepath.Join(fixtureDir, "*.yml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(fixtures) != len(expected) {
		t.Fatalf("%d fixtures on disk, %d answers recorded — regenerate", len(fixtures), len(expected))
	}

	for _, fixture := range fixtures {
		name := filepath.Base(fixture)
		want, recorded := expected[name]
		if !recorded {
			t.Errorf("%s: no recorded answer", name)

			continue
		}

		registry, err := RegistryFromFile(fixture)
		if want.Accepted != (err == nil) {
			t.Errorf("%s: accepted=%v, PHP says %v (%v)", name, err == nil, want.Accepted, err)

			continue
		}
		if !want.Accepted {
			continue
		}

		got := registry.Modules()
		if len(got) != len(want.Modules) {
			t.Errorf("%s: parsed %d modules, PHP parsed %d", name, len(got), len(want.Modules))

			continue
		}
		for key, wantModule := range want.Modules {
			gotModule, found := got[key]
			if !found {
				t.Errorf("%s: PHP found module %q and this did not", name, key)

				continue
			}
			if gotModule.Name != wantModule.Name || gotModule.Project != wantModule.Project {
				t.Errorf("%s/%s: got %+v, PHP got %+v", name, key, gotModule, wantModule)
			}
			if !slices.Equal(gotModule.CoreVersions, wantModule.CoreVersions) {
				t.Errorf("%s/%s: cores %v, PHP got %v", name, key, gotModule.CoreVersions, wantModule.CoreVersions)
			}
			if gotModule.Watched != wantModule.Watched {
				t.Errorf("%s/%s: watched %v, PHP got %v", name, key, gotModule.Watched, wantModule.Watched)
			}
		}
	}
}

// An unquoted number in core_versions is the shape a maintainer writes by
// hand, and it has to come back as the string that becomes a path segment.
func TestUnquotedCoreVersionsBecomeStrings(t *testing.T) {
	registry := load(t, "ok-unquoted-numbers.yml")

	module, found := registry.Find("pathauto")
	if !found {
		t.Fatal("pathauto not loaded")
	}
	if !slices.Equal(module.CoreVersions, []string{"10", "11"}) {
		t.Errorf("cores %v", module.CoreVersions)
	}
}

// The scaffold writes an empty watchlist, and an empty one is legitimate.
func TestAnEmptyModulesKeyIsAnEmptyWatchlist(t *testing.T) {
	registry := load(t, "ok-empty-modules-null.yml")

	if len(registry.Modules()) != 0 {
		t.Errorf("got %v", registry.Modules())
	}
}

// The registry is a file a person reads, and the surveys render in its order.
func TestFileOrderIsPreserved(t *testing.T) {
	registry := load(t, "ok-two-modules.yml")

	if names := registry.Names(); !slices.Equal(names, []string{"pathauto", "token"}) {
		t.Errorf("got %v, want the order the file lists", names)
	}
}

// A registry entry exists precisely for the module whose project path is not
// the convention.
func TestANonStandardProjectPathIsKept(t *testing.T) {
	registry := load(t, "ok-nonstandard-project.yml")

	module, _ := registry.Find("pathauto")
	if module.Project != "someone/fork-of-pathauto" {
		t.Errorf("project %q", module.Project)
	}
}

// Everything loaded from the registry is watched by definition; only
// resolution produces a derived one.
func TestLoadedModulesAreWatched(t *testing.T) {
	registry := load(t, "ok-simple.yml")

	module, _ := registry.Find("pathauto")
	if !module.Watched {
		t.Error("a module read from the registry did not read as watched")
	}
}

// The key becomes a directory name and an engine project name, so the failure
// belongs at load rather than deep inside prune after environments have been
// torn down.
func TestTheRefusalNamesTheOffendingKeyAndWhyItMatters(t *testing.T) {
	_, err := RegistryFromFile(filepath.Join(fixtureDir, "bad-name-traversal.yml"))
	if err == nil {
		t.Fatal("a traversal sequence was accepted as a module key")
	}
	if !strings.Contains(err.Error(), "../escape") {
		t.Errorf("error %q does not name the key", err)
	}
	if !strings.Contains(err.Error(), "directory names") {
		t.Errorf("error %q does not say why it matters", err)
	}
}

// Both of these are refused twice over — the specific guard, and then the
// general one underneath it. What the guard buys is the wording, so that is
// what is pinned: "your file has no modules key" and "your pathauto entry is
// not a mapping" send somebody to different lines of the same file, where the
// fallback message sends them to neither.
func TestTheRefusalNamesTheShapeThatIsWrong(t *testing.T) {
	_, err := RegistryFromFile(filepath.Join(fixtureDir, "bad-no-modules-key.yml"))
	if err == nil {
		t.Fatal("a registry with no modules key loaded")
	}
	if !strings.Contains(err.Error(), "top-level \"modules\" key") {
		t.Errorf("error %q does not say the file has no modules key", err)
	}

	_, err = RegistryFromFile(filepath.Join(fixtureDir, "bad-definition-scalar.yml"))
	if err == nil {
		t.Fatal("a scalar module definition loaded")
	}
	if !strings.Contains(err.Error(), `module "pathauto"`) {
		t.Errorf("error %q does not name the module whose entry is wrong", err)
	}
	if !strings.Contains(err.Error(), "must be a mapping") {
		t.Errorf("error %q does not say what shape it wanted", err)
	}
	// Without the shape check the decoder answers instead, and its complaint
	// quotes the Go struct it was decoding into — which names fields and types
	// a maintainer editing YAML has no use for.
	if strings.Contains(err.Error(), "yaml: unmarshal errors") {
		t.Errorf("error %q leaks the parser's internals", err)
	}
}

func TestAMissingRegistryNamesTheRecovery(t *testing.T) {
	_, err := RegistryFromFile(filepath.Join(t.TempDir(), "registry.yml"))
	if err == nil {
		t.Fatal("a missing registry loaded")
	}
	if !strings.Contains(err.Error(), "upkeep init") {
		t.Errorf("error %q does not name the recovery", err)
	}
}

func load(t *testing.T, fixture string) *Registry {
	t.Helper()

	registry, err := RegistryFromFile(filepath.Join(fixtureDir, fixture))
	if err != nil {
		t.Fatalf("%s: %v", fixture, err)
	}

	return registry
}

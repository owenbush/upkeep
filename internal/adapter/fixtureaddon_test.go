package adapter

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// One fact, two strings, two repositories, nothing comparing them — which is
// how `--fixture` came to be unable to work while every unit test stayed
// green. The probe must look for the file the invocation names.
func TestTheProbeLooksForTheCommandTheAdapterInvokes(t *testing.T) {
	if !FixtureAddOnMarkerMatchesCommand() {
		t.Fatalf("the marker %q is not the command %q", FixtureAddOnMarker, FixtureLoadCommand)
	}
	// ddev names a host command after the file it came from.
	if FixtureAddOnMarker != "commands/host/"+FixtureLoadCommand {
		t.Errorf("marker %q", FixtureAddOnMarker)
	}
}

// ddev gives every add-on's host commands one flat namespace per project, so a
// name as general as "fixture-load" claims ground this add-on has no business
// claiming.
func TestTheCommandIsNamespaced(t *testing.T) {
	if !strings.HasPrefix(FixtureLoadCommand, "upkeep-") {
		t.Errorf("command %q is not namespaced", FixtureLoadCommand)
	}
}

// A published add-on with no pin means every environment silently tracks
// whatever its latest release happens to be.
func TestThePublishedAddOnIsPinnedToARelease(t *testing.T) {
	t.Setenv(FixtureAddOnSourceEnv, "")

	args := FixtureAddOnInstallArguments()
	if !slices.Contains(args, "--version") {
		t.Errorf("the published add-on is installed unpinned: %v", args)
	}
	if !slices.Contains(args, FixtureAddOnVersion) {
		t.Errorf("args %v do not name the pinned release", args)
	}
	if args[0] != FixtureAddOnName {
		t.Errorf("args %v do not name the add-on first", args)
	}
}

// An override is a local checkout or an arbitrary source, where a release tag
// means nothing and naming one is an error rather than a constraint.
func TestAnOverriddenSourceIsInstalledUnpinned(t *testing.T) {
	t.Setenv(FixtureAddOnSourceEnv, "/home/me/ddev-upkeep")

	if !FixtureAddOnIsOverridden() {
		t.Fatal("the override was not seen")
	}
	args := FixtureAddOnInstallArguments()
	if slices.Contains(args, "--version") {
		t.Errorf("a local checkout was given a release tag: %v", args)
	}
	if !slices.Equal(args, []string{"/home/me/ddev-upkeep"}) {
		t.Errorf("args %v", args)
	}
}

// Developing the two repositories together is exactly when a stale add-on is
// hardest to notice, so moving between a checkout and the release re-installs.
func TestTheStampDistinguishesACheckoutFromTheRelease(t *testing.T) {
	t.Setenv(FixtureAddOnSourceEnv, "")
	published := FixtureAddOnExpectedStamp()

	t.Setenv(FixtureAddOnSourceEnv, "/home/me/ddev-upkeep")
	overridden := FixtureAddOnExpectedStamp()

	if published == overridden {
		t.Fatalf("both stamp %q", published)
	}
	if published != FixtureAddOnVersion {
		t.Errorf("the published stamp is %q", published)
	}
	if !strings.HasPrefix(overridden, "source:") {
		t.Errorf("the override stamp is %q", overridden)
	}
}

// A file's presence says nothing about which release wrote it, which is why
// the probe is the command file *and* a version stamp.
func TestTheStampIsUpkeepsOwnFileUnderTheAddOnsDirectory(t *testing.T) {
	if !strings.HasPrefix(FixtureAddOnStamp, "upkeep/") {
		t.Errorf("stamp %q is not under the directory the add-on owns", FixtureAddOnStamp)
	}
	if strings.Contains(FixtureAddOnStamp, "ddev") {
		t.Errorf("stamp %q depends on ddev's own add-on metadata", FixtureAddOnStamp)
	}
}

const moduleComposerJSON = `{
    "name": "drupal/field_visibility_conditions",
    "require": {
        "php": ">=8.1",
        "drupal/core": "^10 || ^11"
    },
    "require-dev": {
        "phpcompatibility/php-compatibility": "^9.3",
        "php": ">=8.1",
        "ext-json": "*",
        "lib-curl": "*",
        "drupal/coder": "^8.3"
    }
}`

// Its ruleset references a sniff its own require-dev provides, and composer
// does not install a path dependency's require-dev.
func TestAModulesOwnDevDependenciesAreRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(moduleComposerJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := ModuleDevRequirements(dir)
	if !slices.Equal(got, []string{"phpcompatibility/php-compatibility", "drupal/coder"}) {
		t.Errorf("got %v", got)
	}
}

// php, ext-* and lib-* are not packages, and asking composer to require them
// into the site would fail a provision over something no install can satisfy.
func TestPlatformRequirementsAreNotPackages(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(moduleComposerJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, platform := range ModuleDevRequirements(dir) {
		if !strings.Contains(platform, "/") {
			t.Errorf("%q is not a package and would fail a provision", platform)
		}
	}
}

// A module with no composer.json, or a malformed one, is not a reason to
// refuse to check it.
func TestAnUnreadableManifestYieldsNoneRatherThanRefusing(t *testing.T) {
	for name, contents := range map[string]string{
		"missing":          "",
		"not JSON":         "{ nope",
		"a list":           "[]",
		"no require-dev":   `{"name":"drupal/x"}`,
		"require-dev null": `{"require-dev":null}`,
		"require-dev list": `{"require-dev":["drupal/coder"]}`,
	} {
		dir := t.TempDir()
		if contents != "" {
			if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(contents), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		}

		got := ModuleDevRequirements(dir)
		if len(got) != 0 {
			t.Errorf("%s yielded %v", name, got)
		}
	}
}

// A composer failure naming the first package should name the first one the
// module wrote.
func TestThePackagesComeOutInTheOrderTheModuleListsThem(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"require-dev":{"z/last":"*","a/first":"*","m/middle":"*"}}`
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := ModuleDevRequirements(dir)
	if !slices.Equal(got, []string{"z/last", "a/first", "m/middle"}) {
		t.Errorf("got %v, want the file's order", got)
	}
}

// A warning rather than a refusal: failing the whole provision would take down
// phpunit, the install check and the smoke test over a linting dependency.
func TestTheUnavailableWarningNamesThePackagesAndWhatItCosts(t *testing.T) {
	message := DevRequirementsUnavailable([]string{"phpcompatibility/php-compatibility", "drupal/coder"})

	for _, want := range []string{
		"phpcompatibility/php-compatibility",
		"drupal/coder",
		"phpcs or phpstan configuration",
		"the check will name it if so",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("message does not carry %q:\n%s", want, message)
		}
	}
}

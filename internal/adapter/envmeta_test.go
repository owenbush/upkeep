package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func aMeta() EnvironmentMeta {
	return EnvironmentMeta{
		ModuleName:      "pathauto",
		CoreMajor:       "11",
		SeedCoreVersion: "11.4.6",
		AddOnVersion:    EngineAddOnVersion,
		CreatedAt:       time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC),
	}
}

// It doubles as the provisioning completion marker, so an unreadable one means
// a partial provision that must be torn down rather than reused.
func TestAnUnreadableMetaIsARefusalNotADefault(t *testing.T) {
	for name, contents := range map[string]string{
		"not YAML":        "module: [pathauto",
		"a list":          "- module: pathauto\n",
		"a scalar":        "just a string\n",
		"empty":           "",
		"no module":       "core_major: '11'\nseed_core_version: 11.4.6\naddon_version: 1.1.5\ncreated_at: '2026-06-12T10:00:00+00:00'\n",
		"no created_at":   "module: pathauto\ncore_major: '11'\nseed_core_version: 11.4.6\naddon_version: 1.1.5\n",
		"null module":     "module: ~\ncore_major: '11'\nseed_core_version: 11.4.6\naddon_version: 1.1.5\ncreated_at: '2026-06-12T10:00:00+00:00'\n",
		"bad created_at":  "module: pathauto\ncore_major: '11'\nseed_core_version: 11.4.6\naddon_version: 1.1.5\ncreated_at: soon\n",
		"bad last_used":   "module: pathauto\ncore_major: '11'\nseed_core_version: 11.4.6\naddon_version: 1.1.5\ncreated_at: '2026-06-12T10:00:00+00:00'\nlast_used_at: soon\n",
		"structured last": "module: pathauto\ncore_major: '11'\nseed_core_version: 11.4.6\naddon_version: 1.1.5\ncreated_at: '2026-06-12T10:00:00+00:00'\nlast_used_at: {a: 1}\n",
	} {
		if _, err := EnvMetaFromYAML(contents); err == nil {
			t.Errorf("%s was accepted as an environment meta", name)
		}
	}
}

// Every reuse stamps it, or age falls back to creation time and prune deletes
// environments that are in active use.
func TestTheLastUseIsStampedAndSurvivesARoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := aMeta().WriteTo(dir); err != nil {
		t.Fatalf("write: %v", err)
	}

	used := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	if err := StampLastUsed(dir, used); err != nil {
		t.Fatalf("stamp: %v", err)
	}

	contents, err := os.ReadFile(EnvMetaPath(dir))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	back, err := EnvMetaFromYAML(string(contents))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if !back.LastUsedAt.Equal(used) {
		t.Errorf("last used %v, want %v", back.LastUsedAt, used)
	}
	// And nothing else moved.
	if !back.CreatedAt.Equal(aMeta().CreatedAt) {
		t.Errorf("created at %v", back.CreatedAt)
	}
	if back.ModuleName != "pathauto" || back.SeedCoreVersion != "11.4.6" {
		t.Errorf("got %+v", back)
	}
}

// The path must never stop existing, or a re-stamp would look like an
// interrupted provision and force a multi-gigabyte rebuild.
func TestTheDotfileIsNeverAbsentMidWrite(t *testing.T) {
	dir := t.TempDir()
	if err := aMeta().WriteTo(dir); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Ten restamps, checking the file exists and parses after every one.
	for i := range 10 {
		if err := StampLastUsed(dir, time.Now().Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("stamp %d: %v", i, err)
		}
		contents, err := os.ReadFile(EnvMetaPath(dir))
		if err != nil {
			t.Fatalf("stamp %d left no dotfile: %v", i, err)
		}
		if _, err := EnvMetaFromYAML(string(contents)); err != nil {
			t.Fatalf("stamp %d left an unreadable dotfile: %v", i, err)
		}
	}
}

// Stamping an environment with no dotfile is a refusal, not a fresh one:
// writing one would turn a partial provision into something that looks
// finished.
func TestStampingAnEnvironmentWithNoDotfileRefuses(t *testing.T) {
	dir := t.TempDir()

	err := StampLastUsed(dir, time.Now())
	if err == nil {
		t.Fatal("an environment with no meta was stamped")
	}
	// "Cannot read the meta" says the provision never finished. "This is not a
	// mapping" reads like a corrupt file, which sends somebody to look at
	// contents that are not there.
	if !strings.Contains(err.Error(), "cannot read the environment meta") {
		t.Errorf("the refusal reads as a parse failure rather than a missing file: %v", err)
	}
	if _, err := os.Stat(EnvMetaPath(dir)); err == nil {
		t.Error("a dotfile was invented for a partial provision")
	}
}

// Any reason means torn down and re-provisioned; an empty list means safe to
// reuse.
func TestEveryKindOfSkewIsItsOwnReason(t *testing.T) {
	meta := aMeta()

	if reasons := meta.StaleReasons("pathauto", "11", "11.4.6", EngineAddOnVersion); len(reasons) != 0 {
		t.Errorf("a matching environment read as stale: %v", reasons)
	}

	for name, args := range map[string][4]string{
		"module": {"token", "11", "11.4.6", EngineAddOnVersion},
		"core":   {"pathauto", "12", "11.4.6", EngineAddOnVersion},
		"seed":   {"pathauto", "11", "11.5.0", EngineAddOnVersion},
		"add-on": {"pathauto", "11", "11.4.6", "1.1.6"},
	} {
		reasons := meta.StaleReasons(args[0], args[1], args[2], args[3])
		if len(reasons) != 1 {
			t.Errorf("%s skew gave %d reasons: %v", name, len(reasons), reasons)
		}
	}

	// And several at once are all reported, not just the first.
	if reasons := meta.StaleReasons("token", "12", "11.5.0", "1.1.6"); len(reasons) != 4 {
		t.Errorf("got %d reasons: %v", len(reasons), reasons)
	}
}

// Seed skew is the one a maintainer cannot guess at, so it says both versions.
func TestSeedSkewNamesBothCoreVersions(t *testing.T) {
	reasons := aMeta().StaleReasons("pathauto", "11", "11.5.0", EngineAddOnVersion)

	if len(reasons) != 1 {
		t.Fatalf("got %v", reasons)
	}
	for _, want := range []string{"11.4.6", "11.5.0", "Seed skew"} {
		if !strings.Contains(reasons[0], want) {
			t.Errorf("reason %q does not carry %q", reasons[0], want)
		}
	}
}

// Bumping the pin is what makes existing environments self-report skew rather
// than being silently reused.
func TestAnAddOnBumpMakesEveryExistingEnvironmentStale(t *testing.T) {
	old := aMeta()
	old.AddOnVersion = "1.1.4"

	reasons := old.StaleReasons("pathauto", "11", "11.4.6", EngineAddOnVersion)
	if len(reasons) != 1 {
		t.Fatalf("got %v", reasons)
	}
	if !strings.Contains(reasons[0], "add-on skew") {
		t.Errorf("reason %q", reasons[0])
	}
}

const shippedAddOnConfig = `#ddev-generated
web_environment:
  - DRUPAL_PROJECTS_PATH=web/modules/custom
  - SIMPLETEST_BASE_URL=http://web
  - SOMETHING_ELSE=keep me
hooks:
  post-start:
    - exec: symlink-project
  pre-start:
    - exec: something
nodejs_version: "20"
`

// The add-on assumes the module is the project root; upkeep seeds a full tree
// and wires the module in with a path repository.
func TestTheAdaptationRemovesTheHooksAndRepointsTheProjectsPath(t *testing.T) {
	adapted, err := AdaptContribConfig(shippedAddOnConfig)
	if err != nil {
		t.Fatalf("adapt: %v", err)
	}

	// Against a full project tree the post-start hook would symlink the whole
	// codebase into itself.
	if strings.Contains(adapted, "symlink-project") || strings.Contains(adapted, "hooks:") {
		t.Errorf("the hooks survived:\n%s", adapted)
	}
	if !strings.Contains(adapted, "DRUPAL_PROJECTS_PATH="+EngineProjectsPath) {
		t.Errorf("the projects path was not repointed:\n%s", adapted)
	}
	if strings.Contains(adapted, "web/modules/custom") {
		t.Errorf("the shipped projects path survived:\n%s", adapted)
	}
}

// Everything else the add-on ships is left exactly as found.
func TestTheAdaptationTouchesNothingElse(t *testing.T) {
	adapted, err := AdaptContribConfig(shippedAddOnConfig)
	if err != nil {
		t.Fatalf("adapt: %v", err)
	}

	for _, want := range []string{
		"SIMPLETEST_BASE_URL=http://web",
		"SOMETHING_ELSE=keep me",
		"nodejs_version:",
	} {
		if !strings.Contains(adapted, want) {
			t.Errorf("the adaptation dropped %q:\n%s", want, adapted)
		}
	}
}

// A future add-on installation may clobber the file, which is why the marker
// is kept and the adaptation is re-run after every one.
func TestTheGeneratedMarkerIsKept(t *testing.T) {
	adapted, err := AdaptContribConfig(shippedAddOnConfig)
	if err != nil {
		t.Fatalf("adapt: %v", err)
	}

	if !strings.HasPrefix(adapted, "#ddev-generated\n") {
		t.Errorf("the marker is not first:\n%s", adapted)
	}
	if !strings.Contains(adapted, "Adapted by upkeep") {
		t.Errorf("nothing says upkeep touched it:\n%s", adapted)
	}
}

// Adapting it again must give the same thing: the adapter re-runs this after
// every add-on installation.
func TestTheAdaptationIsIdempotent(t *testing.T) {
	once, err := AdaptContribConfig(shippedAddOnConfig)
	if err != nil {
		t.Fatalf("adapt: %v", err)
	}
	twice, err := AdaptContribConfig(once)
	if err != nil {
		t.Fatalf("re-adapt: %v", err)
	}

	if twice != once {
		t.Errorf("re-adapting changed it:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
}

// Parsed YAML is untrusted: an entry that is not a KEY=value string is left
// exactly as found rather than coerced.
func TestAnUnexpectedEnvironmentEntryIsLeftAlone(t *testing.T) {
	odd := "web_environment:\n  - {nested: mapping}\n  - 42\n  - DRUPAL_PROJECTS_PATH=web/modules/custom\n"

	adapted, err := AdaptContribConfig(odd)
	if err != nil {
		t.Fatalf("adapt: %v", err)
	}
	if !strings.Contains(adapted, "nested") || !strings.Contains(adapted, "42") {
		t.Errorf("an unrecognised entry was dropped:\n%s", adapted)
	}
	if !strings.Contains(adapted, "DRUPAL_PROJECTS_PATH="+EngineProjectsPath) {
		t.Errorf("the one it does recognise was not rewritten:\n%s", adapted)
	}
}

// A config that is not a mapping is refused rather than adapted into
// something.
func TestAConfigThatIsNotAMappingIsRefused(t *testing.T) {
	for name, contents := range map[string]string{
		"a list":   "- a\n- b\n",
		"a scalar": "just a string\n",
		"empty":    "",
		"not YAML": "web_environment: [unclosed\n",
	} {
		if _, err := AdaptContribConfig(contents); err == nil {
			t.Errorf("%s was adapted", name)
		}
	}
}

// The environment is created with a file mode anyone can read, because a
// dotfile recording a module name and a core version carries nothing private.
func TestTheDotfileIsOrdinaryContent(t *testing.T) {
	dir := t.TempDir()
	if err := aMeta().WriteTo(dir); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, EnvMetaFilename))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("dotfile is %04o", info.Mode().Perm())
	}
}

// A meta with no recorded last use is not a meta with an epoch one.
func TestAnUnusedEnvironmentHasNoLastUse(t *testing.T) {
	rendered, err := aMeta().ToYAML()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(rendered, "last_used_at") {
		t.Errorf("an unused environment recorded a last use:\n%s", rendered)
	}

	back, err := EnvMetaFromYAML(rendered)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !back.LastUsedAt.IsZero() {
		t.Errorf("last used %v, want nothing", back.LastUsedAt)
	}
}

func TestTheRequiredKeysAreAllOfThem(t *testing.T) {
	complete := "module: pathauto\ncore_major: '11'\nseed_core_version: 11.4.6\n" +
		"addon_version: 1.1.5\ncreated_at: '2026-06-12T10:00:00+00:00'\n"

	if _, err := EnvMetaFromYAML(complete); err != nil {
		t.Fatalf("a complete meta was refused: %v", err)
	}

	for _, key := range []string{"module", "core_major", "seed_core_version", "addon_version", "created_at"} {
		without := []string{}
		for _, line := range strings.Split(strings.TrimRight(complete, "\n"), "\n") {
			if !strings.HasPrefix(line, key+":") {
				without = append(without, line)
			}
		}
		if _, err := EnvMetaFromYAML(strings.Join(without, "\n") + "\n"); err == nil {
			t.Errorf("a meta with no %q was accepted", key)
		}
	}
}

// The lenient read, which the disk inventory uses. Strictness is right for the
// reuse decision and wrong here: a partial provision, or one written before a
// field existed, is still disk usage to report and is exactly what prune
// exists to collect.
func TestAttributionIsReadLenientlyWhereTheReuseDecisionIsStrict(t *testing.T) {
	// The shape before seed_core_version and addon_version existed — which
	// EnvMetaFromYAML refuses outright.
	older := "module: pathauto\ncore_major: '11'\ncreated_at: '2026-01-04T10:00:00+00:00'\n"

	if _, err := EnvMetaFromYAML(older); err == nil {
		t.Fatal("the strict reader accepted a meta missing required keys — this test has lost its point")
	}

	module, coreMajor, lastUsedAt := EnvMetaAttribution(older)
	if module != "pathauto" || coreMajor != "11" {
		t.Errorf("attribution was lost: %q / %q", module, coreMajor)
	}
	if lastUsedAt.UTC().Format(time.RFC3339) != "2026-01-04T10:00:00Z" {
		t.Errorf("age was lost: %s", lastUsedAt)
	}
}

// Age prefers the reuse stamp over the creation time: without that, an
// environment in daily use ages from the day it was built and `prune
// --older-than` deletes it.
func TestAttributionAgePrefersTheReuseStamp(t *testing.T) {
	_, _, lastUsedAt := EnvMetaAttribution(
		"module: pathauto\ncore_major: '11'\n" +
			"created_at: '2026-01-04T10:00:00+00:00'\nlast_used_at: '2026-09-01T08:00:00+00:00'\n",
	)

	if lastUsedAt.UTC().Format(time.RFC3339) != "2026-09-01T08:00:00Z" {
		t.Errorf("got %s", lastUsedAt)
	}
}

// Nothing usable is unknown, never a guess: the selector reads an unknown age
// as "cannot tell", which is what stops a prune deleting on the strength of a
// question it could not ask.
func TestAttributionOfSomethingUnreadableIsUnknown(t *testing.T) {
	for _, contents := range []string{
		"",
		"\tnot: [yaml",
		"just a string",
		"- a\n- list\n",
		"module: null\ncore_major: ~\n",
		"module:\n  - pathauto\n",
		"created_at: not-a-date\n",
	} {
		module, coreMajor, lastUsedAt := EnvMetaAttribution(contents)
		if module != "" || coreMajor != "" || !lastUsedAt.IsZero() {
			t.Errorf("%q yielded %q / %q / %s", contents, module, coreMajor, lastUsedAt)
		}
	}
}

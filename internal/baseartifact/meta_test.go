package baseartifact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

const metaFixtureDir = "../../testdata/metas"

type expectedMeta struct {
	Accepted    bool                `json:"accepted"`
	CoreVersion string              `json:"core_version"`
	CoreMajor   string              `json:"core_major"`
	PHPVersion  string              `json:"php_version"`
	DBEngine    string              `json:"db_engine"`
	BuiltAt     string              `json:"built_at"`
	YAML        string              `json:"yaml"`
	Skew        map[string][]string `json:"skew"`
}

// meta.yml is written by whichever implementation built the artifact set and
// read by whichever one uses it, so the two have to agree about which sidecars
// are readable and what they say.
//
// The answers are committed; git history holds the PHP that produced them.
func TestMetaReadingMatchesPhp(t *testing.T) {
	expected := loadExpectedMetas(t)

	fixtures, err := filepath.Glob(filepath.Join(metaFixtureDir, "*.yml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(fixtures) != len(expected) {
		t.Fatalf("%d fixtures on disk, %d answers recorded — regenerate", len(fixtures), len(expected))
	}

	for _, fixture := range fixtures {
		name := filepath.Base(fixture)
		want := expected[name]

		contents, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		meta, err := MetaFromYAML(string(contents))
		if want.Accepted != (err == nil) {
			t.Errorf("%s: accepted=%v, PHP says %v (%v)", name, err == nil, want.Accepted, err)

			continue
		}
		if !want.Accepted {
			continue
		}

		if meta.CoreVersion != want.CoreVersion {
			t.Errorf("%s: core_version %q, PHP read %q", name, meta.CoreVersion, want.CoreVersion)
		}
		// The major is the one a maintainer may leave unquoted, and it becomes
		// a path segment either way.
		if meta.CoreMajor != want.CoreMajor {
			t.Errorf("%s: core_major %q, PHP read %q", name, meta.CoreMajor, want.CoreMajor)
		}
		if meta.PHPVersion != want.PHPVersion {
			t.Errorf("%s: php_version %q, PHP read %q", name, meta.PHPVersion, want.PHPVersion)
		}
		if meta.DBEngine != want.DBEngine {
			t.Errorf("%s: db_engine %q, PHP read %q", name, meta.DBEngine, want.DBEngine)
		}

		wantBuiltAt, err := time.Parse(time.RFC3339, want.BuiltAt)
		if err != nil {
			t.Fatalf("%s: recorded built_at is unparseable: %v", name, err)
		}
		if !meta.BuiltAt.Equal(wantBuiltAt) {
			t.Errorf("%s: built_at %v, PHP read %v", name, meta.BuiltAt, wantBuiltAt)
		}
	}
}

// A sidecar that is the wrong shape entirely must say so, rather than send
// somebody hunting for a key in a file that has no keys at all. Both refusals
// happen either way; what the shape check buys is which one.
func TestAMetaThatIsNotAMappingSaysThatRatherThanNamingAKey(t *testing.T) {
	for _, fixture := range []string{"bad-list.yml", "bad-scalar.yml", "bad-empty.yml"} {
		contents, err := os.ReadFile(filepath.Join(metaFixtureDir, fixture))
		if err != nil {
			t.Fatalf("%s: %v", fixture, err)
		}

		_, err = MetaFromYAML(string(contents))
		if err == nil {
			t.Errorf("%s: accepted", fixture)

			continue
		}
		if !strings.Contains(err.Error(), "must be a mapping") {
			t.Errorf("%s: error %q does not say the file is the wrong shape", fixture, err)
		}
	}
}

// What one side writes the other must read, and a base artifact set outlives
// whichever binary built it.
func TestPhpsRenderingIsReadableHere(t *testing.T) {
	expected := loadExpectedMetas(t)

	checked := 0
	for name, want := range expected {
		if !want.Accepted {
			continue
		}
		meta, err := MetaFromYAML(want.YAML)
		if err != nil {
			t.Errorf("%s: cannot read PHP's own rendering: %v\n%s", name, err, want.YAML)

			continue
		}
		if meta.CoreVersion != want.CoreVersion || meta.CoreMajor != want.CoreMajor {
			t.Errorf("%s: PHP's rendering read back as %+v", name, meta)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no renderings were checked")
	}
}

// And the other direction: this rendering has to survive its own reader, and
// the go-rendered fixtures prove PHP reads it too — they are in the set
// the generator loaded, and which is committed.
func TestThisRenderingRoundTrips(t *testing.T) {
	original := Meta{
		CoreVersion: "12.0.0-alpha1",
		CoreMajor:   "12",
		PHPVersion:  "8.3.14",
		DBEngine:    "mariadb:10.11",
		BuiltAt:     time.Date(2026, 9, 1, 8, 30, 0, 0, time.FixedZone("CEST", 2*3600)),
	}

	rendered, err := original.ToYAML()
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	back, err := MetaFromYAML(rendered)
	if err != nil {
		t.Fatalf("cannot read what it wrote: %v\n%s", err, rendered)
	}
	if back.CoreVersion != original.CoreVersion || back.CoreMajor != original.CoreMajor {
		t.Errorf("got %+v", back)
	}
	// The major must not come back as a number: it is a path segment.
	if !strings.Contains(rendered, "core_major:") {
		t.Errorf("rendering %q", rendered)
	}
	if !back.BuiltAt.Equal(original.BuiltAt) {
		t.Errorf("built_at %v, want %v", back.BuiltAt, original.BuiltAt)
	}
}

// A patch release is ABI-compatible and not skew; a minor is. The database
// engine has to match exactly.
func TestSkewMatchesPhp(t *testing.T) {
	expected := loadExpectedMetas(t)

	for name, want := range expected {
		if !want.Accepted {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(metaFixtureDir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		meta, err := MetaFromYAML(string(contents))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		patched := bumpPatch(meta.PHPVersion)
		got := map[string][]string{
			"same":       meta.SkewAgainst(meta.PHPVersion, meta.DBEngine),
			"patch_only": meta.SkewAgainst(patched, meta.DBEngine),
			"minor":      meta.SkewAgainst("7.0.0", meta.DBEngine),
			"engine":     meta.SkewAgainst(meta.PHPVersion, "sqlite"),
		}

		for key, wantReasons := range want.Skew {
			if !slices.Equal(got[key], wantReasons) {
				t.Errorf("%s/%s: got %v, PHP got %v", name, key, got[key], wantReasons)
			}
		}
	}
}

// bumpPatch is the same substitution the generator made: a trailing ".N"
// becomes ".99", whatever position it is in.
//
// On a three-part version that is a patch bump and not skew. On a two-part one
// like "8.3" it lands on the *minor*, so 8.99 genuinely is skew — which is why
// this mirrors the regex rather than the intent behind it. Getting that wrong
// is what made this test fail first time round, on the harness rather than on
// either implementation.
var trailingNumber = regexp.MustCompile(`\.\d+$`)

func bumpPatch(version string) string {
	return trailingNumber.ReplaceAllString(version, ".99")
}

// A resolved core version comes from the lock, so an alpha build reads
// 12.0.0-alpha1 and cannot be mistaken for a release.
func TestTheCoreVersionComesFromTheLock(t *testing.T) {
	version, err := CoreVersionFromLock(`{"packages":[
		{"name":"drupal/token","version":"1.15.0"},
		{"name":"drupal/core","version":"12.0.0-alpha1"}
	]}`)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if version != "12.0.0-alpha1" {
		t.Errorf("got %q", version)
	}
}

// A lock file is untrusted input: anything but an object naming drupal/core
// with a string version falls through rather than being coerced into a version
// that was never resolved.
func TestALockWithoutAResolvedCoreIsRefused(t *testing.T) {
	for name, lock := range map[string]string{
		"not json":        `{ nope`,
		"no packages":     `{}`,
		"packages a list": `{"packages":["drupal/core"]}`,
		"no core":         `{"packages":[{"name":"drupal/token","version":"1.15.0"}]}`,
		"numeric version": `{"packages":[{"name":"drupal/core","version":11}]}`,
		"no version":      `{"packages":[{"name":"drupal/core"}]}`,
	} {
		if version, err := CoreVersionFromLock(lock); err == nil {
			t.Errorf("%s: read %q out of it", name, version)
		}
	}
}

func loadExpectedMetas(t *testing.T) map[string]expectedMeta {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(metaFixtureDir, "expected.json"))
	if err != nil {
		t.Fatalf("answers: %v (a committed fixture — see git history for the PHP that produced it)", err)
	}

	var expected map[string]expectedMeta
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatalf("answers: %v", err)
	}
	if len(expected) == 0 {
		t.Fatal("no answers recorded")
	}

	return expected
}

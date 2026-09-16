package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

// Composer's own branch normalisation. Requiring exactly this constraint pins
// the working copy's branch and can never drift to a different dev branch
// published on packages.drupal.org.
func TestTheDevConstraintFollowsComposersBranchNormalisation(t *testing.T) {
	for branch, want := range map[string]string{
		"1.0.x":   "1.0.x-dev",
		"2.x":     "2.x-dev",
		"11.1":    "11.1-dev",
		"v2.0":    "v2.0-dev",
		"3.*":     "3.*-dev",
		"main":    "dev-main",
		"8.x-1.x": "dev-8.x-1.x",
		"master":  "dev-master",
		// A work branch is not version-like whatever it starts with.
		"3603341-drupal-12-compatibility": "dev-3603341-drupal-12-compatibility",
		"mr-12":                           "dev-mr-12",
	} {
		if got := DevConstraintForBranch(branch); got != want {
			t.Errorf("%q: got %q, want %q", branch, got, want)
		}
	}
}

const projectComposerJSON = `{
    "name": "drupal/recommended-project",
    "type": "project",
    "repositories": [
        {
            "type": "composer",
            "url": "https://packages.drupal.org/8"
        }
    ],
    "require": {
        "drupal/core-recommended": "^11"
    },
    "minimum-stability": "dev"
}`

// Composer honours repository order, and the first repository providing a
// package wins — so the module in the working copy must outrank the one
// published on packages.drupal.org.
func TestThePathRepositoryOutranksPackagesDrupalOrg(t *testing.T) {
	rewritten, err := WithPathRepository(projectComposerJSON, "/var/www/html/pathauto")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	var decoded struct {
		Repositories []map[string]any `json:"repositories"`
	}
	if err := json.Unmarshal([]byte(rewritten), &decoded); err != nil {
		t.Fatalf("the result is not JSON: %v\n%s", err, rewritten)
	}

	if len(decoded.Repositories) != 2 {
		t.Fatalf("got %d repositories: %v", len(decoded.Repositories), decoded.Repositories)
	}
	first := decoded.Repositories[0]
	if first["type"] != "path" || first["url"] != "/var/www/html/pathauto" {
		t.Errorf("the path repository is not first: %v", decoded.Repositories)
	}
	options, isObject := first["options"].(map[string]any)
	if !isObject || options["symlink"] != true {
		t.Errorf("the path repository does not symlink: %v", first)
	}
	// And the existing one survives.
	if decoded.Repositories[1]["url"] != "https://packages.drupal.org/8" {
		t.Errorf("the shipped repository was dropped: %v", decoded.Repositories)
	}
}

// The adapter rewrites this on every provision, so doing it twice must not
// accumulate entries.
func TestRewritingTwiceDoesNotAccumulate(t *testing.T) {
	once, err := WithPathRepository(projectComposerJSON, "/var/www/html/pathauto")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	twice, err := WithPathRepository(once, "/var/www/html/pathauto")
	if err != nil {
		t.Fatalf("re-rewrite: %v", err)
	}

	if twice != once {
		t.Errorf("rewriting twice changed it:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
}

// A path repository for a *different* module is somebody else's and stays.
func TestAPathRepositoryForSomewhereElseIsKept(t *testing.T) {
	withOther := `{"repositories":[{"type":"path","url":"/var/www/html/token","options":{"symlink":true}}]}`

	rewritten, err := WithPathRepository(withOther, "/var/www/html/pathauto")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if !strings.Contains(rewritten, "/var/www/html/token") {
		t.Errorf("another module's path repository was dropped:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "/var/www/html/pathauto") {
		t.Errorf("ours was not added:\n%s", rewritten)
	}
}

// Decoded JSON is untrusted: an entry that is not an object is not one of
// ours, so it is kept rather than dropped.
func TestAnUnrecognisedRepositoryEntryIsKept(t *testing.T) {
	odd := `{"repositories":["a string", 42, null, {"type":"composer","url":"https://x"}]}`

	rewritten, err := WithPathRepository(odd, "/var/www/html/pathauto")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	var decoded struct {
		Repositories []any `json:"repositories"`
	}
	if err := json.Unmarshal([]byte(rewritten), &decoded); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	// Four originals plus ours.
	if len(decoded.Repositories) != 5 {
		t.Errorf("got %d entries: %v", len(decoded.Repositories), decoded.Repositories)
	}
}

// composer.json is a file people read and edit, so rewriting one key must not
// shuffle the rest.
func TestTheKeyOrderSurvivesTheRewrite(t *testing.T) {
	rewritten, err := WithPathRepository(projectComposerJSON, "/var/www/html/pathauto")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	order := []string{`"name"`, `"type"`, `"repositories"`, `"require"`, `"minimum-stability"`}
	at := -1
	for _, key := range order {
		found := strings.Index(rewritten, key)
		if found < 0 {
			t.Fatalf("%s is gone:\n%s", key, rewritten)
		}
		if found < at {
			t.Errorf("%s moved:\n%s", key, rewritten)
		}
		at = found
	}
}

// A project with no repositories key at all gets one.
func TestAProjectWithNoRepositoriesGetsThePathOne(t *testing.T) {
	rewritten, err := WithPathRepository(`{"name":"drupal/recommended-project"}`, "/var/www/html/pathauto")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if !strings.Contains(rewritten, `"type": "path"`) {
		t.Errorf("no path repository was added:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, `"name": "drupal/recommended-project"`) {
		t.Errorf("the existing key was lost:\n%s", rewritten)
	}
}

// Slashes are not escaped, because a composer.json full of \/ is one nobody
// wants to read.
func TestNothingIsEscapedThatComposerWritesPlainly(t *testing.T) {
	// A URL carrying the characters Go's encoder escapes by default and PHP's
	// does not. composer.json is a file people read and diff, and \u0026 in
	// the middle of a repository URL is not what either implementation should
	// leave behind.
	withEntities := `{"repositories":[{"type":"composer","url":"https://example.test/p?a=1&b=2<3"}]}`

	rewritten, err := WithPathRepository(withEntities, "/var/www/html/pathauto")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if strings.Contains(rewritten, `\/`) {
		t.Errorf("slashes were escaped:\n%s", rewritten)
	}
	if strings.Contains(rewritten, `\u0026`) || strings.Contains(rewritten, `\u003c`) {
		t.Errorf("HTML characters were escaped:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "https://example.test/p?a=1&b=2<3") {
		t.Errorf("the URL did not survive verbatim:\n%s", rewritten)
	}
}

// Refusing to rewrite is better than rewriting something that is not a project
// file.
func TestAnUnusableComposerJsonIsRefused(t *testing.T) {
	for name, contents := range map[string]string{
		"not JSON":               `{ nope`,
		"a list":                 `[]`,
		"a scalar":               `"a string"`,
		"repositories an object": `{"repositories":{"drupal":{"type":"composer"}}}`,
		"repositories a scalar":  `{"repositories":"nope"}`,
	} {
		if _, err := WithPathRepository(contents, "/var/www/html/pathauto"); err == nil {
			t.Errorf("%s was rewritten", name)
		}
	}
}

// Child output is untrusted: absent, null-shaped and wrongly typed all collapse
// to the same "not known".
func TestTheDescriptionNarrowsEveryShapeToNotKnown(t *testing.T) {
	description, ok := DescriptionFromJSON(`{"raw":{
		"approot":"/home/me/projects/upkeep-pathauto-d11",
		"status":"running",
		"nested":{"deep":{"value":"found"}},
		"structured":{"a":1},
		"list":[1,2],
		"nothing":null,
		"number":8080,
		"truthy":true
	}}`)
	if !ok {
		t.Fatal("a usable description was rejected")
	}

	for want, path := range map[string][]string{
		"/home/me/projects/upkeep-pathauto-d11": {"approot"},
		"running":                               {"status"},
		"found":                                 {"nested", "deep", "value"},
		"8080":                                  {"number"},
		"1":                                     {"truthy"},
	} {
		if got := description.StringOrNull(path...); got != want {
			t.Errorf("%v: got %q, want %q", path, got, want)
		}
	}

	for _, path := range [][]string{
		{"structured"}, {"list"}, {"nothing"}, {"missing"},
		{"approot", "deeper"}, {"nested", "missing"}, {"nested", "deep", "value", "deeper"},
	} {
		if got := description.StringOrNull(path...); got != "" {
			t.Errorf("%v: got %q, want nothing", path, got)
		}
	}
}

// No output, invalid JSON, or no description object are all "nothing usable".
func TestAnUnusableDescriptionIsSaidRatherThanGuessed(t *testing.T) {
	for name, output := range map[string]string{
		"nothing":      "",
		"not JSON":     "ddev: command not found",
		"a list":       `[]`,
		"no raw key":   `{"level":"error","msg":"project not found"}`,
		"raw a scalar": `{"raw":"nope"}`,
		"raw a list":   `{"raw":[]}`,
		"raw null":     `{"raw":null}`,
	} {
		if _, ok := DescriptionFromJSON(output); ok {
			t.Errorf("%s was read as a usable description", name)
		}
	}
}

// FETCH_HEAD rather than origin/<base>: `git fetch origin <base>` always
// writes it, while a remote-tracking ref is a property of how the clone was
// configured.
func TestTheCutPointIsFetchHeadWhenUpdating(t *testing.T) {
	if got := CutPoint("2.0.x", RefreshUpdate); got != "FETCH_HEAD" {
		t.Errorf("got %q", got)
	}
	if got := CutPoint("2.0.x", RefreshSkip); got != "2.0.x" {
		t.Errorf("got %q", got)
	}
}

// Skip is only reachable through the flag.
func TestOnlyTheFlagProducesASkip(t *testing.T) {
	if RefreshFromNoUpdateFlag(false) != RefreshUpdate {
		t.Error("the default is not to update")
	}
	if RefreshFromNoUpdateFlag(true) != RefreshSkip {
		t.Error("--no-update did not skip")
	}
}

// A line per run saying "up to date" is a line people stop reading, and the
// whole value of this message is that it appears exactly when something moved.
func TestNothingIsSaidWhenTheBaseWasAlreadyCurrent(t *testing.T) {
	for _, behind := range []int{0, -1} {
		if got := DescribeBaseUpdate("2.0.x", behind); got != "" {
			t.Errorf("%d behind said %q", behind, got)
		}
	}

	one := DescribeBaseUpdate("2.0.x", 1)
	if !strings.Contains(one, "1 commit behind") {
		t.Errorf("got %q, want the singular", one)
	}

	many := DescribeBaseUpdate("2.0.x", 47)
	if !strings.Contains(many, "47 commits behind") {
		t.Errorf("got %q", many)
	}
	// And it says why it matters, because the number alone is trivia.
	if !strings.Contains(many, "CI tests your work merged into this") {
		t.Errorf("got %q", many)
	}
}

// Never resolved automatically: the local commits are not in the tree being
// checked, and that is a thing a maintainer has to be told.
func TestADivergedBaseIsReportedRatherThanReset(t *testing.T) {
	message := DivergedBase("2.0.x")

	for _, want := range []string{"left alone", "origin/2.0.x", "not in what is about to be checked"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q does not carry %q", message, want)
		}
	}
}

// Everything else here degrades and says so; this one produces a verdict that
// looks exactly like a good one and gets cached as evidence the gate reads.
func TestAnUnreachableBaseRefusesAndNamesTheFlag(t *testing.T) {
	err := UnreachableBaseError("2.0.x", "fatal: could not read from remote repository")

	for _, want := range []string{
		"cannot tell what CI would test this against",
		"fatal: could not read from remote repository",
		"--no-update",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q:\n%v", want, err)
		}
	}
}

// git saying nothing is still worth reporting as git saying nothing.
func TestASilentFetchFailureStillSaysSo(t *testing.T) {
	err := UnreachableBaseError("2.0.x", "   \n  ")

	if !strings.Contains(err.Error(), "(no output from git)") {
		t.Errorf("got:\n%v", err)
	}
}

func TestASkipSaysWhatItCostsSoAStaleVerdictIsNeverSilent(t *testing.T) {
	message := SkippedBaseUpdate("2.0.x")

	if !strings.Contains(message, "--no-update") {
		t.Errorf("message %q does not say what caused it", message)
	}
	if !strings.Contains(message, "may not be what CI merges into") {
		t.Errorf("message %q does not say what it costs", message)
	}
}

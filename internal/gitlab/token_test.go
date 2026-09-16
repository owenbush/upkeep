package gitlab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolverIn builds a resolver over a token file in a fresh directory, and
// collects what it warns about.
func resolverIn(t *testing.T, contents string, mode os.FileMode) (*TokenResolver, *[]string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "drupal-pat")
	if contents != "" {
		if err := os.WriteFile(path, []byte(contents), mode); err != nil {
			t.Fatalf("write: %v", err)
		}
		// WriteFile applies the umask, so the mode is set explicitly — the
		// permission warning is the thing under test.
		if err := os.Chmod(path, mode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
	}

	warnings := &[]string{}

	return NewTokenResolver("UPKEEP_TEST_TOKEN", path, func(message string) {
		*warnings = append(*warnings, message)
	}), warnings
}

func TestTheEnvironmentOutranksTheFile(t *testing.T) {
	resolver, _ := resolverIn(t, "from-file\n", 0o600)
	t.Setenv("UPKEEP_TEST_TOKEN", "from-env")

	if got := resolver.Resolve(); got != "from-env" {
		t.Errorf("got %q, want the environment's", got)
	}
}

func TestTheFileIsUsedWhenTheEnvironmentIsEmpty(t *testing.T) {
	resolver, _ := resolverIn(t, "from-file\n", 0o600)
	t.Setenv("UPKEEP_TEST_TOKEN", "   ")

	if got := resolver.Resolve(); got != "from-file" {
		t.Errorf("got %q, want the file's", got)
	}
}

func TestNoSourceResolvesToNothing(t *testing.T) {
	resolver, _ := resolverIn(t, "", 0o600)
	t.Setenv("UPKEEP_TEST_TOKEN", "")

	if got := resolver.Resolve(); got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}

// A .netrc-style block, a trailing comment or a stray newline must not travel
// into a header value.
func TestOnlyTheFirstNonEmptyLineIsTaken(t *testing.T) {
	for _, tc := range []struct{ name, contents, want string }{
		{"trailing newline", "glpat-abc\n", "glpat-abc"},
		{"leading blank lines", "\n\n  glpat-abc  \n", "glpat-abc"},
		{"a comment after it", "glpat-abc\n# from the issue page\n", "glpat-abc"},
		{"crlf", "glpat-abc\r\nsomething else\r\n", "glpat-abc"},
		{"blank file", "   \n\n", ""},
	} {
		resolver, _ := resolverIn(t, tc.contents, 0o600)
		t.Setenv("UPKEEP_TEST_TOKEN", "")

		if got := resolver.Resolve(); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A space is legal inside a header value, so splitting on one would silently
// truncate a token rather than refuse it.
func TestASpaceInsideAValueIsKept(t *testing.T) {
	resolver, _ := resolverIn(t, "two words\n", 0o600)
	t.Setenv("UPKEEP_TEST_TOKEN", "")

	if got := resolver.Resolve(); got != "two words" {
		t.Errorf("got %q, want the whole value", got)
	}
}

// Otherwise it reaches the HTTP client as a header value and fails with an
// error naming neither the cause nor the source.
func TestAValueThatCannotBeAHeaderIsRefusedAndTheSourceNamed(t *testing.T) {
	resolver, warnings := resolverIn(t, "", 0o600)
	t.Setenv("UPKEEP_TEST_TOKEN", "glpat-\x01abc")

	if got := resolver.Resolve(); got != "" {
		t.Errorf("got %q, want a refusal", got)
	}
	if len(*warnings) != 1 {
		t.Fatalf("warnings: %v", *warnings)
	}
	warning := (*warnings)[0]
	if !strings.Contains(warning, "env var UPKEEP_TEST_TOKEN") {
		t.Errorf("warning %q does not name the source", warning)
	}
	if strings.Contains(warning, "glpat") {
		t.Fatalf("the warning quotes token material: %q", warning)
	}
}

func TestAWorldReadableTokenFileIsWarnedAboutOnceWithTheFix(t *testing.T) {
	resolver, warnings := resolverIn(t, "glpat-abc\n", 0o644)
	t.Setenv("UPKEEP_TEST_TOKEN", "")

	for range 3 {
		if got := resolver.Resolve(); got != "glpat-abc" {
			t.Fatalf("got %q", got)
		}
	}

	if len(*warnings) != 1 {
		t.Fatalf("warned %d times, want once per resolver: %v", len(*warnings), *warnings)
	}
	warning := (*warnings)[0]
	if !strings.Contains(warning, "chmod 600") {
		t.Errorf("warning %q does not name the fix", warning)
	}
	if strings.Contains(warning, "glpat") {
		t.Fatalf("the warning quotes token material: %q", warning)
	}
}

func TestAPrivateTokenFileIsNotWarnedAbout(t *testing.T) {
	resolver, warnings := resolverIn(t, "glpat-abc\n", 0o600)
	t.Setenv("UPKEEP_TEST_TOKEN", "")

	if got := resolver.Resolve(); got != "glpat-abc" {
		t.Fatalf("got %q", got)
	}
	if len(*warnings) != 0 {
		t.Errorf("warned about a 0600 file: %v", *warnings)
	}
}

func TestTheDefaultConfigFileFollowsXdg(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := DefaultConfigFile(); got != "/xdg/upkeep/drupal-pat" {
		t.Errorf("got %q", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/someone")
	if got := DefaultConfigFile(); got != "/home/someone/.config/upkeep/drupal-pat" {
		t.Errorf("got %q", got)
	}
}

// The guidance names sources so somebody can act on it, and never a value.
func TestTheGuidanceNamesTheResolversOwnSourcesNotTheDefaults(t *testing.T) {
	resolver, _ := resolverIn(t, "", 0o600)

	for _, message := range []string{MissingTokenMessage(resolver), AnonymousReadMessage(resolver)} {
		if !strings.Contains(message, "UPKEEP_TEST_TOKEN") {
			t.Errorf("message %q names the default env var, not this run's", message)
		}
		if strings.Contains(message, DefaultEnvVar) {
			t.Errorf("message %q sends somebody to a variable nothing reads", message)
		}
	}
}

// Anonymous reading is a documented degraded mode, and what it costs is worth
// saying: GitLab hides a private project's existence behind a 404.
func TestTheAnonymousNoteSaysWhatIsLost(t *testing.T) {
	resolver, _ := resolverIn(t, "", 0o600)
	message := AnonymousReadMessage(resolver)

	for _, want := range []string{"not found", "rate limits", "merge, comment or publish"} {
		if !strings.Contains(message, want) {
			t.Errorf("the note does not mention %q: %q", want, message)
		}
	}
}

func TestReadOnlyAlwaysReturnsAClientAndNotesTheDegradedMode(t *testing.T) {
	resolver, _ := resolverIn(t, "", 0o600)
	t.Setenv("UPKEEP_TEST_TOKEN", "")

	noted := []string{}
	client := ReadOnly(resolver, func(message string) { noted = append(noted, message) })

	if client == nil {
		t.Fatal("no client: reading is supposed to work without a credential")
	}
	if !client.IsAnonymous() {
		t.Error("client does not know it is anonymous")
	}
	if len(noted) != 1 {
		t.Errorf("noted %d times, want once: %v", len(noted), noted)
	}
}

func TestReadOnlySaysNothingWhenThereIsAToken(t *testing.T) {
	resolver, _ := resolverIn(t, "", 0o600)
	t.Setenv("UPKEEP_TEST_TOKEN", "glpat-abc")

	noted := []string{}
	client := ReadOnly(resolver, func(message string) { noted = append(noted, message) })

	if client.IsAnonymous() {
		t.Error("a configured token was not used")
	}
	if len(noted) != 0 {
		t.Errorf("reported a degraded mode that is not happening: %v", noted)
	}
}

// The fallback arm has to be reachable on its own, which is the whole reason
// it lives in the factory rather than at each call site.
func TestReadOnlyOrPrefersTheInjectedClient(t *testing.T) {
	resolver, _ := resolverIn(t, "", 0o600)
	t.Setenv("UPKEEP_TEST_TOKEN", "")
	injected, _ := clientWith("t")

	noted := []string{}
	got := ReadOnlyOr(injected, resolver, func(message string) { noted = append(noted, message) })

	if got != injected {
		t.Error("built a client over the injected one")
	}
	if len(noted) != 0 {
		t.Errorf("reported a degraded mode for an injected client: %v", noted)
	}

	if built := ReadOnlyOr(nil, resolver, func(string) {}); built == nil {
		t.Error("the fallback arm produced nothing")
	}
}

func TestAuthenticatedRefusesWithoutAToken(t *testing.T) {
	resolver, _ := resolverIn(t, "", 0o600)
	t.Setenv("UPKEEP_TEST_TOKEN", "")

	reported := []string{}
	client := Authenticated(resolver, func(message string) { reported = append(reported, message) })

	if client != nil {
		t.Error("built an authenticated client with no credential")
	}
	if len(reported) != 1 || !strings.Contains(reported[0], "No GitLab token found") {
		t.Errorf("reported %v", reported)
	}
}

func TestAuthenticatedBuildsAClientWithAToken(t *testing.T) {
	resolver, _ := resolverIn(t, "", 0o600)
	t.Setenv("UPKEEP_TEST_TOKEN", "glpat-abc")

	client := Authenticated(resolver, func(string) { t.Error("reported a problem there is not") })
	if client == nil || client.IsAnonymous() {
		t.Fatal("no authenticated client")
	}
}

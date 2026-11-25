package adapter

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/check"
)

// withModuleFile writes a file into the module working copy.
func withModuleFile(t *testing.T, environment Environment, name, contents string) Environment {
	t.Helper()

	path := filepath.Join(moduleWorkingCopy(environment.ProjectPath), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return environment
}

// withNodeModules puts core's linters where the probe looks.
func withNodeModules(t *testing.T, environment Environment) Environment {
	t.Helper()

	bin := filepath.Join(environment.ProjectPath, "web", "core", "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, tool := range []string{"eslint", "stylelint", "cspell"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	return environment
}

// aLintableEnvironment has node modules and a file for every job.
func aLintableEnvironment(t *testing.T) Environment {
	t.Helper()

	environment := withNodeModules(t, anEnvironment(t))
	environment = withModuleFile(t, environment, "pathauto.info.yml", "name: Pathauto\n")

	return withModuleFile(t, environment, "css/admin.css", "a {}\n")
}

func frontendScripts() map[string]string {
	module := QuoteShellArgument("/var/www/html/web/modules/contrib/pathauto")
	settings := ReadFrontendSettings(os.TempDir(), "pathauto")

	return map[string]string{
		"node modules":         NodeModulesScript(),
		"eslint":               EslintScript(module, "pathauto", "--quiet"),
		"stylelint, core's":    StylelintScript(module, "pathauto", false, ""),
		"stylelint, own":       StylelintScript(module, "pathauto", true, ""),
		"stylelint probe":      StylelintConfigProbe(module),
		"cspell":               CspellScript(module, "pathauto", settings.CspellEnvironment, ""),
		"phpstan, pinned":      PhpstanScript(module, "phpstan.neon", "", 80300),
		"phpstan, pinned dflt": PhpstanScript(module, "", "", 80300),
	}
}

// The same rule the PHP scripts are held to, for the same reason: only the
// container's own variables survive `ddev exec`. These scripts may `cd` —
// eslint, prettier and stylelint find their configuration from the working
// directory, and the directory is a scratch copy with no vendor/ to break —
// and may prefix a command with NAME=value, which sets that command's
// environment and is never read back through a $.
func TestFrontendScriptsUseOnlyTheContainersVariables(t *testing.T) {
	reference := regexp.MustCompile(`\$\{?[A-Za-z_][A-Za-z0-9_]*`)
	fromTheContainer := map[string]bool{
		"$DDEV_DOCROOT": true, "$DRUPAL_PROJECTS_PATH": true, "$DDEV_COMPOSER_ROOT": true,
	}

	for name, script := range frontendScripts() {
		for _, found := range reference.FindAllString(script, -1) {
			if !fromTheContainer[found] {
				t.Errorf("%s uses %s, which is not in the container's environment:\n  %s", name, found, script)
			}
		}
		if strings.ContainsAny(script, "\n\r") {
			t.Errorf("%s spans more than one line:\n  %s", name, script)
		}
	}
}

// Every job runs in a fresh scratch copy, never in the working copy CI's steps
// would otherwise write into.
func TestFrontendJobsRunInAScratchCopy(t *testing.T) {
	copyDir := `cd "$DDEV_COMPOSER_ROOT"/.upkeep-lint/'pathauto'`
	for name, script := range frontendScripts() {
		if !strings.HasPrefix(name, "eslint") && !strings.HasPrefix(name, "stylelint,") &&
			!strings.HasPrefix(name, "cspell") {
			continue
		}
		if !strings.HasPrefix(script, `rm -rf "$DDEV_COMPOSER_ROOT"/.upkeep-lint && mkdir -p`) {
			t.Errorf("%s does not start from a fresh copy:\n  %s", name, script)
		}
		if !strings.Contains(script, copyDir+" && ") {
			t.Errorf("%s does not run inside the copy:\n  %s", name, script)
		}
		if strings.Index(script, ".eslintignore") >= 0 && strings.Index(script, ".eslintignore") < strings.Index(script, copyDir) {
			t.Errorf("%s touches .eslintignore before entering the copy:\n  %s", name, script)
		}
	}
}

func TestEslintIsCIsJob(t *testing.T) {
	script := EslintScript("'/m'", "m", "--quiet")
	for _, want := range []string{
		`ln -s "$DDEV_COMPOSER_ROOT/$DDEV_DOCROOT"/core/.eslintrc.passing.json "$DDEV_COMPOSER_ROOT"/.upkeep-lint/.eslintrc.json`,
		`ln -s "$DDEV_COMPOSER_ROOT/$DDEV_DOCROOT"/core/.eslintrc.jquery.json "$DDEV_COMPOSER_ROOT"/.upkeep-lint/.eslintrc.jquery.json`,
		`{ grep -qF .min.js .eslintignore || echo '*.min.js' >> .eslintignore; }`,
		`{ test -e .prettierrc.json || ln -s "$DDEV_COMPOSER_ROOT/$DDEV_DOCROOT"/core/.prettierrc.json .; }`,
		`{ test -e .prettierignore || echo '*.yml' > .prettierignore; }`,
		`/core/node_modules/.bin/eslint --no-error-on-unmatched-pattern --ignore-pattern='*.es6.js' ` +
			`--resolve-plugins-relative-to="$DDEV_COMPOSER_ROOT/$DDEV_DOCROOT"/core --ext=.js,.yml --quiet .`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("missing %q from:\n  %s", want, script)
		}
	}
}

// The glob is unquoted so the inner shell expands it after the cd, as CI's
// does: without globstar that is CSS exactly one directory down.
func TestStylelintIsCIsJob(t *testing.T) {
	core := StylelintScript("'/m'", "m", false, "--quiet")
	if !strings.HasSuffix(core, `--ignore-path ./.stylelintignore --config "$DDEV_COMPOSER_ROOT/$DDEV_DOCROOT"/core/.stylelintrc.json ./**/*.css --formatter verbose --quiet`) {
		t.Errorf("core's config is not the default:\n  %s", core)
	}
	if own := StylelintScript("'/m'", "m", true, ""); strings.Contains(own, "--config") {
		t.Errorf("the module's own config was overridden:\n  %s", own)
	}
	if !strings.Contains(core, "|| ln -s ") || !strings.Contains(core, "/core/.prettierrc.json .; }") {
		t.Errorf("prettier is not configured for core's stylelint config:\n  %s", core)
	}
}

func TestCspellIsCIsJob(t *testing.T) {
	script := CspellScript("'/m'", "m", []string{"A='1'"}, "")
	for _, want := range []string{
		"{ test -f .cspell.json || curl -sSfL " + templatesBase + "/.cspell.json -o .cspell.json; }",
		"curl -sSfL " + scriptsBase + `/prepare-cspell.php -o "$DDEV_COMPOSER_ROOT"/.upkeep-lint/prepare-cspell.php`,
		`env A='1' php "$DDEV_COMPOSER_ROOT"/.upkeep-lint/prepare-cspell.php`,
		"/core/node_modules/.bin/cspell -c .cspell.json --show-suggestions --show-context --no-progress .",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("missing %q from:\n  %s", want, script)
		}
	}
}

func TestFrontendSettingsDefaultToTheTemplates(t *testing.T) {
	settings := ReadFrontendSettings(t.TempDir(), "pathauto")

	if settings.Extra[check.EsLint] != "--quiet" || settings.Extra[check.StyleLint] != "" {
		t.Errorf("extras: %v", settings.Extra)
	}
	for _, one := range FrontendChecks {
		if settings.Skipped[one] {
			t.Errorf("%s skipped by default", one)
		}
	}
	joined := strings.Join(settings.CspellEnvironment, " ")
	for _, want := range []string{
		"_CSPELL_DICTIONARY='.cspell-project-words.txt'", "_CSPELL_MINWORDLENGTH='4'",
		"_CSPELL_IGNORE_STANDARD_FILES='1'", "CI_PROJECT_NAME='pathauto'", `_WEB_ROOT=../../"$DDEV_DOCROOT"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s from %s", want, joined)
		}
	}
}

func TestFrontendSettingsComeFromTheModulesPipeline(t *testing.T) {
	dir := t.TempDir()
	pipeline := `variables:
  SKIP_STYLELINT: '1'
  _ESLINT_EXTRA: ''
  _STYLELINT_EXTRA: {value: '--fix; rm -rf /'}
  _CSPELL_WORDS: {value: "'drupalism', 'it''s'", description: words}
  _CSPELL_EXTRA: [not, a, scalar]
`
	if err := os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), []byte(pipeline), 0o644); err != nil {
		t.Fatal(err)
	}

	settings := ReadFrontendSettings(dir, "m")
	if !settings.Skipped[check.StyleLint] || settings.Skipped[check.EsLint] {
		t.Errorf("skips: %v", settings.Skipped)
	}
	// Declared empty means warnings, not the template's --quiet.
	if extra, present := settings.Extra[check.EsLint]; !present || extra != "" {
		t.Errorf("a declared-empty _ESLINT_EXTRA was not honoured: %q", extra)
	}
	if len(settings.Rejected) != 1 || settings.Rejected[0] != "_STYLELINT_EXTRA" {
		t.Errorf("rejected: %v", settings.Rejected)
	}
	if _, present := settings.Extra[check.StyleLint]; present {
		t.Error("a rejected extra was used")
	}
	// A non-scalar is not a value; the default stands.
	if settings.Extra[check.CSpell] != "" {
		t.Errorf("cspell extra: %q", settings.Extra[check.CSpell])
	}
	// Data, so quoted rather than refused.
	if !strings.Contains(strings.Join(settings.CspellEnvironment, " "),
		`_CSPELL_WORDS=''\''drupalism'\'', '\''it'\'''\''s'\'''`) {
		t.Errorf("words were not passed quoted: %v", settings.CspellEnvironment)
	}

	if err := os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), []byte("variables: [unparseable"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ReadFrontendSettings(dir, "m").Extra[check.EsLint] != "--quiet" {
		t.Error("an unparseable pipeline did not fall back to the defaults")
	}
}

func TestHasFilesForFollowsCIsRules(t *testing.T) {
	dir := t.TempDir()
	if HasFilesFor(dir, check.EsLint) || HasFilesFor(dir, check.StyleLint) {
		t.Error("an empty module has files to lint")
	}
	if !HasFilesFor(dir, check.CSpell) {
		t.Error("cspell has no file rule in CI")
	}
	// Under .git does not count.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "x.css"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if HasFilesFor(dir, check.StyleLint) {
		t.Error("a file under .git counted")
	}
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "b", "x.yml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !HasFilesFor(dir, check.EsLint) || HasFilesFor(dir, check.StyleLint) {
		t.Error("a nested yml file did not count for eslint alone")
	}
	if HasFilesFor(filepath.Join(dir, "missing"), check.EsLint) {
		t.Error("a missing directory has files")
	}
}

// The default suite runs CI's front-end jobs, each as CI's script in the
// scratch copy.
func TestTheFrontendJobsRunAsCIRunsThem(t *testing.T) {
	runner := newRunner()
	result, err := engineWith(runner).RunChecks(aLintableEnvironment(t), FrontendChecks)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for i, one := range FrontendChecks {
		if result.Results[i].Status != check.Passed {
			t.Errorf("%s: %q %s", one, result.Results[i].Status, result.Results[i].Output)
		}
		if !runner.didRun("ddev exec bash -c", ".upkeep-lint", "node_modules/.bin/"+string(one)) {
			t.Errorf("%s did not run CI's job:\n%s", one, runner.transcript())
		}
	}
	if runner.didRun("yarn install") {
		t.Errorf("node modules were reinstalled when present:\n%s", runner.transcript())
	}
	for _, one := range FrontendChecks {
		if !slicesContainsType(DefaultChecks, one) {
			t.Errorf("%s is not in the default suite", one)
		}
	}
}

func slicesContainsType(types []check.Type, one check.Type) bool {
	for _, each := range types {
		if each == one {
			return true
		}
	}

	return false
}

func TestStylelintUsesTheModulesOwnConfigWhenItShipsOne(t *testing.T) {
	runner := newRunner()
	runner.answer("grep -qE '^[.]stylelintrc", "")
	if _, err := engineWith(runner).RunChecks(aLintableEnvironment(t), []check.Type{check.StyleLint}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if runner.didRun("node_modules/.bin/stylelint", "--config") {
		t.Errorf("core's config overrode the module's:\n%s", runner.transcript())
	}

	runner = newRunner().fails("grep -qE '^[.]stylelintrc")
	if _, err := engineWith(runner).RunChecks(aLintableEnvironment(t), []check.Type{check.StyleLint}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !runner.didRun("node_modules/.bin/stylelint", "--config", ".stylelintrc.json") {
		t.Errorf("core's config was not the fallback:\n%s", runner.transcript())
	}
}

// Missing node modules are installed once for the run, before the first job.
func TestNodeModulesAreInstalledOnceWhenMissing(t *testing.T) {
	environment := withModuleFile(t, anEnvironment(t), "x.info.yml", "")
	environment = withModuleFile(t, environment, "x.css", "")

	runner := newRunner()
	engine, said := logging(nil, runner)
	if _, err := engine.RunChecks(environment, FrontendChecks); err != nil {
		t.Fatalf("run: %v", err)
	}

	if strings.Count(runner.transcript(), "yarn install") != 1 {
		t.Errorf("node modules were not installed exactly once:\n%s", runner.transcript())
	}
	if install := runner.indexOfCommand("yarn install"); install < 0 || install > runner.indexOfCommand("bin/eslint") {
		t.Errorf("the install did not come first:\n%s", runner.transcript())
	}
	if !strings.Contains(strings.Join(*said, "\n"), "Installing core's node modules") {
		t.Errorf("the install was not announced: %v", *said)
	}
}

// A failed install fails every job that would have run — never unavailable,
// which passes, because CI would have run them.
func TestAFailedNodeInstallFailsTheJobs(t *testing.T) {
	environment := withModuleFile(t, anEnvironment(t), "x.info.yml", "")
	environment = withModuleFile(t, environment, "x.css", "")

	runner := newRunner().fails("yarn install")
	result, err := engineWith(runner).RunChecks(environment, FrontendChecks)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, each := range result.Results {
		if each.Status != check.Failed || !strings.Contains(each.Output, "could not be installed") {
			t.Errorf("%s: %q %s", each.Type, each.Status, each.Output)
		}
	}
	if runner.didRun("bin/eslint") {
		t.Errorf("a job ran without node modules:\n%s", runner.transcript())
	}
}

func TestSkippedAndFilelessJobsDoNotRun(t *testing.T) {
	environment := withNodeModules(t, anEnvironment(t))
	environment = withModuleFile(t, environment, ".gitlab-ci.yml", "variables:\n  SKIP_CSPELL: '1'\n")

	runner := newRunner()
	result, err := engineWith(runner).RunChecks(environment, FrontendChecks)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// The pipeline file is yml, so eslint has a file; stylelint has none.
	want := map[check.Type]check.Status{
		check.EsLint: check.Passed, check.StyleLint: check.NoTests, check.CSpell: check.Unavailable,
	}
	for _, each := range result.Results {
		if each.Status != want[each.Type] {
			t.Errorf("%s: %q, want %q (%s)", each.Type, each.Status, want[each.Type], each.Output)
		}
	}
	if !strings.Contains(result.Results[2].Output, "SKIP_CSPELL=1") {
		t.Errorf("the skip does not say why: %s", result.Results[2].Output)
	}
	if runner.didRun("bin/stylelint") || runner.didRun("bin/cspell") {
		t.Errorf("a job CI would not run was run:\n%s", runner.transcript())
	}
}

func TestRejectedFrontendExtrasAreReported(t *testing.T) {
	environment := withModuleFile(t, aLintableEnvironment(t), ".gitlab-ci.yml",
		"variables:\n  _ESLINT_EXTRA: '--quiet; true'\n")

	runner := newRunner()
	engine, said := logging(nil, runner)
	if _, err := engine.RunChecks(environment, []check.Type{check.EsLint}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(strings.Join(*said, "\n"), "Ignoring _ESLINT_EXTRA") {
		t.Errorf("the rejection was not reported: %v", *said)
	}
	if runner.didRun("true .") {
		t.Errorf("the rejected value reached the command:\n%s", runner.transcript())
	}
}

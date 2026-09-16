package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/check"
)

// withToolchain puts the check binaries where the probe looks, so a test about
// running checks is not really a test about provisioning one.
func withToolchain(t *testing.T, environment Environment) Environment {
	t.Helper()

	bin := filepath.Join(environment.ProjectPath, "vendor", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, binary := range []string{"phpunit", "phpstan", "phpcs"} {
		if err := os.WriteFile(filepath.Join(bin, binary), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	return environment
}

// artifactsFor is a base-artifacts directory holding one core's meta, which is
// where check provisioning reads the seeded core version from.
func artifactsFor(t *testing.T, coreMajor, coreVersion string) *baseartifact.Layout {
	t.Helper()

	layout := baseartifact.NewLayout(t.TempDir())
	metaPath, err := layout.MetaPath(coreMajor)
	if err != nil {
		t.Fatalf("meta path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	meta := baseartifact.Meta{
		CoreVersion: coreVersion, CoreMajor: coreMajor,
		PHPVersion: "8.3", DBEngine: "mariadb:10.11", BuiltAt: time.Now(),
	}
	contents, err := meta.ToYAML()
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if err := os.WriteFile(metaPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return layout
}

// logging is an engine that keeps what it told the operator.
func logging(layout *baseartifact.Layout, runner *recordingRunner) (*DdevContrib, *[]string) {
	said := &[]string{}

	return NewDdevContrib(layout, "/projects", runner, func(line string) {
		*said = append(*said, line)
	}), said
}

// Every check that runs, and nothing else — a suite that silently skipped one
// would report green over something that never ran.
func TestTheDefaultSuiteRunsEveryCheckItNames(t *testing.T) {
	runner := newRunner()
	environment := withToolchain(t, anEnvironment(t))

	result, err := engineWith(runner).RunChecks(environment, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Results) != len(DefaultChecks) {
		t.Fatalf("ran %d checks for %d named", len(result.Results), len(DefaultChecks))
	}
	for i, one := range DefaultChecks {
		if result.Results[i].Type != one {
			t.Errorf("check %d is %q, want %q", i, result.Results[i].Type, one)
		}
	}
}

// Naming checks runs exactly those.
func TestNamingChecksRunsOnlyThose(t *testing.T) {
	runner := newRunner()
	environment := withToolchain(t, anEnvironment(t))

	result, err := engineWith(runner).RunChecks(environment, []check.Type{check.ModuleInstall})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Results) != 1 || result.Results[0].Type != check.ModuleInstall {
		t.Fatalf("got %+v", result.Results)
	}
	if runner.didRun("phpunit") || runner.didRun("phpcs") {
		t.Errorf("it ran checks nobody asked for:\n%s", runner.transcript())
	}
}

// The toolchain is provisioned only when a check in the run needs it.
func TestTheToolchainIsProvisionedOnlyForTheChecksThatNeedIt(t *testing.T) {
	layout := artifactsFor(t, "11", "11.4.6")
	// No binaries on disk, so provisioning would run if anything asked for it.
	environment := anEnvironment(t)

	runner := newRunner()
	engine := NewDdevContrib(layout, "/projects", runner, nil)
	if _, err := engine.RunChecks(
		environment, []check.Type{check.ModuleInstall, check.FunctionalSmoke},
	); err != nil {
		t.Fatalf("run: %v", err)
	}
	if runner.didRun("composer require --dev") {
		t.Errorf("the toolchain was provisioned for checks that do not use it:\n%s", runner.transcript())
	}

	runner = newRunner()
	engine = NewDdevContrib(layout, "/projects", runner, nil)
	if _, err := engine.RunChecks(environment, []check.Type{check.PhpCs}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !runner.didRun("composer require --dev", "drupal/core-dev") {
		t.Errorf("the toolchain was not provisioned for a check that needs it:\n%s", runner.transcript())
	}
	// The composer plugin the coder install needs is allowed first, or the
	// require is refused non-interactively.
	allow := runner.indexOfCommand("allow-plugins.dealerdirect/phpcodesniffer-composer-installer")
	if allow < 0 || allow > runner.indexOfCommand("composer require --dev") {
		t.Errorf("the phpcs installer plugin was not allowed first:\n%s", runner.transcript())
	}
}

// The stability comes from the tree being installed into, not from a second
// flag that could disagree with it: drupal/core-dev:^12 resolves to nothing
// while 12 is in alpha, which is exactly when compatibility work happens.
func TestTheToolchainFollowsTheSeededCoresStability(t *testing.T) {
	for coreVersion, want := range map[string]string{
		"12.0.0-alpha1": "drupal/core-dev:^12@alpha",
		"12.0.0":        "drupal/core-dev:^12",
	} {
		runner := newRunner()
		environment := anEnvironment(t)
		environment.CoreMajor = "12"

		engine := NewDdevContrib(artifactsFor(t, "12", coreVersion), "/projects", runner, nil)
		if _, err := engine.RunChecks(environment, []check.Type{check.PhpStan}); err != nil {
			t.Fatalf("run: %v", err)
		}

		if !runner.didRun("composer require --dev", want) {
			t.Errorf("core %s did not ask for %q:\n%s", coreVersion, want, runner.transcript())
		}
		// The stability belongs to core's constraint alone — coder@alpha would
		// admit an alpha of a package with nothing to do with the seeded core.
		if runner.didRun("drupal/coder@") {
			t.Errorf("a stability flag leaked onto an unrelated package:\n%s", runner.transcript())
		}
	}
}

// With no artifact for the core there is nothing to derive a toolchain from,
// and the refusal names how to build one.
func TestProvisioningRefusesWithoutABaseArtifactAndSaysHowToBuildOne(t *testing.T) {
	runner := newRunner()
	engine := NewDdevContrib(baseartifact.NewLayout(t.TempDir()), "/projects", runner, nil)

	_, err := engine.RunChecks(anEnvironment(t), []check.Type{check.PhpUnit})
	if err == nil {
		t.Fatal("it provisioned a toolchain with no artifact to derive it from")
	}
	if !strings.Contains(err.Error(), "base-artifacts:build --version=11") {
		t.Errorf("the refusal does not name the recovery: %v", err)
	}
	if runner.didRun("composer require --dev") {
		t.Errorf("it required packages anyway:\n%s", runner.transcript())
	}
}

// Checks target exactly the module under maintenance — never all of
// DRUPAL_PROJECTS_PATH, where composer also materialises the module's real
// dependencies, whose packaged code must not pollute the results.
func TestTheChecksTargetOnlyTheModule(t *testing.T) {
	runner := newRunner()
	environment := withToolchain(t, anEnvironment(t))

	if _, err := engineWith(runner).RunChecks(
		environment, []check.Type{check.PhpCs, check.PhpStan, check.PhpUnit},
	); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, line := range runner.ran {
		if !strings.Contains(line, "pathauto") {
			t.Errorf("a check did not scope itself to the module: %s", line)
		}
	}
	// The module name is spliced into a shell script body, so its safety must
	// not depend on a validator two packages away.
	if !runner.didRun("bash -c", QuoteShellArgument("pathauto")) {
		t.Errorf("the module name was not quoted into the script:\n%s", runner.transcript())
	}
}

// A module's own configuration is used and the gitlab_templates default is
// only the fallback, which is the behaviour CI has and upkeep did not.
func TestAModulesOwnRulesetIsProbedInPrecedenceOrderAndUsed(t *testing.T) {
	runner := newRunner()
	// It ships the second candidate, not the first.
	runner.answer("phpcs.xml.dist", "")

	engine, said := logging(nil, runner)
	if _, err := engine.RunChecks(
		withToolchain(t, anEnvironment(t)), []check.Type{check.PhpCs},
	); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(strings.Join(*said, "\n"), "own PHPCS ruleset (phpcs.xml.dist)") {
		t.Errorf("it did not report using the module's own:\n%s", strings.Join(*said, "\n"))
	}
	// Probed in the tool's own precedence order, and it stopped at the match.
	first, second := runner.indexOfCommand("test -f"), runner.indexOfCommand("phpcs.xml.dist")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("the candidates were not probed in order:\n%s", runner.transcript())
	}
	if runner.didRun(".phpcs.xml") {
		t.Errorf("it kept probing after it had its answer:\n%s", runner.transcript())
	}
	// And the name reaches the tool, rather than being discovered there.
	if !runner.didRun("phpcs ", "--standard=", "/phpcs.xml.dist ") {
		t.Errorf("the ruleset was not named to phpcs:\n%s", runner.transcript())
	}
}

// A module that ships none falls back, and says so.
func TestAModuleWithNoConfigurationFallsBackAndSaysSo(t *testing.T) {
	runner := newRunner()

	engine, said := logging(nil, runner)
	if _, err := engine.RunChecks(
		withToolchain(t, anEnvironment(t)), []check.Type{check.PhpStan},
	); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(strings.Join(*said, "\n"), "ships no PHPStan configuration") {
		t.Errorf("the fallback was silent:\n%s", strings.Join(*said, "\n"))
	}
	// Every candidate was tried before giving up.
	for _, candidate := range PhpstanConfigs {
		if !runner.didRun("test -f", candidate) {
			t.Errorf("it gave up before probing %s:\n%s", candidate, runner.transcript())
		}
	}
	// And the fallback fetches the template rather than inventing one.
	if !runner.didRun("gitlab_templates") {
		t.Errorf("the default configuration was not fetched:\n%s", runner.transcript())
	}
}

// The outcome is data: a non-zero exit is a failed result, not an error that
// takes the run down.
func TestAFailingCheckIsAResultRatherThanAnError(t *testing.T) {
	runner := newRunner()
	runner.fails("drush pm:install")
	runner.answer("drush pm:install", "The module could not be installed.\n")

	result, err := engineWith(runner).RunChecks(
		anEnvironment(t), []check.Type{check.ModuleInstall},
	)
	if err != nil {
		t.Fatalf("a failing check became an error: %v", err)
	}

	if len(result.Results) != 1 || result.Results[0].Status != check.Failed {
		t.Fatalf("got %+v", result.Results)
	}
	if result.AllPassed() {
		t.Error("a failing check read as green")
	}
	if !strings.Contains(result.Results[0].Output, "could not be installed") {
		t.Errorf("the output was lost: %q", result.Results[0].Output)
	}
}

// A check that runs past its timebox is a timed-out result with a reason,
// rather than a hang or a failure indistinguishable from a red suite.
func TestACheckThatRunsPastItsTimeboxIsSaidToHaveTimedOut(t *testing.T) {
	runner := newRunner().stalls("phpunit")
	environment := withToolchain(t, anEnvironment(t))

	result, err := engineWith(runner).RunChecks(environment, []check.Type{check.PhpUnit})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Results[0].Status != check.Failed {
		t.Errorf("status %q", result.Results[0].Status)
	}
	if !strings.Contains(result.Results[0].Output, "imed out") {
		t.Errorf("the result does not say it timed out: %q", result.Results[0].Output)
	}
}

// The front page over HTTP with the module enabled, passing only on 200.
func TestTheSmokeCheckPassesOnlyOnTwoHundred(t *testing.T) {
	environment := anEnvironment(t)
	environment.PrimaryURL = "https://upkeep-pathauto-d11.ddev.site"

	// 204 and 302 are here because "a 2xx is fine" and "a redirect is fine"
	// are the two plausible loosenings, and neither is: the front page
	// answering anything but 200 with the module enabled is the failure this
	// check exists to catch.
	for code, want := range map[string]check.Status{
		"200": check.Passed,
		"204": check.Failed,
		"302": check.Failed,
		"404": check.Failed,
		"500": check.Failed,
	} {
		runner := newRunner().answer("curl", code)

		result, err := engineWith(runner).RunChecks(environment, []check.Type{check.FunctionalSmoke})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if result.Results[0].Status != want {
			t.Errorf("HTTP %s gave %q, want %q", code, result.Results[0].Status, want)
		}
		if !strings.Contains(result.Results[0].Output, code) {
			t.Errorf("HTTP %s: the output does not say what came back: %q", code, result.Results[0].Output)
		}
	}
}

// A request that never completed is said, rather than scored as a status
// nobody received.
func TestASmokeRequestThatFailsIsSaidRatherThanScored(t *testing.T) {
	runner := newRunner()
	runner.fails("curl")
	runner.answer("curl", "curl: (7) Failed to connect")

	result, err := engineWith(runner).RunChecks(
		anEnvironment(t), []check.Type{check.FunctionalSmoke},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Results[0].Status != check.Failed {
		t.Errorf("status %q", result.Results[0].Status)
	}
	if !strings.Contains(result.Results[0].Output, "Request failed") {
		t.Errorf("output %q does not say the request failed", result.Results[0].Output)
	}
}

// The pinned engine add-on ships no upgrade-status command, and that is
// recorded explicitly rather than silently omitted.
func TestAnUnavailableCheckIsRecordedNotOmitted(t *testing.T) {
	runner := newRunner()

	result, err := engineWith(runner).RunChecks(anEnvironment(t), []check.Type{check.Deprecation})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Results) != 1 {
		t.Fatalf("got %d results", len(result.Results))
	}
	if result.Results[0].Status != check.Unavailable {
		t.Errorf("status %q, want unavailable", result.Results[0].Status)
	}
	// An honest non-failure: it does not block a green verdict.
	if !result.AllPassed() {
		t.Error("an unavailable check blocked a green verdict")
	}
	if !strings.Contains(result.Results[0].Output, EngineAddOnVersion) {
		t.Errorf("the reason does not name the pinned add-on: %q", result.Results[0].Output)
	}
	if runner.didRun("upgrade-status") {
		t.Errorf("it ran a command that is not there:\n%s", runner.transcript())
	}
}

// And when the engine does provide it, it runs.
func TestTheDeprecationCheckRunsWhenTheEngineProvidesIt(t *testing.T) {
	environment := anEnvironment(t)
	commandDir := filepath.Join(environment.ProjectPath, ".ddev", "commands", "web")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(commandDir, "upgrade-status"), []byte("#!/bin/sh\n"), 0o755,
	); err != nil {
		t.Fatalf("write: %v", err)
	}

	runner := newRunner().answer("upgrade-status", "No deprecations found.\n")
	result, err := engineWith(runner).RunChecks(environment, []check.Type{check.Deprecation})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Results[0].Status != check.Passed {
		t.Errorf("status %q", result.Results[0].Status)
	}
	if !runner.didRun("ddev upgrade-status") {
		t.Errorf("it did not run the command:\n%s", runner.transcript())
	}
}

// A check type the adapter has no arm for is a gap in the adapter, and a run
// that silently skipped it would report a green suite that never ran it.
func TestACheckTheEngineCannotRunIsSaidRatherThanSkipped(t *testing.T) {
	runner := newRunner()

	result, err := engineWith(runner).RunChecks(
		anEnvironment(t), []check.Type{check.Type("quantum_lint")},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Results) != 1 {
		t.Fatalf("the check was skipped entirely: %+v", result.Results)
	}
	if result.Results[0].Status != check.Unavailable {
		t.Errorf("status %q", result.Results[0].Status)
	}
	if !strings.Contains(result.Results[0].Output, "quantum_lint") {
		t.Errorf("the reason does not name the check: %q", result.Results[0].Output)
	}
	if len(runner.ran) != 0 {
		t.Errorf("it ran something for a check it cannot run:\n%s", runner.transcript())
	}
}

// withManifest gives the module working copy a composer.json.
func withManifest(t *testing.T, environment Environment, manifest string) Environment {
	t.Helper()

	path := filepath.Join(moduleWorkingCopy(environment.ProjectPath), "composer.json")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return environment
}

// Honouring a module's ruleset means installing what it references, and those
// are the *module's* packages: composer never installs a path dependency's
// require-dev. A directory test on the host, so a reused environment costs
// nothing to re-verify.
func TestOnlyMissingDevRequirementsAreInstalled(t *testing.T) {
	environment := withManifest(t, withToolchain(t, anEnvironment(t)),
		`{"require-dev":{"phpcompatibility/php-compatibility":"^9","drupal/coder":"^8","php":"^8.2"}}`)

	// One of the two is already there.
	if err := os.MkdirAll(
		filepath.Join(environment.ProjectPath, "vendor", "drupal", "coder"), 0o755,
	); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	runner := newRunner()
	if _, err := engineWith(runner).RunChecks(environment, []check.Type{check.PhpCs}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !runner.didRun("composer require --dev", "phpcompatibility/php-compatibility") {
		t.Errorf("the missing dependency was not installed:\n%s", runner.transcript())
	}
	for _, line := range runner.ran {
		if strings.Contains(line, "composer require --dev") && strings.Contains(line, "drupal/coder") {
			t.Errorf("an already-present dependency was reinstalled: %s", line)
		}
		// php is not a package; asking composer to require it would fail a
		// provision over something no install can satisfy.
		if strings.Contains(line, "composer require --dev") && strings.Contains(line, " php ") {
			t.Errorf("a platform requirement was required as a package: %s", line)
		}
	}
}

// A freshly provisioned environment gets them along with the toolchain.
func TestDevRequirementsAreInstalledAlongsideAFreshToolchain(t *testing.T) {
	// No binaries on disk, so this is the provisioning path.
	environment := withManifest(t, anEnvironment(t),
		`{"require-dev":{"phpcompatibility/php-compatibility":"^9"}}`)

	runner := newRunner()
	engine := NewDdevContrib(artifactsFor(t, "11", "11.4.6"), "/projects", runner, nil)
	if _, err := engine.RunChecks(environment, []check.Type{check.PhpCs}); err != nil {
		t.Fatalf("run: %v", err)
	}

	toolchain := runner.indexOfCommand("composer require --dev", "drupal/core-dev")
	module := runner.indexOfCommand("composer require --dev", "phpcompatibility/php-compatibility")
	if toolchain < 0 || module < 0 {
		t.Fatalf("missing installs:\n%s", runner.transcript())
	}
	// The module's own, after the toolchain and separately from it: a conflict
	// in a linting dependency must not take the toolchain down with it.
	if module <= toolchain {
		t.Errorf("the module's dev dependencies went in with the toolchain:\n%s", runner.transcript())
	}
}

// The toolchain gate returns early once the binaries are in vendor/bin, and
// putting this inside that block made it do nothing on every *existing*
// environment — which is every environment after the first.
func TestDevRequirementsAreInstalledEvenWhenTheToolchainIsAlreadyThere(t *testing.T) {
	environment := withManifest(t, withToolchain(t, anEnvironment(t)),
		`{"require-dev":{"phpcompatibility/php-compatibility":"^9"}}`)

	runner := newRunner()
	if _, err := engineWith(runner).RunChecks(environment, []check.Type{check.PhpCs}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !runner.didRun("composer require --dev", "phpcompatibility/php-compatibility") {
		t.Errorf(
			"an environment with the toolchain already present never got the module's own "+
				"dev dependencies:\n%s",
			runner.transcript(),
		)
	}
	// And it did not reprovision the toolchain to get there.
	if runner.didRun("drupal/core-dev") {
		t.Errorf("the toolchain was reprovisioned:\n%s", runner.transcript())
	}
}

// Failure is a warning, not a refusal: a version conflict here would take down
// phpunit, the install check and the smoke test over a linting dependency.
func TestDevRequirementsThatWillNotInstallAreWarnedAboutNotRefused(t *testing.T) {
	environment := withManifest(t, withToolchain(t, anEnvironment(t)),
		`{"require-dev":{"phpcompatibility/php-compatibility":"^9"}}`)

	runner := newRunner()
	runner.fails("composer require --dev")

	engine, said := logging(nil, runner)
	result, err := engine.RunChecks(environment, []check.Type{check.PhpCs})
	if err != nil {
		t.Fatalf("a failed dev-dependency install refused the run: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("the check did not run: %+v", result.Results)
	}
	if !strings.Contains(strings.Join(*said, "\n"), "may reference a sniff or extension that is now missing") {
		t.Errorf("the warning was not given:\n%s", strings.Join(*said, "\n"))
	}
}

// A module with no composer.json, or a malformed one, is not a reason to
// refuse to check it.
func TestAModuleWithNoManifestIsStillChecked(t *testing.T) {
	runner := newRunner()

	result, err := engineWith(runner).RunChecks(
		withToolchain(t, anEnvironment(t)), []check.Type{check.PhpCs},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("the check did not run: %+v", result.Results)
	}
	if runner.didRun("composer require --dev") {
		t.Errorf("it required something out of a manifest that is not there:\n%s", runner.transcript())
	}
}

// The meta is written as the last provisioning step, so a directory without
// one is a partial provision and must never be reused.
func TestAPartialProvisionIsNotAnEnvironment(t *testing.T) {
	root := t.TempDir()
	engine := NewDdevContrib(nil, root, newRunner(), nil)

	projectPath := filepath.Join(root, "upkeep-pathauto-d11")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if got := engine.ResolveEnvPath("pathauto", "11"); got != "" {
		t.Errorf("a directory with no meta resolved to %q", got)
	}
	if _, found := engine.InspectWorkingCopy("pathauto", "11"); found {
		t.Error("a partial provision was inspected as an environment")
	}

	// With the marker, it is one.
	meta := EnvironmentMeta{
		ModuleName: "pathauto", CoreMajor: "11", SeedCoreVersion: "11.4.6",
		AddOnVersion: EngineAddOnVersion, CreatedAt: time.Now(),
	}
	if err := meta.WriteTo(projectPath); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := engine.ResolveEnvPath("pathauto", "11"); got != projectPath {
		t.Errorf("got %q, want %q", got, projectPath)
	}
	if _, found := engine.InspectWorkingCopy("pathauto", "11"); !found {
		t.Error("a completed environment was not inspected")
	}
}

// A name that cannot be an engine project resolves to nothing, rather than to
// a path assembled from it.
func TestAnImpossibleModuleNameResolvesToNothing(t *testing.T) {
	engine := NewDdevContrib(nil, t.TempDir(), newRunner(), nil)

	for _, name := range []string{"../escape", "Path-Auto", ""} {
		if got := engine.ResolveEnvPath(name, "11"); got != "" {
			t.Errorf("%q resolved to %q", name, got)
		}
	}
	if got := engine.ResolveEnvPath("pathauto", "11.2"); got != "" {
		t.Errorf("a core that is not a major resolved to %q", got)
	}
}

// The concrete hazard behind the refusal above: a name that cannot be a
// project name joins onto nothing, so ignoring the error would hand back the
// projects root itself as an environment — and every later step would run
// against the directory holding all of them.
func TestAnImpossibleModuleNameNeverResolvesToTheProjectsRoot(t *testing.T) {
	root := t.TempDir()
	meta := EnvironmentMeta{
		ModuleName: "pathauto", CoreMajor: "11", SeedCoreVersion: "11.4.6",
		AddOnVersion: EngineAddOnVersion, CreatedAt: time.Now(),
	}
	if err := meta.WriteTo(root); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := NewDdevContrib(nil, root, newRunner(), nil).ResolveEnvPath("Path-Auto", "11"); got != "" {
		t.Errorf("an impossible name resolved to the projects root itself: %q", got)
	}
}

// Serving installs the module and hands back a login when the engine can mint
// one.
func TestServingInstallsTheModuleAndMintsALoginWhenItCan(t *testing.T) {
	environment := anEnvironment(t)
	environment.PrimaryURL = "https://upkeep-pathauto-d11.ddev.site"

	runner := newRunner().answer("drush uli", environment.PrimaryURL+"/user/reset/1/x\n")

	result, err := engineWith(runner).Serve(environment)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if result.URL != environment.PrimaryURL {
		t.Errorf("url %q", result.URL)
	}
	if result.LoginURL != environment.PrimaryURL+"/user/reset/1/x" {
		t.Errorf("login %q", result.LoginURL)
	}
	if !runner.didRun("drush pm:install pathauto -y") {
		t.Errorf("the module was not installed:\n%s", runner.transcript())
	}
}

// A login the engine cannot mint is absent, rather than an empty string that
// reads as one.
//
// The guard in Serve is an equivalent mutant against this: proc.Runner.TryRun
// returns "" on a failed command (pinned by internal/proc's own test), so
// assigning unconditionally would assign "" anyway. It stays because it states
// what the code means rather than leaning on another package's return value,
// and this test pins the property whichever way that is spelled.
func TestServingWithoutALoginStillHandsBackTheUrl(t *testing.T) {
	environment := anEnvironment(t)
	environment.PrimaryURL = "https://upkeep-pathauto-d11.ddev.site"

	runner := newRunner()
	runner.fails("drush uli")

	result, err := engineWith(runner).Serve(environment)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if result.LoginURL != "" {
		t.Errorf("login %q, want none", result.LoginURL)
	}
	if result.URL != environment.PrimaryURL {
		t.Errorf("url %q", result.URL)
	}
}

// A module that will not install is a refusal, since there is nothing to open.
func TestServingRefusesWhenTheModuleWillNotInstall(t *testing.T) {
	runner := newRunner()
	runner.fails("drush pm:install")

	if _, err := engineWith(runner).Serve(anEnvironment(t)); err == nil {
		t.Fatal("it handed back a URL for a module that is not installed")
	}
}

// The add-on command owns all fixture resolution and load semantics; the
// adapter only invokes it, and only after making sure it is there.
func TestLoadingAFixtureGoesThroughTheAddOnsOwnCommand(t *testing.T) {
	runner := newRunner()
	environment := anEnvironment(t)

	if err := engineWith(runner).LoadFixture(environment, "baseline"); err != nil {
		t.Fatalf("load: %v\n%s", err, runner.transcript())
	}

	install := runner.indexOfCommand("add-on get")
	load := runner.indexOfCommand(FixtureLoadCommand, "baseline")
	if install < 0 || load < 0 {
		t.Fatalf("missing steps:\n%s", runner.transcript())
	}
	if install > load {
		t.Errorf("it loaded before installing the add-on:\n%s", runner.transcript())
	}
	// Pinned, as the engine add-on is: unpinned, every environment tracks
	// whatever the latest release happens to be.
	if !runner.didRun("add-on get", "--version", FixtureAddOnVersion) {
		t.Errorf("the add-on release was not pinned:\n%s", runner.transcript())
	}
}

// A fixture that will not load is a refusal — the environment now holds
// whatever half a load left behind.
func TestAFixtureThatWillNotLoadIsARefusal(t *testing.T) {
	runner := newRunner()
	runner.fails(FixtureLoadCommand)

	if err := engineWith(runner).LoadFixture(anEnvironment(t), "baseline"); err == nil {
		t.Fatal("a failed fixture load was reported as success")
	}
}

// The stamp is written after the install and never before: one that outlived a
// failed install would make the next run skip the retry.
func TestTheAddOnStampIsWrittenOnlyAfterASuccessfulInstall(t *testing.T) {
	environment := anEnvironment(t)
	stamp := filepath.Join(environment.ProjectPath, ".ddev", FixtureAddOnStamp)

	runner := newRunner()
	runner.fails("add-on get")

	if err := engineWith(runner).LoadFixture(environment, "baseline"); err == nil {
		t.Fatal("a failed add-on install was reported as success")
	}
	if _, err := os.Stat(stamp); err == nil {
		t.Error("a stamp outlived a failed install")
	}
	if runner.didRun(FixtureLoadCommand) {
		t.Errorf("it loaded a fixture through an add-on that is not installed:\n%s", runner.transcript())
	}

	// And a successful one stamps.
	runner = newRunner()
	if err := engineWith(runner).LoadFixture(environment, "baseline"); err != nil {
		t.Fatalf("load: %v", err)
	}
	written, err := os.ReadFile(stamp)
	if err != nil {
		t.Fatalf("no stamp was written: %v", err)
	}
	if strings.TrimSpace(string(written)) != FixtureAddOnExpectedStamp() {
		t.Errorf("stamp %q, want %q", written, FixtureAddOnExpectedStamp())
	}
}

// installedAddOn lays out what an add-on install leaves behind, stamped with a
// given version.
func installedAddOn(t *testing.T, environment Environment, stamp string) {
	t.Helper()

	ddev := filepath.Join(environment.ProjectPath, ".ddev")
	marker := filepath.Join(ddev, FixtureAddOnMarker)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(marker, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	stampPath := filepath.Join(ddev, FixtureAddOnStamp)
	if err := os.MkdirAll(filepath.Dir(stampPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(stampPath, []byte(stamp+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// Both the marker and the stamp, not either: a command file's presence says
// nothing about which release wrote it.
func TestAnEnvironmentCarryingAnOlderAddOnIsReinstalled(t *testing.T) {
	environment := anEnvironment(t)
	installedAddOn(t, environment, "0.9.0")

	runner := newRunner()
	if err := engineWith(runner).LoadFixture(environment, "baseline"); err != nil {
		t.Fatalf("load: %v", err)
	}

	if !runner.didRun("add-on get") {
		t.Errorf("an environment holding an older release was not re-installed:\n%s", runner.transcript())
	}
}

// A stamp with no commands beside it is not an installation either.
func TestAStampWithoutTheCommandsIsNotAnInstallation(t *testing.T) {
	environment := anEnvironment(t)
	installedAddOn(t, environment, FixtureAddOnExpectedStamp())
	if err := os.Remove(filepath.Join(environment.ProjectPath, ".ddev", FixtureAddOnMarker)); err != nil {
		t.Fatalf("remove: %v", err)
	}

	runner := newRunner()
	if err := engineWith(runner).LoadFixture(environment, "baseline"); err != nil {
		t.Fatalf("load: %v", err)
	}

	if !runner.didRun("add-on get") {
		t.Errorf("a stamp alone was taken for an installation:\n%s", runner.transcript())
	}
}

// And one already carrying the pinned release is left alone.
func TestAnEnvironmentAlreadyOnThePinnedAddOnIsNotReinstalled(t *testing.T) {
	environment := anEnvironment(t)
	installedAddOn(t, environment, FixtureAddOnExpectedStamp())

	runner := newRunner()
	if err := engineWith(runner).LoadFixture(environment, "baseline"); err != nil {
		t.Fatalf("load: %v", err)
	}

	if runner.didRun("add-on get") {
		t.Errorf("the add-on was re-fetched when it was already right:\n%s", runner.transcript())
	}
	if !runner.didRun(FixtureLoadCommand, "baseline") {
		t.Errorf("the fixture was not loaded:\n%s", runner.transcript())
	}
}

// Package contract holds the tests that cross the container boundary.
//
// Everything else in this repository is fast, offline, and held to a
// per-package coverage floor. These are the opposite by necessity: they need
// docker and ddev, they take minutes, and they exist because the rest of the
// suite is structurally unable to see the class of failure that has cost this
// project the most.
//
// Three bugs shipped past a fully green PHP suite in two days, all of them at
// this boundary and none visible to a test that only inspects a string:
//
//  1. `MODULE: unbound variable` — a multi-line script, then a variable of our
//     own. `ddev exec` re-joins its arguments and hands the result to a shell
//     that expands the string *before* the container's shell runs it, so
//     neither survives.
//  2. `Referenced sniff "./vendor/drupal/coder/…" does not exist` — a `cd`
//     into the module, copied from CI, where the module repo root is where
//     composer put vendor/. Under ddev-drupal-contrib it is not.
//  3. A module's own `require-dev` never installed, because composer does not
//     install a path dependency's dev requirements.
//
// Every one of those is a fact about an environment, not about a string. So
// these run the real commands, in a real container, and read what comes back.
//
// The Go port inherited all three fixes as code and none of them as evidence:
// every adapter test here drives a fake runner, which records what it was
// handed and cannot tell you whether a container would accept it. This is that
// evidence.
//
// Skipped rather than failed when there is no project to run against, so a
// contributor without docker still gets the four gates.
package contract_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/proc"
)

// projectEnv names a started ddev project. The fixture script builds one.
const projectEnv = "UPKEEP_DDEV_PROJECT"

// modulePath is the module's in-container path exactly as the adapter builds
// it.
//
// Duplicated deliberately rather than called: if the adapter's own helper
// changed shape, a test that borrowed it would follow the change and still
// pass. This is the string the container has to understand.
const modulePath = `"$DDEV_DOCROOT/$DRUPAL_PROJECTS_PATH"/'widget'`

// project is the started ddev project, or a skip.
func project(t *testing.T) string {
	t.Helper()

	dir := os.Getenv(projectEnv)
	if dir == "" {
		t.Skipf("no ddev project to run against; set %s to a started project "+
			"(internal/contract/fixture/setup.sh builds one)", projectEnv)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Skipf("%s=%q is not a directory", projectEnv, dir)
	}

	return dir
}

// inContainer runs one command through `ddev exec`, exactly as the adapter
// does, and reports the exit status and everything printed.
//
// Through internal/proc like every other child upkeep starts, so the
// credential scrubbing is the same here as in production — a contract test
// that bypassed it would be testing a different program.
func inContainer(t *testing.T, dir, commandLine string) (int, string) {
	t.Helper()

	runner := proc.New(nil, nil, nil)
	captured := runner.Capture(
		[]string{"ddev", "exec", commandLine}, dir, 5*time.Minute)

	if captured.TimedOut {
		t.Fatalf("`ddev exec %s` did not finish in five minutes:\n%s",
			commandLine, captured.Output)
	}
	// A nil status is the child never reporting one — killed, or never
	// executed. That is a broken contract, not a verdict, and it must not read
	// as a passing zero.
	if captured.ExitCode == nil {
		t.Fatalf("`ddev exec %s` produced no exit status at all:\n%s",
			commandLine, captured.Output)
	}

	return *captured.ExitCode, captured.Output
}

// Every command upkeep sends survives the trip to the container's shell.
//
// The failure this exists for shipped twice: `ROOT=$(pwd) && … "$ROOT/x"` died
// with `ROOT: unbound variable`, as an earlier `MODULE=…` did, because the
// command reached a shell that had already expanded the string.
//
// **That exact shape no longer reproduces**, and saying so is more useful than
// implying the test is stronger than it is. On ddev 1.24 `nounset` is on in the
// container, but an assignment and its use in one `ddev exec` argument survive
// — verified by hand. So this no longer catches a reintroduction of that
// precise bug; what it catches is the class, by refusing every way a command
// can fail *as a command* rather than as a verdict.
//
// The distinction matters because phpcs and phpstan exit non-zero for a living.
// A non-zero status is an answer here; a shell that could not parse what it was
// given is not.
func TestEveryGeneratedCommandSurvivesTheTripToTheShell(t *testing.T) {
	dir := project(t)

	// Ways a command fails as a command. None of these is a tool verdict.
	brokenCommand := []string{
		"unbound variable",
		"command not found",
		"syntax error",
		"No such file or directory",
		"cannot open",
	}

	for name, commandLine := range everyCheckCommand() {
		_, output := inContainer(t, dir, commandLine)

		for _, broken := range brokenCommand {
			if strings.Contains(output, broken) {
				t.Errorf("%s did not survive the trip (%q):\n%s", name, broken, output)
			}
		}
	}
}

// everyCheckCommand is every command string the adapter sends to the
// container, in both the module-config and fallback shapes.
func everyCheckCommand() map[string]string {
	return map[string]string{
		"phpstan probe":                  adapter.ConfigProbe(modulePath, "phpstan.neon"),
		"phpcs probe":                    adapter.ConfigProbe(modulePath, "phpcs.xml.dist"),
		"phpstan with the module config": adapter.PhpstanScript(modulePath, "phpstan.neon"),
		"phpcs with the module ruleset":  adapter.PhpcsScript(modulePath, "phpcs.xml.dist"),
		"phpstan falling back":           adapter.PhpstanScript(modulePath, ""),
		"phpcs falling back":             adapter.PhpcsScript(modulePath, ""),
	}
}

// The probe answers about the container's real filesystem.
//
// Its exit status decides which configuration a check is given, so a probe
// that cannot see the module would silently send every module down the
// fallback path — and every module's own level, baseline and ruleset would be
// ignored without a word.
func TestTheProbeSeesAConfigThatIsThereAndNotOneThatIsNot(t *testing.T) {
	dir := project(t)

	if code, out := inContainer(t, dir, adapter.ConfigProbe(modulePath, "phpcs.xml.dist")); code != 0 {
		t.Errorf("the fixture module ships a phpcs.xml.dist, probe said %d:\n%s", code, out)
	}
	if code, _ := inContainer(t, dir, adapter.ConfigProbe(modulePath, ".phpcs.xml")); code == 0 {
		t.Error("the probe claims a config the fixture does not ship")
	}
}

// A vendor-relative sniff path resolves from where the check actually runs.
//
// The second failure. CI runs phpcs from inside the module because there the
// module repo root is where `composer install` put vendor/. Under
// ddev-drupal-contrib the module is a checkout inside a site whose vendor/ is
// at the project root, so a `cd` into the module breaks every vendor-relative
// reference — observed as `Referenced sniff "./vendor/drupal/coder/…" does not
// exist` on a real module.
//
// The fixture's ruleset uses that exact long-path form, so this fails if
// anything ever moves the working directory again.
//
// Asserted positively — phpcs got far enough to report — rather than by
// looking for the historical error text. Reintroducing the `cd` on purpose
// showed why: it moved the failure *earlier*, to `--basepath ... points to a
// non-existent directory`, so a test watching only for "Referenced sniff"
// passed while the check was comprehensively broken. The known strings are
// still named, but to explain a failure rather than to detect it.
func TestAVendorRelativeSniffPathResolvesFromWhereTheCheckRuns(t *testing.T) {
	dir := project(t)

	_, output := inContainer(t, dir, adapter.PhpcsScript(modulePath, "phpcs.xml.dist"))

	for _, known := range []string{
		"Referenced sniff",
		"No sniffs were registered",
		"points to a non-existent directory",
	} {
		if strings.Contains(output, known) {
			t.Errorf("phpcs never analysed anything (%q):\n%s", known, output)
		}
	}

	// The summary is printed only once files have actually been analysed.
	if !strings.Contains(output, "PHP CODE SNIFFER REPORT SUMMARY") {
		t.Errorf("phpcs produced no report, so it never got to the files:\n%s", output)
	}
}

// And the module's own ruleset is the one that ran.
//
// Not merely "phpcs exited": a default ruleset lying in the working directory
// would also exit, and would say nothing about whether the module's
// configuration was honoured. The fixture's file breaks the Drupal standard
// and nothing else, so a `Drupal.` sniff code in the output is the proof that
// the ruleset the module ships — the one referencing the standard by vendor
// path — is what loaded.
//
// The sniff *code*, not a particular sniff: which rules the Drupal standard
// enables is coder's business and changes between releases. Pinning one would
// make this fail on a coder upgrade while the property it guards still held.
func TestTheModulesOwnRulesetIsWhatRuns(t *testing.T) {
	dir := project(t)

	_, output := inContainer(t, dir, adapter.PhpcsScript(modulePath, "phpcs.xml.dist"))

	if !strings.Contains(output, "(Drupal.") {
		t.Errorf("no Drupal sniff fired, so the module's ruleset did not load:\n%s", output)
	}
}

// phpstan runs, and its answer is about the module.
//
// Level 0 over a three-line class: the point is not the verdict but that the
// command survived the trip and analysed the right directory.
func TestPhpstanAnalysesTheModule(t *testing.T) {
	dir := project(t)

	code, output := inContainer(t, dir, adapter.PhpstanScript(modulePath, "phpstan.neon"))

	if strings.Contains(output, "not found") || strings.Contains(output, "No files found") {
		t.Errorf("phpstan analysed nothing:\n%s", output)
	}
	if code != 0 {
		t.Logf("phpstan exited %d, which is a verdict rather than a contract failure:\n%s",
			code, output)
	}
}

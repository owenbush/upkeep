package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/check"
)

// The engine's JavaScript checks are commands of its own, with nothing to
// configure and nothing to scope.
func TestTheJavaScriptChecksAreTheEnginesOwnCommands(t *testing.T) {
	for _, one := range []check.Type{check.EsLint, check.StyleLint} {
		runner := newRunner()

		result, err := engineWith(runner).RunChecks(anEnvironment(t), []check.Type{one})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if result.Results[0].Status != check.Passed {
			t.Errorf("%s: status %q", one, result.Results[0].Status)
		}
		if !runner.didRun("ddev " + string(one)) {
			t.Errorf("%s did not run as the engine's own command:\n%s", one, runner.transcript())
		}
	}
}

// A check that stops making progress is a timed-out result carrying what it
// said before it stopped — never a hang, and never a plain red.
func TestEveryCheckTimesOutRatherThanHanging(t *testing.T) {
	environment := anEnvironment(t)
	environment.PrimaryURL = "https://upkeep-pathauto-d11.ddev.site"

	commandDir := filepath.Join(environment.ProjectPath, ".ddev", "commands", "web")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(commandDir, "upgrade-status"), []byte("#!/bin/sh\n"), 0o755,
	); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, one := range []check.Type{check.FunctionalSmoke, check.Deprecation, check.ModuleInstall} {
		runner := newRunner().stalls("ddev").stalls("curl")

		result, err := engineWith(runner).RunChecks(environment, []check.Type{one})
		if err != nil {
			t.Fatalf("%s: %v", one, err)
		}
		if result.Results[0].Status != check.Failed {
			t.Errorf("%s: status %q", one, result.Results[0].Status)
		}
		if !strings.Contains(result.Results[0].Output, "imed out") {
			t.Errorf("%s: the result does not say it timed out: %q", one, result.Results[0].Output)
		}
	}
}

// A patch with no base branch of its own falls back to what the working copy
// implies, which is the same rule the other apply paths resolve against.
func TestAPatchWithNoBaseBranchResolvesOneFromTheWorkingCopy(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("config --get upkeep.base-branch", "2.0.x\n").
		answer("rev-parse HEAD", "patched1\n")

	if err := engineWith(runner).ApplyPatch(
		anEnvironment(t),
		PatchApplication{IssueNid: 3601234, LocalPath: "/tmp/p.patch"},
		RefreshUpdate,
	); err != nil {
		t.Fatalf("apply: %v\n%s", err, runner.transcript())
	}

	if !runner.didRun("checkout -B patch-3601234") {
		t.Errorf("no branch was cut:\n%s", runner.transcript())
	}
	if !runner.didRun("fetch origin 2.0.x") {
		t.Errorf("it did not resolve and fetch the base:\n%s", runner.transcript())
	}
}

// Promoting routes through StartWork, so its refusals are the ones a promotion
// gives — the branch it lands on can be the only place the work exists.
func TestPromotingInheritsTheWorkBranchRefusals(t *testing.T) {
	runner := cleanOn("2.0.x").answer("status --porcelain", "M  src/Thing.php\n")

	_, err := engineWith(runner).PromotePatch(
		anEnvironment(t),
		PatchApplication{IssueNid: 3601234, LocalPath: "/tmp/p.patch"},
		IssueBranchFor(3601234, "Fix the thing"),
		"Issue #3601234 by someone: Fix the thing",
		RefreshUpdate,
		false,
	)
	if err == nil {
		t.Fatal("it promoted onto a dirty working copy")
	}
	if runner.didRun("apply") {
		t.Errorf("it applied anyway:\n%s", runner.transcript())
	}
}

// A project the engine forgets mid-flight is a refusal rather than an
// environment with no URL.
func TestAProjectThatDisappearsMidReuseIsARefusal(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := existingEnvironment(t, root, aCurrentMeta())

	// Known at the health check and gone by the time it is reused.
	answered := false
	runner.answer("describe", describeJSON("running", projectPath, ""))
	runner.does("describe", func() {
		if answered {
			runner.answer("describe", "")
		}
		answered = true
	})

	if _, err := engine.EnsureEnv(pathauto, "11"); err == nil {
		t.Fatal("it reused a project the engine no longer reports")
	}
}

// And one that does not come back after a start is not one to hand out.
func TestAProjectThatDoesNotComeBackAfterAStartIsARefusal(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := existingEnvironment(t, root, aCurrentMeta())
	runner.answer("describe", describeJSON("stopped", projectPath, ""))
	runner.does("ddev start", func() { runner.answer("describe", "") })

	_, err := engine.EnsureEnv(pathauto, "11")
	if err == nil {
		t.Fatal("it handed back an environment the engine does not report")
	}
	if !strings.Contains(err.Error(), "did not come back") {
		t.Errorf("the refusal does not say what happened: %v", err)
	}
}

// The pre-flight catches a moved root when the engine can still describe the
// old registration. It cannot when the old directory is gone and the record
// survives — so the engine's own refusal is translated rather than surfacing
// as an unactionable wall of its output.
func TestARootConflictTheEngineOnlyRaisesLaterIsStillTranslated(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := filepath.Join(root, "upkeep-pathauto-d11")
	aSuccessfulProvision(t, runner, projectPath)
	// Nothing to describe up front, and the engine refuses at config time.
	runner.answer("describe", "")
	runner.fails("ddev config")
	runner.answer("ddev config",
		"Failed to config project: project root is already set to /old/root/upkeep-pathauto-d11")

	_, err := engine.EnsureEnv(pathauto, "11")
	if err == nil {
		t.Fatal("a root conflict was reported as success")
	}
	if !strings.Contains(err.Error(), "--unlist") {
		t.Errorf("the engine's refusal was not translated: %v", err)
	}
}

// A conflict the engine raised without saying where it thinks the project
// lives still names the recovery.
func TestAConflictWithNoKnownRootStillNamesTheRecovery(t *testing.T) {
	message := ProjectRegistration{ProjectName: "upkeep-pathauto-d11"}.
		ConflictError("/home/me/projects/upkeep-pathauto-d11").Error()

	if !strings.Contains(message, "(unknown)") {
		t.Errorf("an unknown root was rendered as nothing: %s", message)
	}
	if !strings.Contains(message, "--unlist") {
		t.Errorf("the recovery was not named: %s", message)
	}
}

// A description's scalars come back as strings; anything structured is "not
// known", never the shape it happened to have.
func TestDescriptionScalarsNarrowAndStructuresDoNot(t *testing.T) {
	described, ok := DescriptionFromJSON(
		`{"raw":{"name":"x","mutagen":true,"off":false,"port":8080,"ratio":1.5,` +
			`"services":{"web":1},"tags":["a"],"missing":null}}`,
	)
	if !ok {
		t.Fatal("it did not parse")
	}

	for field, want := range map[string]string{
		"name":     "x",
		"mutagen":  "1",
		"off":      "",
		"port":     "8080",
		"ratio":    "1.5",
		"services": "",
		"tags":     "",
		"missing":  "",
		"absent":   "",
	} {
		if got := described.StringOrNull(field); got != want {
			t.Errorf("%s is %q, want %q", field, got, want)
		}
	}
}

// An override is a local checkout, where a release tag means nothing: it pins
// no version and stamps its source, so moving between a checkout and the
// published release re-installs rather than trusting whichever landed first.
func TestAnOverriddenAddOnSourceIsInstalledUnpinnedAndStampsItself(t *testing.T) {
	t.Setenv(FixtureAddOnSourceEnv, "/home/me/code/ddev-upkeep")

	environment := anEnvironment(t)
	runner := newRunner()
	engine, said := logging(nil, runner)

	if err := engine.LoadFixture(environment, "baseline"); err != nil {
		t.Fatalf("load: %v\n%s", err, runner.transcript())
	}

	if !runner.didRun("add-on get /home/me/code/ddev-upkeep") {
		t.Errorf("the override was not used:\n%s", runner.transcript())
	}
	if runner.didRun("--version") {
		t.Errorf("a checkout was given a release tag:\n%s", runner.transcript())
	}
	if !strings.Contains(strings.Join(*said, "\n"), "/home/me/code/ddev-upkeep") {
		t.Errorf("the source was not reported:\n%s", strings.Join(*said, "\n"))
	}

	stamp, err := os.ReadFile(filepath.Join(environment.ProjectPath, ".ddev", FixtureAddOnStamp))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(string(stamp)) != "source:/home/me/code/ddev-upkeep" {
		t.Errorf("stamp %q — it would not re-install on a move back to the release", stamp)
	}
}

// shortSHA is for a log line, so a full SHA is abbreviated and anything
// already short is left alone.
func TestShortShaAbbreviatesOnlyWhatIsLongEnough(t *testing.T) {
	if got := shortSHA("0123456789abcdef0123456789abcdef01234567"); got != "01234567" {
		t.Errorf("got %q", got)
	}
	for _, short := range []string{"", "abc", "01234567"} {
		if got := shortSHA(short); got != short {
			t.Errorf("%q became %q", short, got)
		}
	}
}

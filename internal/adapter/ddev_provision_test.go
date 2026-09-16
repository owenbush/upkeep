package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/cockpit"
)

// pathauto is the module under maintenance in these tests.
var pathauto = cockpit.Module{Name: "pathauto", Project: "pathauto", CoreVersions: []string{"11"}}

// describeJSON is the shape the engine answers `describe -j` with.
func describeJSON(status, approot, primaryURL string) string {
	return `{"raw":{"status":"` + status + `","approot":"` + approot +
		`","primary_url":"` + primaryURL + `"}}`
}

// aProjectsRoot is an empty projects root plus a base artifact for one core,
// which is the state a first provision starts from.
func aProjectsRoot(t *testing.T, coreMajor string) (root string, engine *DdevContrib, runner *recordingRunner) {
	t.Helper()

	root = filepath.Join(t.TempDir(), "projects")
	runner = newRunner()
	engine = NewDdevContrib(artifactsFor(t, coreMajor, coreMajor+".4.6"), root, runner, nil)

	return root, engine, runner
}

// aSuccessfulProvision scripts every command a provision makes, and the files
// each one leaves for the step after it.
//
// Scripted rather than faked away, because provisioning is a sequence whose
// steps hand things to each other: the seeded tree carries the composer.json
// the wiring rewrites, the add-on install writes the config the adaptation
// reads, and the composer require is what puts the module symlink where the
// ownership check looks for it. A fake that skipped the filesystem would be
// testing the order of some strings.
func aSuccessfulProvision(t *testing.T, runner *recordingRunner, projectPath string) {
	t.Helper()

	write := func(path, contents string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	runner.
		does("cp -a", func() {
			write(filepath.Join(projectPath, "composer.json"), `{"name":"drupal/recommended-project"}`)
		}).
		does("git clone", func() {
			if err := os.MkdirAll(moduleWorkingCopy(projectPath), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}).
		does("add-on get "+EngineAddOnName, func() {
			write(filepath.Join(projectPath, ".ddev", EngineAddOnConfigFilename), shippedAddOnConfig)
		}).
		does("composer require drupal/pathauto", func() {
			link := filepath.Join(projectPath, "web", "modules", "contrib", "pathauto")
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.Symlink(moduleWorkingCopy(projectPath), link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}).
		answer("symbolic-ref --short HEAD", "2.0.x\n").
		answer("drush status", "11.4.6\n").
		answer("describe", describeJSON("running", projectPath, "https://upkeep-pathauto-d11.ddev.site"))
}

// Provisioning is a sequence, and every step depends on the one before it: the
// codebase before the module, the engine project before its add-on, the
// add-on's config adapted before anything starts, and the database only once
// there is a site to import it into.
func TestProvisioningRunsItsStepsInOrder(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := filepath.Join(root, "upkeep-pathauto-d11")
	aSuccessfulProvision(t, runner, projectPath)

	environment, err := engine.EnsureEnv(pathauto, "11")
	if err != nil {
		t.Fatalf("ensure: %v\n%s", err, runner.transcript())
	}

	steps := [][]string{
		{"cp -a"},
		{"git clone"},
		{"ddev config"},
		{"add-on get", EngineAddOnName},
		{"ddev start"},
		{"composer require drush/drush"},
		{"composer require drupal/pathauto"},
		{"import-db"},
		{"drush status"},
	}
	last := -1
	for _, step := range steps {
		at := runner.indexOfCommand(step...)
		if at < 0 {
			t.Fatalf("step %v never ran:\n%s", step, runner.transcript())
		}
		if at < last {
			t.Errorf("step %v ran out of order:\n%s", step, runner.transcript())
		}
		last = at
	}

	if environment.Reused {
		t.Error("a fresh provision reported itself as a reuse")
	}
	if environment.PrimaryURL != "https://upkeep-pathauto-d11.ddev.site" {
		t.Errorf("primary url %q", environment.PrimaryURL)
	}
	if environment.ProjectPath != projectPath {
		t.Errorf("project path %q, want %q", environment.ProjectPath, projectPath)
	}
}

// The add-on assumes the module is the project root; upkeep seeds a whole
// codebase and wires the module in. So its config is adapted after every
// installation, which may clobber the file.
func TestTheAddOnConfigIsAdaptedToTheSeededLayout(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := filepath.Join(root, "upkeep-pathauto-d11")
	aSuccessfulProvision(t, runner, projectPath)

	if _, err := engine.EnsureEnv(pathauto, "11"); err != nil {
		t.Fatalf("ensure: %v\n%s", err, runner.transcript())
	}

	adapted, err := os.ReadFile(filepath.Join(projectPath, ".ddev", EngineAddOnConfigFilename))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Against a full project tree the post-start hook would symlink the whole
	// codebase into itself.
	if strings.Contains(string(adapted), "symlink-project") {
		t.Errorf("the post-start hook survived:\n%s", adapted)
	}
	// And the checks must find the module where the path-repository install
	// puts it.
	if !strings.Contains(string(adapted), "DRUPAL_PROJECTS_PATH="+EngineProjectsPath) {
		t.Errorf("the projects path was not repointed:\n%s", adapted)
	}
	// The marker is kept: a future installation may clobber the file, which is
	// why the adaptation is re-run after every one.
	if !strings.HasPrefix(string(adapted), "#ddev-generated") {
		t.Errorf("the generated marker was dropped:\n%s", adapted)
	}
}

// The environment must bootstrap and report the core it was asked for, read
// from the project itself rather than assumed from what was seeded.
func TestAnEnvironmentReportingTheWrongCoreIsNotAnEnvironment(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := filepath.Join(root, "upkeep-pathauto-d11")
	aSuccessfulProvision(t, runner, projectPath)
	runner.answer("drush status", "10.3.9\n")

	_, err := engine.EnsureEnv(pathauto, "11")
	if err == nil {
		t.Fatal("an environment running the wrong core was accepted")
	}
	if !strings.Contains(err.Error(), "10.3.9") || !strings.Contains(err.Error(), "expected major 11") {
		t.Errorf("the refusal does not say what it found: %v", err)
	}
	// And the partial environment did not survive to be reused.
	if _, err := os.Stat(EnvMetaPath(projectPath)); err == nil {
		t.Error("a completion marker was written for an environment that failed its health gate")
	}
	if !runner.didRun("ddev delete") {
		t.Errorf("the partial environment was not torn down:\n%s", runner.transcript())
	}
}

// The meta is the completion marker and it is written last, so a crash
// anywhere before it leaves nothing that reads as reusable.
func TestAFailedProvisionLeavesNoCompletionMarkerAndIsTornDown(t *testing.T) {
	for _, step := range []string{"cp -a", "git clone", "ddev config", "add-on get", "ddev start", "import-db"} {
		root, engine, runner := aProjectsRoot(t, "11")
		projectPath := filepath.Join(root, "upkeep-pathauto-d11")
		aSuccessfulProvision(t, runner, projectPath)
		runner.fails(step)

		if _, err := engine.EnsureEnv(pathauto, "11"); err == nil {
			t.Errorf("%s failed and the provision reported success", step)

			continue
		}
		if _, err := os.Stat(EnvMetaPath(projectPath)); err == nil {
			t.Errorf("%s failed and a completion marker was still written", step)
		}
		if !runner.didRun("ddev delete") {
			t.Errorf("%s failed and nothing was torn down:\n%s", step, runner.transcript())
		}
	}
}

// Cleanup failing after a failed provision does not replace the reason the
// provision failed — that is the one the operator needs.
func TestCleanupFailureDoesNotHideWhyProvisioningFailed(t *testing.T) {
	root, runner := filepath.Join(t.TempDir(), "projects"), newRunner()
	projectPath := filepath.Join(root, "upkeep-pathauto-d11")
	engine, said := logging(artifactsFor(t, "11", "11.4.6"), runner)
	engine.projectsRoot = root
	aSuccessfulProvision(t, runner, projectPath)
	runner.fails("git clone")
	runner.fails("rm -rf")

	_, err := engine.EnsureEnv(pathauto, "11")
	if err == nil || !strings.Contains(err.Error(), "git clone") {
		t.Fatalf("the reported failure is not the one that happened: %v", err)
	}
	if !strings.Contains(strings.Join(*said, "\n"), "Cleanup after failed provisioning also failed") {
		t.Errorf("the cleanup failure was swallowed entirely:\n%s", strings.Join(*said, "\n"))
	}
}

// Engine project names are global to the machine while the projects root is
// configurable, so a moved root collides with whatever the old one registered.
// Refused before any work, rather than after a codebase is seeded and a
// repository cloned.
func TestAProjectRegisteredSomewhereElseIsRefusedBeforeAnyWorkIsDone(t *testing.T) {
	_, engine, runner := aProjectsRoot(t, "11")
	runner.answer("describe", describeJSON("running", "/somewhere/else/upkeep-pathauto-d11", ""))

	_, err := engine.EnsureEnv(pathauto, "11")
	if err == nil {
		t.Fatal("it provisioned over another root's registration")
	}
	if !strings.Contains(err.Error(), "/somewhere/else/upkeep-pathauto-d11") {
		t.Errorf("the refusal does not name where the engine thinks it lives: %v", err)
	}
	// `ddev stop --unlist` deregisters rather than deleting, and an operator
	// deciding whether to run it needs to know that.
	if !strings.Contains(err.Error(), "--unlist") {
		t.Errorf("the refusal does not name the recovery: %v", err)
	}
	if runner.didRun("cp -a") || runner.didRun("git clone") {
		t.Errorf("work was done before the refusal:\n%s", runner.transcript())
	}
}

// A registration the engine did not report is not a conflict: refusing on the
// strength of a question that could not be asked would turn a diagnostic into
// an outage.
func TestAnUnreportedRegistrationIsNotTreatedAsAConflict(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := filepath.Join(root, "upkeep-pathauto-d11")
	aSuccessfulProvision(t, runner, projectPath)
	// The engine answers describe only once there is a project; before that it
	// says nothing at all.
	runner.answer("describe", "")
	runner.does("ddev config", func() {
		runner.answer("describe", describeJSON("running", projectPath, "https://x.ddev.site"))
	})

	if _, err := engine.EnsureEnv(pathauto, "11"); err != nil {
		t.Fatalf("an unanswerable question refused a provision: %v\n%s", err, runner.transcript())
	}
}

// existingEnvironment is a provisioned environment on disk, with its
// completion marker.
func existingEnvironment(t *testing.T, root string, meta EnvironmentMeta) string {
	t.Helper()

	projectPath := filepath.Join(root, "upkeep-pathauto-d11")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := meta.WriteTo(projectPath); err != nil {
		t.Fatalf("write: %v", err)
	}

	return projectPath
}

// aCurrentMeta matches what a provision from the test artifact would record.
func aCurrentMeta() EnvironmentMeta {
	created := time.Now().Add(-72 * time.Hour)

	return EnvironmentMeta{
		ModuleName: "pathauto", CoreMajor: "11", SeedCoreVersion: "11.4.6",
		AddOnVersion: EngineAddOnVersion, CreatedAt: created, LastUsedAt: created,
	}
}

// A healthy environment is reused rather than rebuilt, and the reuse is
// stamped: `prune --older-than` filters on that, and without it age is
// time-since-creation and prune deletes environments in daily use.
func TestAHealthyEnvironmentIsReusedAndTheReuseIsStamped(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := existingEnvironment(t, root, aCurrentMeta())
	runner.answer("describe", describeJSON("running", projectPath, "https://upkeep-pathauto-d11.ddev.site"))

	// The meta records whole seconds, so the comparison is made at that
	// resolution rather than at the clock's.
	before := time.Now().Truncate(time.Second)
	environment, err := engine.EnsureEnv(pathauto, "11")
	if err != nil {
		t.Fatalf("ensure: %v\n%s", err, runner.transcript())
	}

	if !environment.Reused {
		t.Error("a reuse did not report itself as one")
	}
	if runner.didRun("cp -a") || runner.didRun("ddev delete") {
		t.Errorf("a healthy environment was rebuilt:\n%s", runner.transcript())
	}

	contents, err := os.ReadFile(EnvMetaPath(projectPath))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	meta, err := EnvMetaFromYAML(string(contents))
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if meta.LastUsedAt.Before(before) {
		t.Errorf("the reuse was not stamped: last used %s", meta.LastUsedAt)
	}
	if !meta.CreatedAt.Equal(aCurrentMeta().CreatedAt.Truncate(time.Second)) {
		t.Errorf("stamping a reuse rewrote when the environment was created: %s", meta.CreatedAt)
	}
}

// A stopped environment is started rather than rebuilt, under a timebox of its
// own: the one-hour default is for resolves and installs, and applied here it
// turned a wedged start into an hour of silence.
func TestAStoppedEnvironmentIsStartedUnderItsOwnTimebox(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := existingEnvironment(t, root, aCurrentMeta())
	runner.answer("describe", describeJSON("stopped", projectPath, ""))
	runner.does("ddev start", func() {
		runner.answer("describe", describeJSON("running", projectPath, "https://upkeep-pathauto-d11.ddev.site"))
	})

	environment, err := engine.EnsureEnv(pathauto, "11")
	if err != nil {
		t.Fatalf("ensure: %v\n%s", err, runner.transcript())
	}

	if !runner.didRun("ddev start -y") {
		t.Errorf("a stopped environment was not started:\n%s", runner.transcript())
	}
	if got := runner.timeoutOf("ddev start -y"); got != startTimeout {
		t.Errorf("started under %s, want %s", got, startTimeout)
	}
	// And the URL comes from the description read *after* the start: a stopped
	// project has none.
	if environment.PrimaryURL != "https://upkeep-pathauto-d11.ddev.site" {
		t.Errorf("primary url %q — read before the start?", environment.PrimaryURL)
	}
}

// The file-sync case is named outright: it is the common one on macOS after an
// unclean shutdown, and the fix is a single safe command.
func TestAWedgedFileSyncIsDiagnosedRatherThanReportedRaw(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := existingEnvironment(t, root, aCurrentMeta())
	runner.answer("describe", describeJSON("stopped", projectPath, ""))
	runner.fails("ddev start")
	runner.answer("ddev start", "Starting Mutagen sync process...")

	_, err := engine.EnsureEnv(pathauto, "11")
	if err == nil {
		t.Fatal("a failed start was reported as success")
	}
	if !strings.Contains(err.Error(), "mutagen reset") {
		t.Errorf("the recovery was not named: %v", err)
	}
	// The host files are the source of truth, and saying so is what makes the
	// command safe to run.
	if !strings.Contains(err.Error(), "files on disk are untouched") {
		t.Errorf("the refusal does not say the command is safe: %v", err)
	}
}

// A start that failed for some other reason gets no invented diagnosis.
func TestAStartThatFailedForAnotherReasonIsReportedAsItIs(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := existingEnvironment(t, root, aCurrentMeta())
	runner.answer("describe", describeJSON("stopped", projectPath, ""))
	runner.fails("ddev start")

	_, err := engine.EnsureEnv(pathauto, "11")
	if err == nil {
		t.Fatal("a failed start was reported as success")
	}
	if strings.Contains(err.Error(), "mutagen reset") {
		t.Errorf("an unrelated failure was given a guessed diagnosis: %v", err)
	}
}

// Each staleness reason on its own forces a rebuild, and the operator is told
// which one it was.
func TestEveryKindOfStalenessForcesARebuildAndIsNamed(t *testing.T) {
	stale := map[string]func(EnvironmentMeta) EnvironmentMeta{
		"provisioned for module": func(m EnvironmentMeta) EnvironmentMeta {
			m.ModuleName = "token"

			return m
		},
		"provisioned for core": func(m EnvironmentMeta) EnvironmentMeta {
			m.CoreMajor = "10"

			return m
		},
		"Seed skew": func(m EnvironmentMeta) EnvironmentMeta {
			m.SeedCoreVersion = "11.1.0"

			return m
		},
		"Engine add-on skew": func(m EnvironmentMeta) EnvironmentMeta {
			m.AddOnVersion = "1.0.0"

			return m
		},
	}

	for expected, skew := range stale {
		root, runner := filepath.Join(t.TempDir(), "projects"), newRunner()
		engine, said := logging(artifactsFor(t, "11", "11.4.6"), runner)
		engine.projectsRoot = root

		projectPath := existingEnvironment(t, root, skew(aCurrentMeta()))
		aSuccessfulProvision(t, runner, projectPath)
		runner.answer("describe", describeJSON("running", projectPath, "https://x.ddev.site"))

		if _, err := engine.EnsureEnv(pathauto, "11"); err != nil {
			t.Fatalf("%s: %v\n%s", expected, err, runner.transcript())
		}
		if !runner.didRun("ddev delete") || !runner.didRun("cp -a") {
			t.Errorf("%s did not force a rebuild:\n%s", expected, runner.transcript())
		}
		if !strings.Contains(strings.Join(*said, "\n"), expected) {
			t.Errorf("the rebuild did not say why (%s):\n%s", expected, strings.Join(*said, "\n"))
		}
	}
}

// The meta dotfile is written as the last provisioning step, so its absence
// means an interrupted provision and never a reusable environment.
func TestADirectoryWithNoCompletionMarkerIsRebuilt(t *testing.T) {
	root, runner := filepath.Join(t.TempDir(), "projects"), newRunner()
	engine, said := logging(artifactsFor(t, "11", "11.4.6"), runner)
	engine.projectsRoot = root

	projectPath := filepath.Join(root, "upkeep-pathauto-d11")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	aSuccessfulProvision(t, runner, projectPath)

	if _, err := engine.EnsureEnv(pathauto, "11"); err != nil {
		t.Fatalf("ensure: %v\n%s", err, runner.transcript())
	}
	if !runner.didRun("cp -a") {
		t.Errorf("a partial provision was reused:\n%s", runner.transcript())
	}
	if !strings.Contains(strings.Join(*said, "\n"), "did not finish") {
		t.Errorf("the rebuild did not say why:\n%s", strings.Join(*said, "\n"))
	}
}

// An unreadable meta is a reason to rebuild, not a reason to fail.
func TestAnUnreadableMetaIsARebuildRatherThanARefusal(t *testing.T) {
	root, runner := filepath.Join(t.TempDir(), "projects"), newRunner()
	engine, said := logging(artifactsFor(t, "11", "11.4.6"), runner)
	engine.projectsRoot = root

	projectPath := filepath.Join(root, "upkeep-pathauto-d11")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(EnvMetaPath(projectPath), []byte("\tnot: [yaml"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	aSuccessfulProvision(t, runner, projectPath)

	if _, err := engine.EnsureEnv(pathauto, "11"); err != nil {
		t.Fatalf("ensure: %v\n%s", err, runner.transcript())
	}
	if !strings.Contains(strings.Join(*said, "\n"), "meta is unreadable") {
		t.Errorf("the rebuild did not say why:\n%s", strings.Join(*said, "\n"))
	}
}

// A project the engine has forgotten cannot be reused however good its meta
// looks: there is nothing running to reuse.
func TestAnEnvironmentTheEngineNoLongerKnowsIsRebuilt(t *testing.T) {
	root, runner := filepath.Join(t.TempDir(), "projects"), newRunner()
	engine, said := logging(artifactsFor(t, "11", "11.4.6"), runner)
	engine.projectsRoot = root

	projectPath := existingEnvironment(t, root, aCurrentMeta())
	aSuccessfulProvision(t, runner, projectPath)
	// Nothing known until the project is configured again.
	runner.answer("describe", "")
	runner.does("ddev config", func() {
		runner.answer("describe", describeJSON("running", projectPath, "https://x.ddev.site"))
	})

	if _, err := engine.EnsureEnv(pathauto, "11"); err != nil {
		t.Fatalf("ensure: %v\n%s", err, runner.transcript())
	}
	if !runner.didRun("cp -a") {
		t.Errorf("it reused a project the engine does not report:\n%s", runner.transcript())
	}
	if !strings.Contains(strings.Join(*said, "\n"), "no longer reports the project") {
		t.Errorf("the rebuild did not say why:\n%s", strings.Join(*said, "\n"))
	}
}

// Re-provisioning destroys the working copy, so unpushed work stops the
// rebuild rather than being taken down with it.
func TestAStaleEnvironmentHoldingLocalWorkRefusesRatherThanRebuilding(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")

	meta := aCurrentMeta()
	meta.AddOnVersion = "1.0.0"
	projectPath := existingEnvironment(t, root, meta)
	if err := os.MkdirAll(moduleWorkingCopy(projectPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runner.
		answer("describe", describeJSON("running", projectPath, "")).
		answer("status --porcelain", "").
		answer("symbolic-ref --short HEAD", "3601234-fix-the-thing\n").
		answer("rev-list --count @{upstream}..HEAD", "2\n")

	_, err := engine.EnsureEnv(pathauto, "11")
	if err == nil {
		t.Fatal("it rebuilt over unpushed work")
	}
	if !strings.Contains(err.Error(), "unpushed") {
		t.Errorf("the refusal does not say what would be lost: %v", err)
	}
	if runner.didRun("ddev delete") || runner.didRun("rm -rf") {
		t.Errorf("it started tearing down anyway:\n%s", runner.transcript())
	}
}

// Teardown removes containers and volumes through the engine before the tree:
// a bare tree removal would leave containers running.
func TestTeardownDeletesThroughTheEngineBeforeRemovingTheTree(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := existingEnvironment(t, root, aCurrentMeta())
	runner.answer("describe", describeJSON("running", projectPath, ""))

	if err := engine.Teardown(pathauto, "11"); err != nil {
		t.Fatalf("teardown: %v", err)
	}

	del, remove := runner.indexOfCommand("ddev delete"), runner.indexOfCommand("rm -rf")
	if del < 0 || remove < 0 {
		t.Fatalf("missing steps:\n%s", runner.transcript())
	}
	if del > remove {
		t.Errorf("the tree went before the containers:\n%s", runner.transcript())
	}
	if !runner.didRun("ddev delete", "--omit-snapshot") {
		t.Errorf("the delete kept a snapshot of a disposable environment:\n%s", runner.transcript())
	}
}

// An engine delete that fails does not stop the tree removal — the project may
// simply not be registered, and leaving the directory behind would make the
// next ensure reuse it.
func TestAFailedEngineDeleteStillRemovesTheTree(t *testing.T) {
	root, runner := filepath.Join(t.TempDir(), "projects"), newRunner()
	engine, said := logging(artifactsFor(t, "11", "11.4.6"), runner)
	engine.projectsRoot = root

	projectPath := existingEnvironment(t, root, aCurrentMeta())
	runner.answer("describe", describeJSON("running", projectPath, ""))
	runner.fails("ddev delete")

	if err := engine.Teardown(pathauto, "11"); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if !runner.didRun("rm -rf") {
		t.Errorf("the tree survived a failed engine delete:\n%s", runner.transcript())
	}
	if !strings.Contains(strings.Join(*said, "\n"), "may not be registered") {
		t.Errorf("the failure was silent:\n%s", strings.Join(*said, "\n"))
	}
}

// Nothing to tear down is a fact, not a failure.
func TestTearingDownWhatIsNotThereIsNotAFailure(t *testing.T) {
	root, runner := filepath.Join(t.TempDir(), "projects"), newRunner()
	engine, said := logging(artifactsFor(t, "11", "11.4.6"), runner)
	engine.projectsRoot = root

	if err := engine.Teardown(pathauto, "11"); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if runner.didRun("ddev delete") || runner.didRun("rm -rf") {
		t.Errorf("it tore down something that does not exist:\n%s", runner.transcript())
	}
	if !strings.Contains(strings.Join(*said, "\n"), "nothing to tear down") {
		t.Errorf("it said nothing:\n%s", strings.Join(*said, "\n"))
	}
}

// Either half can outlive the other, so a registration with no tree is still
// something to tear down.
func TestARegistrationWithNoTreeIsStillTornDown(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	runner.answer("describe", describeJSON("running", filepath.Join(root, "upkeep-pathauto-d11"), ""))

	if err := engine.Teardown(pathauto, "11"); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if !runner.didRun("ddev delete") {
		t.Errorf("an orphaned registration was left behind:\n%s", runner.transcript())
	}
	// And nothing was removed, because there is no tree to remove.
	if runner.didRun("rm -rf") {
		t.Errorf("it removed a tree that is not there:\n%s", runner.transcript())
	}
}

// Teardown is destructive and irreversible, so unpushed work stops it.
func TestTeardownRefusesOverLocalWork(t *testing.T) {
	root, engine, runner := aProjectsRoot(t, "11")
	projectPath := existingEnvironment(t, root, aCurrentMeta())
	if err := os.MkdirAll(moduleWorkingCopy(projectPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runner.
		answer("describe", describeJSON("running", projectPath, "")).
		answer("status --porcelain", " M src/Form/SettingsForm.php\n").
		answer("symbolic-ref --short HEAD", "2.0.x\n").
		answer("rev-list --count @{upstream}..HEAD", "0\n")

	err := engine.Teardown(pathauto, "11")
	if err == nil {
		t.Fatal("it tore down an environment holding uncommitted changes")
	}
	if !strings.Contains(err.Error(), "Unstaged changes") {
		t.Errorf("the refusal does not say what would be lost: %v", err)
	}
	if runner.didRun("ddev delete") || runner.didRun("rm -rf") {
		t.Errorf("it started tearing down anyway:\n%s", runner.transcript())
	}
}

// Without a base artifact there is nothing to seed from, and the refusal names
// the command that builds one.
func TestEnsuringWithoutABaseArtifactNamesTheBuildCommand(t *testing.T) {
	_, engine, runner := aProjectsRoot(t, "11")

	_, err := engine.EnsureEnv(pathauto, "12")
	if err == nil {
		t.Fatal("it provisioned from an artifact that does not exist")
	}
	if !strings.Contains(err.Error(), "base-artifacts:build --version=12") {
		t.Errorf("the refusal does not name the recovery: %v", err)
	}
	if len(runner.ran) != 0 {
		t.Errorf("it started work anyway:\n%s", runner.transcript())
	}
}

// A name that cannot be an engine project is refused before anything looks for
// an artifact or a directory.
func TestEnsuringAnImpossibleModuleNameIsRefused(t *testing.T) {
	_, engine, runner := aProjectsRoot(t, "11")

	if _, err := engine.EnsureEnv(cockpit.Module{Name: "Path-Auto", Project: "pathauto"}, "11"); err == nil {
		t.Fatal("an impossible name was provisioned")
	}
	if err := engine.Teardown(cockpit.Module{Name: "Path-Auto", Project: "pathauto"}, "11"); err == nil {
		t.Fatal("an impossible name was torn down")
	}
	if len(runner.ran) != 0 {
		t.Errorf("it ran something for a name that cannot exist:\n%s", runner.transcript())
	}
}

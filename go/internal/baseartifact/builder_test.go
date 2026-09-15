package baseartifact

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/proc"
)

// scriptedRunner answers commands from a substring script and can be told to
// fail one, recording everything it was asked to run.
type scriptedRunner struct {
	ran     []string
	fail    map[string]bool
	effects []struct {
		key string
		run func(command []string)
	}
}

func newScripted() *scriptedRunner {
	return &scriptedRunner{fail: map[string]bool{}}
}

func (r *scriptedRunner) fails(key string) *scriptedRunner {
	r.fail[key] = true

	return r
}

// does scripts what a command leaves behind, so a sequence whose steps hand
// files to each other can be exercised as one.
func (r *scriptedRunner) does(key string, effect func(command []string)) *scriptedRunner {
	r.effects = append(r.effects, struct {
		key string
		run func(command []string)
	}{key, effect})

	return r
}

func (r *scriptedRunner) Run(command []string, _ string, _ time.Duration) (string, error) {
	line := strings.Join(command, " ")
	r.ran = append(r.ran, line)

	for key := range r.fail {
		if strings.Contains(line, key) {
			return "", errors.New("command failed: " + line)
		}
	}
	for _, effect := range r.effects {
		if strings.Contains(line, effect.key) {
			effect.run(command)
		}
	}

	return "", nil
}

func (r *scriptedRunner) TryRun(command []string, dir string, timeout time.Duration) (string, bool) {
	out, err := r.Run(command, dir, timeout)

	return out, err == nil
}

func (r *scriptedRunner) Capture([]string, string, time.Duration) proc.Captured {
	return proc.Captured{}
}

func (r *scriptedRunner) didRun(fragments ...string) bool {
	for _, line := range r.ran {
		matched := true
		for _, fragment := range fragments {
			if !strings.Contains(line, fragment) {
				matched = false

				break
			}
		}
		if matched {
			return true
		}
	}

	return false
}

func (r *scriptedRunner) transcript() string { return "  " + strings.Join(r.ran, "\n  ") }

// fakeSite is an install site that writes a dump and records what it was asked
// for.
type fakeSite struct {
	installed   []string
	tornDown    []string
	fail        error
	writeDump   bool
	afterDump   func()
	environment InstallEnvironment
}

func newSite() *fakeSite {
	return &fakeSite{
		writeDump:   true,
		environment: InstallEnvironment{PHPVersion: "8.3.14", DBEngine: "mariadb:10.11"},
	}
}

func (s *fakeSite) CleanInstallAndDump(
	coreMajor, treePath, throwawayPath, projectName, dumpPath string,
) (InstallEnvironment, error) {
	s.installed = append(s.installed, projectName)
	if s.fail != nil {
		return InstallEnvironment{}, s.fail
	}
	if s.writeDump {
		if err := os.MkdirAll(filepath.Dir(dumpPath), 0o755); err == nil {
			_ = os.WriteFile(dumpPath, []byte("-- a database"), 0o644)
		}
	}
	if s.afterDump != nil {
		s.afterDump()
	}

	return s.environment, nil
}

func (s *fakeSite) Teardown(throwawayPath, projectName string) {
	s.tornDown = append(s.tornDown, projectName)
}

// aBuilder is a builder over a fresh base-artifacts directory, with the
// resolve scripted to leave a lock file behind.
func aBuilder(t *testing.T, runner *scriptedRunner, site InstallSite) (*Builder, *Layout, *[]string) {
	t.Helper()

	layout := NewLayout(filepath.Join(t.TempDir(), "base-artifacts"))
	said := &[]string{}

	runner.does("composer create-project", func(command []string) {
		// create-project's third argument is where the tree goes.
		treePath := command[3]
		if err := os.MkdirAll(treePath, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		lock := `{"packages":[{"name":"drupal/pathauto","version":"1.0.0"},` +
			`{"name":"drupal/core","version":"11.4.6"}]}`
		if err := os.WriteFile(filepath.Join(treePath, "composer.lock"), []byte(lock), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	})
	// The real one removes trees; nothing here should need it to.
	runner.does("rm -rf", func(command []string) { _ = os.RemoveAll(command[2]) })

	builder := NewBuilder(layout, site, filepath.Join(t.TempDir(), "scratch"), runner,
		func(line string) { *said = append(*said, line) })

	return builder, layout, said
}

// A build resolves, installs, and leaves a complete set where the layout says.
func TestABuildLeavesACompleteArtifactSet(t *testing.T) {
	runner, site := newScripted(), newSite()
	builder, layout, _ := aBuilder(t, runner, site)

	meta, err := builder.Build("11", false, "")
	if err != nil {
		t.Fatalf("build: %v\n%s", err, runner.transcript())
	}

	if meta.CoreVersion != "11.4.6" || meta.CoreMajor != "11" {
		t.Errorf("meta %+v", meta)
	}
	if meta.PHPVersion != "8.3.14" || meta.DBEngine != "mariadb:10.11" {
		t.Errorf("the install environment was not recorded: %+v", meta)
	}

	for _, part := range []func(string) (string, error){
		layout.TreePath, layout.DumpPath, layout.MetaPath, layout.CanonicalMarkerPath,
	} {
		path, err := part("11")
		if err != nil {
			t.Fatalf("path: %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was not written", path)
		}
	}

	// And the set is visible as a core somebody can be offered.
	versions, err := layout.VersionsOnDisk()
	if err != nil || len(versions) != 1 || versions[0] != "11" {
		t.Errorf("versions on disk %v (%v)", versions, err)
	}
}

// An existing set is not rebuilt by accident: a rebuild takes minutes and
// replaces what every environment for the core was seeded from.
func TestAnExistingSetIsNotRebuiltWithoutForce(t *testing.T) {
	runner, site := newScripted(), newSite()
	builder, layout, _ := aBuilder(t, runner, site)

	if _, err := builder.Build("11", false, ""); err != nil {
		t.Fatalf("build: %v", err)
	}

	_, err := builder.Build("11", false, "")
	if err == nil {
		t.Fatal("it rebuilt over an existing set")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("the refusal does not name the way to do it deliberately: %v", err)
	}
	if len(site.installed) != 1 {
		t.Errorf("it did the work anyway: %v", site.installed)
	}

	// The existing set is exactly as it was.
	versionDir, _ := layout.VersionDir("11")
	if _, err := os.Stat(filepath.Join(versionDir, MetaFilename)); err != nil {
		t.Errorf("the existing set was disturbed: %v", err)
	}
}

// A rebuild is staged beside the live set and swapped in, so the live set is
// exposed for two renames rather than for the minutes a resolve and an install
// take.
func TestARebuildIsStagedBesideTheLiveSetAndSwappedIn(t *testing.T) {
	runner, site := newScripted(), newSite()
	builder, layout, _ := aBuilder(t, runner, site)

	if _, err := builder.Build("11", false, ""); err != nil {
		t.Fatalf("build: %v", err)
	}

	versionDir, _ := layout.VersionDir("11")
	marker := filepath.Join(versionDir, "the-first-build-was-here")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The resolve must not have happened at the live path.
	runner.does("composer create-project", func(command []string) {
		if strings.HasPrefix(command[3], versionDir+string(os.PathSeparator)) {
			t.Errorf("the rebuild resolved into the live set at %s", command[3])
		}
		if _, err := os.Stat(marker); err != nil {
			t.Error("the live set was gone before the new one was built")
		}
	})

	if _, err := builder.Build("11", true, ""); err != nil {
		t.Fatalf("rebuild: %v\n%s", err, runner.transcript())
	}

	// The new set is in place, and the old one is gone with the staging.
	if _, err := os.Stat(marker); err == nil {
		t.Error("the old set survived the swap")
	}
	if _, err := os.Stat(filepath.Join(versionDir, MetaFilename)); err != nil {
		t.Errorf("the new set is not in place: %v", err)
	}
	if leftovers := stagingDirectories(t, layout); len(leftovers) != 0 {
		t.Errorf("staging survived a successful build: %v", leftovers)
	}
}

// stagingDirectories is what the base-artifacts directory holds that is not a
// finished set.
func stagingDirectories(t *testing.T, layout *Layout) []string {
	t.Helper()

	entries, err := os.ReadDir(layout.Dir)
	if err != nil {
		return nil
	}

	var staging []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), stagingPrefix) {
			staging = append(staging, entry.Name())
		}
	}

	return staging
}

// A failed rebuild costs the attempt and nothing else. This is the whole point
// of staging: a network blip used to leave the core with no artifact set at
// all and every environment for it unusable.
func TestAFailedRebuildLeavesTheExistingSetUntouched(t *testing.T) {
	for _, failing := range []string{"composer create-project", "composer validate"} {
		runner, site := newScripted(), newSite()
		builder, layout, said := aBuilder(t, runner, site)

		if _, err := builder.Build("11", false, ""); err != nil {
			t.Fatalf("build: %v", err)
		}
		versionDir, _ := layout.VersionDir("11")
		marker := filepath.Join(versionDir, "the-good-set")
		if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		runner.fails(failing)
		if _, err := builder.Build("11", true, ""); err == nil {
			t.Errorf("%s failed and the rebuild reported success", failing)
		}

		if _, err := os.Stat(marker); err != nil {
			t.Errorf("%s failed and took the existing set with it", failing)
		}
		if !strings.Contains(strings.Join(*said, "\n"), "are untouched") {
			t.Errorf("%s failed and nobody said the existing set survived: %v", failing, *said)
		}
		if leftovers := stagingDirectories(t, layout); len(leftovers) != 0 {
			t.Errorf("%s failed and left staging behind: %v", failing, leftovers)
		}
	}
}

// A first build that fails leaves no version directory at all: an existing one
// must always mean the last build completed.
func TestAFailedFirstBuildLeavesNothingThatReadsAsASet(t *testing.T) {
	runner, site := newScripted(), newSite()
	site.fail = errors.New("the site would not install")
	builder, layout, _ := aBuilder(t, runner, site)

	if _, err := builder.Build("11", false, ""); err == nil {
		t.Fatal("a failed install reported success")
	}

	versions, err := layout.VersionsOnDisk()
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("a failed build left a core somebody can be offered: %v", versions)
	}
	if leftovers := stagingDirectories(t, layout); len(leftovers) != 0 {
		t.Errorf("staging survived: %v", leftovers)
	}
}

// A build in progress is invisible to everything that lists cores: a half-built
// tree must never read as one somebody can be offered.
func TestABuildInProgressIsNotACoreAnybodyCanBeOffered(t *testing.T) {
	runner, site := newScripted(), newSite()
	builder, layout, _ := aBuilder(t, runner, site)

	runner.does("composer create-project", func([]string) {
		versions, err := layout.VersionsOnDisk()
		if err != nil {
			t.Fatalf("versions: %v", err)
		}
		if len(versions) != 0 {
			t.Errorf("a build in progress read as a finished core: %v", versions)
		}
	})

	if _, err := builder.Build("11", false, ""); err != nil {
		t.Fatalf("build: %v", err)
	}
}

// A staging directory left by a build that was killed outright is collected by
// the next build for that core — nothing else collects it, because prune
// protects the whole base-artifacts directory.
func TestStagingFromAnInterruptedBuildIsCollectedByTheNextOne(t *testing.T) {
	runner, site := newScripted(), newSite()
	builder, layout, said := aBuilder(t, runner, site)

	orphan := filepath.Join(layout.Dir, stagingPrefix+"11-deadbeef")
	if err := os.MkdirAll(filepath.Join(orphan, "11", "tree"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Another core's orphan is not this build's to collect.
	otherCore := filepath.Join(layout.Dir, stagingPrefix+"12-cafebabe")
	if err := os.MkdirAll(otherCore, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := builder.Build("11", false, ""); err != nil {
		t.Fatalf("build: %v", err)
	}

	if _, err := os.Stat(orphan); err == nil {
		t.Error("the orphaned staging directory survived")
	}
	if _, err := os.Stat(otherCore); err != nil {
		t.Error("it collected another core's build in progress")
	}
	if !strings.Contains(strings.Join(*said, "\n"), "interrupted build") {
		t.Errorf("it collected it silently: %v", *said)
	}
}

// A throwaway site is torn down whether or not the install worked: a failed
// install leaves containers and volumes exactly as a successful one does.
func TestTheThrowawaySiteIsTornDownEitherWay(t *testing.T) {
	for name, failing := range map[string]bool{"a successful install": false, "a failed install": true} {
		runner, site := newScripted(), newSite()
		if failing {
			site.fail = errors.New("nope")
		}
		builder, _, _ := aBuilder(t, runner, site)

		_, _ = builder.Build("11", false, "")

		if len(site.tornDown) != 1 {
			t.Errorf("%s: torn down %v", name, site.tornDown)
		}
		if len(site.installed) == 1 && site.tornDown[0] != site.installed[0] {
			t.Errorf("%s: it tore down %q having installed %q", name, site.tornDown[0], site.installed[0])
		}
	}
}

// An export that reported success and wrote nothing would give every
// environment for this core an empty database, and the first thing anyone
// would blame is the module.
func TestAnEmptyDumpIsABuildFailure(t *testing.T) {
	for name, write := range map[string]func(string){
		"no dump at all": func(string) {},
		"an empty dump": func(path string) {
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			_ = os.WriteFile(path, nil, 0o644)
		},
	} {
		runner := newScripted()
		site := newSite()
		site.writeDump = false
		builder, layout, _ := aBuilder(t, runner, site)

		dumpPath, _ := layout.DumpPath("11")
		runner.does("composer create-project", func([]string) { write(dumpPath) })

		if _, err := builder.Build("11", false, ""); err == nil {
			t.Errorf("%s was accepted as a build", name)
		}
	}
}

// A core with no stable release yet is the commonest reason a resolve finds
// nothing, and composer's output does not suggest there is a flag for it.
func TestAResolveThatFindsNothingNamesTheStabilityFlag(t *testing.T) {
	runner, site := newScripted(), newSite()
	runner.fails("composer create-project")
	builder, _, _ := aBuilder(t, runner, site)

	_, err := builder.Build("12", false, "")
	if err == nil {
		t.Fatal("a failed resolve reported success")
	}
	// Composer's own words first, then what can be done about them.
	if !strings.Contains(err.Error(), "composer create-project") {
		t.Errorf("composer's own output was dropped: %v", err)
	}
	if !strings.Contains(err.Error(), "--stability") {
		t.Errorf("the flag was not named: %v", err)
	}
}

// Once a stability was given, the constraint is not the obvious suspect any
// more, and repeating advice already taken buries what composer said.
func TestAResolveThatFailedWithAStabilityDoesNotRepeatTheAdvice(t *testing.T) {
	runner, site := newScripted(), newSite()
	runner.fails("composer create-project")
	builder, _, _ := aBuilder(t, runner, site)

	_, err := builder.Build("12", false, "alpha")
	if err == nil {
		t.Fatal("a failed resolve reported success")
	}
	if strings.Contains(err.Error(), "--stability") {
		t.Errorf("it repeated advice already taken: %v", err)
	}
}

// A stability that is not one composer knows is refused before any work.
func TestAnUnknownStabilityIsRefusedBeforeAnyWork(t *testing.T) {
	runner, site := newScripted(), newSite()
	builder, _, _ := aBuilder(t, runner, site)

	if _, err := builder.Build("12", false, "nearly-ready"); err == nil {
		t.Fatal("it built with a stability composer does not know")
	}
	if len(runner.ran) != 0 {
		t.Errorf("it started work anyway:\n%s", runner.transcript())
	}
}

// The canonical tree stays module-free, and drush goes only into the
// throwaway: a base tree carrying it would put it in every environment.
func TestTheCanonicalTreeIsResolvedWithNothingAddedToIt(t *testing.T) {
	runner, site := newScripted(), newSite()
	builder, _, _ := aBuilder(t, runner, site)

	if _, err := builder.Build("11", false, ""); err != nil {
		t.Fatalf("build: %v", err)
	}

	for _, line := range runner.ran {
		if strings.Contains(line, "composer require") {
			t.Errorf("something was added to the canonical tree: %s", line)
		}
	}
	if !runner.didRun("composer validate") {
		t.Errorf("the resolved tree was not validated:\n%s", runner.transcript())
	}
}

// A core that is not a whole major is refused: it would become a directory
// name under the base-artifacts directory.
func TestACoreThatIsNotAMajorIsRefused(t *testing.T) {
	runner, site := newScripted(), newSite()
	builder, _, _ := aBuilder(t, runner, site)

	for _, core := range []string{"11.2", "", "../escape", "v11"} {
		if _, err := builder.Build(core, false, ""); err == nil {
			t.Errorf("%q was built", core)
		}
	}
	if len(runner.ran) != 0 {
		t.Errorf("it started work anyway:\n%s", runner.transcript())
	}
}

// The swap is two renames, and the residual window between them is the one
// place a build can leave a core with no version directory. Both failures name
// every path, because nothing has been deleted and the recovery is to move a
// directory by hand.
func TestARetireThatFailsSaysTheExistingSetIsUntouched(t *testing.T) {
	runner, site := newScripted(), newSite()
	builder, layout, _ := aBuilder(t, runner, site)

	if _, err := builder.Build("11", false, ""); err != nil {
		t.Fatalf("build: %v", err)
	}
	versionDir, _ := layout.VersionDir("11")

	// Something already occupying the name the outgoing set is renamed to.
	runner.does("composer create-project", func([]string) {
		staging := stagingDirectories(t, layout)
		if len(staging) != 1 {
			t.Fatalf("staging %v", staging)
		}
		blocked := filepath.Join(layout.Dir, staging[0], retiredDir)
		if err := os.MkdirAll(blocked, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(blocked, "in-the-way"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	})

	_, err := builder.Build("11", true, "")
	if err == nil {
		t.Fatal("a failed retire reported success")
	}
	if !strings.Contains(err.Error(), "existing set is untouched") {
		t.Errorf("the refusal does not say the live set survived: %v", err)
	}
	// And it did: nothing was deleted.
	if _, statErr := os.Stat(filepath.Join(versionDir, MetaFilename)); statErr != nil {
		t.Errorf("the existing set was disturbed anyway: %v", statErr)
	}
	// The finished set is named, so it can be moved by hand.
	if !strings.Contains(err.Error(), stagingPrefix) {
		t.Errorf("the refusal does not name where the new set is: %v", err)
	}
}

// And a swap that cannot put the new set in place names the staging directory
// as a whole — true whether or not there was a previous set, which is why
// there is no branch on it.
func TestASwapThatFailsNamesEverythingAndDeletesNothing(t *testing.T) {
	runner, site := newScripted(), newSite()
	builder, layout, _ := aBuilder(t, runner, site)

	versionDir, _ := layout.VersionDir("11")
	// Occupied by the time the swap happens, but not when the build starts —
	// so this is a first build and the retire step is skipped.
	runner.does("composer create-project", func([]string) {
		if err := os.MkdirAll(versionDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(versionDir, "in-the-way"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	})

	_, err := builder.Build("11", false, "")
	if err == nil {
		t.Fatal("a failed swap reported success")
	}
	if !strings.Contains(err.Error(), "Nothing has been deleted") {
		t.Errorf("the refusal does not say what is safe: %v", err)
	}
	if !strings.Contains(err.Error(), "by hand") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}
	// The finished set is still there to be moved.
	staging := stagingDirectories(t, layout)
	if len(staging) != 1 {
		t.Fatalf("the finished set was cleaned up out from under the recovery: %v", staging)
	}
	if _, statErr := os.Stat(
		filepath.Join(layout.Dir, staging[0], "11", MetaFilename),
	); statErr != nil {
		t.Errorf("the finished set is not where the message says: %v", statErr)
	}
}

// A builder with nowhere to report progress still builds.
func TestABuilderWithNoLogStillBuilds(t *testing.T) {
	runner, site := newScripted(), newSite()
	layout := NewLayout(filepath.Join(t.TempDir(), "base-artifacts"))
	runner.does("composer create-project", func(command []string) {
		if err := os.MkdirAll(command[3], 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(command[3], "composer.lock"),
			[]byte(`{"packages":[{"name":"drupal/core","version":"11.4.6"}]}`), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	})
	runner.does("rm -rf", func(command []string) { _ = os.RemoveAll(command[2]) })

	builder := NewBuilder(layout, site, filepath.Join(t.TempDir(), "scratch"), runner, nil)

	if _, err := builder.Build("11", false, ""); err != nil {
		t.Fatalf("build: %v\n%s", err, runner.transcript())
	}
}

// No step of a build may be swallowed: one that carried on past a failed
// resolve or a failed validate would write a meta and a canonical marker over
// a tree that is not what they describe, and every environment seeded from it
// afterwards would be wrong in a way nothing downstream could detect.
//
// Written as one property rather than a test per command, for the same reason
// the adapter's is: the individual assertions would all say the same thing,
// and a sequence that grows a step would silently not get one.
func TestNoBuildStepIsSwallowed(t *testing.T) {
	// One scene for every attempt, so the absolute paths in each command line
	// stay the same and the failure being scripted matches something.
	root := filepath.Join(t.TempDir(), "base-artifacts")
	scratch := filepath.Join(t.TempDir(), "scratch")

	build := func(runner *scriptedRunner) error {
		if err := os.RemoveAll(root); err != nil {
			t.Fatalf("remove: %v", err)
		}
		runner.does("composer create-project", func(command []string) {
			if err := os.MkdirAll(command[3], 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(command[3], "composer.lock"),
				[]byte(`{"packages":[{"name":"drupal/core","version":"11.4.6"}]}`), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		})
		runner.does("rm -rf", func(command []string) { _ = os.RemoveAll(command[2]) })

		_, err := NewBuilder(NewLayout(root), newSite(), scratch, runner, nil).Build("11", false, "")

		return err
	}

	healthy := newScripted()
	if err := build(healthy); err != nil {
		t.Fatalf("the healthy run failed, so there is nothing to vary: %v\n%s", err, healthy.transcript())
	}

	var swallowed []string
	for _, command := range healthy.ran {
		// Cleanup is deliberately not fatal: every caller of it is already on
		// a path where the outcome is decided, and failing a finished build
		// over a directory that would not go is the wrong trade.
		if strings.HasPrefix(command, "rm -rf") {
			continue
		}
		// Keyed on the head of the command rather than the whole line,
		// because a staging directory is named with fresh random bytes on
		// every attempt — so the full line from one run matches nothing in the
		// next, and a key that matches nothing silently tests nothing.
		failing := newScripted()
		failing.fails(commandHead(command))
		if err := build(failing); err == nil {
			swallowed = append(swallowed, command)
		}
	}

	if len(swallowed) > 0 {
		t.Errorf("these failed and the build reported success:\n  %s", strings.Join(swallowed, "\n  "))
	}
}

// commandHead is the part of a command line that is the same on every attempt.
func commandHead(command string) string {
	fields := strings.Fields(command)
	if len(fields) > 3 {
		fields = fields[:3]
	}

	return strings.Join(fields, " ")
}

// A cleanup that will not go is reported and does not fail a build that is
// already decided either way.
func TestCleanupThatWillNotGoIsReportedRatherThanFatal(t *testing.T) {
	runner, site := newScripted(), newSite()
	builder, _, said := aBuilder(t, runner, site)
	runner.fails("rm -rf")

	if _, err := builder.Build("11", false, ""); err != nil {
		t.Fatalf("a finished build failed over its own cleanup: %v", err)
	}
	if !strings.Contains(strings.Join(*said, "\n"), "Could not remove") {
		t.Errorf("the cleanup failure was silent: %v", *said)
	}
}

// A build that cannot write where it was told to is a failure that names the
// path, not a half-set that reads as finished. Each of the three write points
// is checked, because a missing meta or canonical marker makes the set read as
// incomplete and stops prune protecting it while the command says it worked.
func TestABuildThatCannotWriteIsAFailureAndLeavesNoSet(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into every directory regardless of its mode")
	}

	runner, site := newScripted(), newSite()
	builder, layout, _ := aBuilder(t, runner, site)

	// Readable but not writable, so the staging directory cannot be made.
	if err := os.MkdirAll(layout.Dir, 0o555); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(layout.Dir, 0o755) })

	if _, err := builder.Build("11", false, ""); err == nil {
		t.Fatal("it built into a directory it cannot write")
	}
	if len(site.installed) != 0 {
		t.Errorf("it did the expensive work anyway: %v", site.installed)
	}
}

// A staged set whose meta or marker cannot be written takes the whole set with
// it: either one missing makes the set read as incomplete and stops prune
// protecting it, while the command reports a successful build.
//
// Each is blocked on its own, because blocking both at once — locking the
// directory — cannot tell which check did the refusing, and the second one is
// unreachable if the first already covers it.
func TestASetMissingEitherSidecarIsNotOfferedAsACore(t *testing.T) {
	for name, blocked := range map[string]string{
		"the meta":             MetaFilename,
		"the canonical marker": CanonicalMarker,
	} {
		runner, site := newScripted(), newSite()
		builder, layout, _ := aBuilder(t, runner, site)

		// A directory where the file has to go: the write fails, the rest of
		// the directory stays writable.
		var staged string
		runner.does("composer create-project", func(command []string) { staged = filepath.Dir(command[3]) })
		site.afterDump = func() {
			if err := os.MkdirAll(filepath.Join(staged, blocked), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}

		if _, err := builder.Build("11", false, ""); err == nil {
			t.Errorf("a set with no %s was reported as built", name)
		}

		versions, err := layout.VersionsOnDisk()
		if err != nil {
			t.Fatalf("versions: %v", err)
		}
		if len(versions) != 0 {
			t.Errorf("a set with no %s is offered as a core: %v", name, versions)
		}
	}
}

// A tree the resolve left without a lock file, or with one naming no core, is
// a build failure: the meta records the exact resolved version and every later
// staleness decision is made against it.
func TestATreeWithNoResolvedCoreVersionIsABuildFailure(t *testing.T) {
	for name, lock := range map[string]string{
		"no lock file at all":     "",
		"a lock naming no core":   `{"packages":[{"name":"drupal/pathauto","version":"1.0.0"}]}`,
		"a lock that is not JSON": "not json",
		"a core with no version":  `{"packages":[{"name":"drupal/core"}]}`,
	} {
		runner, site := newScripted(), newSite()
		layout := NewLayout(filepath.Join(t.TempDir(), "base-artifacts"))
		runner.does("composer create-project", func(command []string) {
			if err := os.MkdirAll(command[3], 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if lock == "" {
				return
			}
			if err := os.WriteFile(
				filepath.Join(command[3], "composer.lock"), []byte(lock), 0o644,
			); err != nil {
				t.Fatalf("write: %v", err)
			}
		})
		runner.does("rm -rf", func(command []string) { _ = os.RemoveAll(command[2]) })

		builder := NewBuilder(layout, site, filepath.Join(t.TempDir(), "scratch"), runner, nil)

		_, err := builder.Build("11", false, "")
		if err == nil {
			t.Errorf("%s was accepted as a build", name)

			continue
		}
		// The diagnosis has to say which of the two it was: "no lock file" and
		// "a lock that does not name core" send somebody to different places.
		if lock == "" && !strings.Contains(err.Error(), "no readable composer.lock") {
			t.Errorf("%s was reported as %v", name, err)
		}
		if len(site.installed) != 0 {
			t.Errorf("%s: it installed a site for a tree it could not identify", name)
		}
	}
}

// A scratch directory that cannot be made is a failure before the site is
// installed into it.
func TestAScratchDirectoryThatCannotBeMadeIsAFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into every directory regardless of its mode")
	}

	runner, site := newScripted(), newSite()
	layout := NewLayout(filepath.Join(t.TempDir(), "base-artifacts"))
	runner.does("composer create-project", func(command []string) {
		if err := os.MkdirAll(command[3], 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(command[3], "composer.lock"),
			[]byte(`{"packages":[{"name":"drupal/core","version":"11.4.6"}]}`), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	})
	runner.does("rm -rf", func(command []string) { _ = os.RemoveAll(command[2]) })

	locked := t.TempDir()
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	builder := NewBuilder(layout, site, filepath.Join(locked, "scratch"), runner, nil)

	if _, err := builder.Build("11", false, ""); err == nil {
		t.Fatal("it installed into a scratch directory it could not make")
	}
	if len(site.installed) != 0 {
		t.Errorf("it installed anyway: %v", site.installed)
	}
}

// The whole path set comes from one validation, so a core that is not a major
// is refused once rather than at each path in turn.
func TestThePathSetIsResolvedTogether(t *testing.T) {
	layout := NewLayout("/cockpit/base-artifacts")

	paths, err := layout.PathsFor("11")
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	for name, got := range map[string]string{
		"version dir": paths.VersionDir,
		"tree":        paths.Tree,
		"dump":        paths.Dump,
		"meta":        paths.Meta,
		"marker":      paths.CanonicalMarker,
	} {
		if !strings.HasPrefix(got, "/cockpit/base-artifacts/11") {
			t.Errorf("%s is %q", name, got)
		}
	}

	for _, core := range []string{"11.2", "", "../escape"} {
		if _, err := layout.PathsFor(core); err == nil {
			t.Errorf("%q was resolved", core)
		}
	}
}

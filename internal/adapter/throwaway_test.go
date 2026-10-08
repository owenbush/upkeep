package adapter

import (
	"strings"
	"testing"
)

// A throwaway install is a sequence, and every step depends on the one before
// it: the tree copied before the project is configured, the project started
// before composer runs in it, drush present before the site is installed, and
// the dump taken only once there is a site to dump.
func TestAThrowawayInstallRunsItsStepsInOrder(t *testing.T) {
	runner := newRunner().
		answer("php -r", "8.3.14\n").
		answer("describe", `{"raw":{"dbinfo":{"database_type":"mariadb","database_version":"10.11"}}}`)

	environment, err := NewThrowawaySite(runner, nil).CleanInstallAndDump(
		"11", "/artifacts/11/tree", "/scratch/upkeep-base-d11-abc", "upkeep-base-d11-abc", "/artifacts/11/dump.sql.gz", "11.4.8", "",
	)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, runner.transcript())
	}

	steps := [][]string{
		{"cp -a", "/artifacts/11/tree", "/scratch/upkeep-base-d11-abc"},
		{"ddev config", "--project-type=drupal11", "--project-name=upkeep-base-d11-abc"},
		{"ddev start"},
		{"composer require drush/drush"},
		{"drush site:install minimal"},
		{"export-db", "--gzip=true"},
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

	if environment.PHPVersion != "8.3.14" || environment.DBEngine != "mariadb:10.11" {
		t.Errorf("the install environment was not read: %+v", environment)
	}
	// The dump goes where it was asked for, not somewhere derived.
	if !runner.didRun("export-db", "--file=/artifacts/11/dump.sql.gz") {
		t.Errorf("the dump went somewhere else:\n%s", runner.transcript())
	}
}

// The canonical tree stays module-free: drush goes only into the throwaway
// copy, which is why the copy exists at all.
func TestDrushGoesOnlyIntoTheThrowawayCopy(t *testing.T) {
	runner := newRunner()

	if _, err := NewThrowawaySite(runner, nil).CleanInstallAndDump(
		"11", "/artifacts/11/tree", "/scratch/throwaway", "throwaway", "/artifacts/11/dump.sql.gz", "11.4.8", "",
	); err != nil {
		t.Fatalf("install: %v", err)
	}

	for _, line := range runner.ran {
		if strings.Contains(line, "composer require") && strings.Contains(line, "/artifacts/") {
			t.Errorf("something was installed into the canonical tree: %s", line)
		}
	}
}

// The database engine is recorded as "type:version", and "unknown" when the
// engine will not say — the meta is read by a human later, and a blank would
// read as a field nobody filled in.
func TestTheDatabaseEngineIsRecordedOrSaidToBeUnknown(t *testing.T) {
	for name, scripted := range map[string]struct {
		describe string
		fails    bool
		want     string
	}{
		"type and version": {
			describe: `{"raw":{"dbinfo":{"database_type":"mariadb","database_version":"10.11"}}}`,
			want:     "mariadb:10.11",
		},
		"type only": {
			describe: `{"raw":{"dbinfo":{"database_type":"postgres"}}}`,
			want:     "postgres",
		},
		"no dbinfo at all": {
			describe: `{"raw":{"status":"running"}}`,
			want:     "unknown",
		},
		"unparseable":                 {describe: "not json", want: "unknown"},
		"the engine would not answer": {fails: true, want: "unknown"},
	} {
		runner := newRunner().answer("describe", scripted.describe)
		if scripted.fails {
			runner.fails("describe")
		}

		environment, err := NewThrowawaySite(runner, nil).CleanInstallAndDump(
			"11", "/tree", "/scratch/throwaway", "throwaway", "/dump.sql.gz", "11.4.8", "",
		)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if environment.DBEngine != scripted.want {
			t.Errorf("%s: engine %q, want %q", name, environment.DBEngine, scripted.want)
		}
	}
}

// No step of a throwaway install may be swallowed: a build that carried on
// past a failed one would export a dump of a site that was never installed.
func TestNoThrowawayStepIsSwallowed(t *testing.T) {
	install := func(runner *recordingRunner) error {
		runner.answer("php -r", "8.3.14\n")
		_, err := NewThrowawaySite(runner, nil).CleanInstallAndDump(
			"11", "/tree", "/scratch/throwaway", "throwaway", "/dump.sql.gz", "11.4.8", "",
		)

		return err
	}

	healthy := newRunner()
	if err := install(healthy); err != nil {
		t.Fatalf("the healthy run failed: %v", err)
	}

	var swallowed []string
	for _, command := range healthy.mustSucceed() {
		// The description is read for a field that has an "unknown" answer, so
		// its failure is deliberately not fatal.
		if strings.Contains(command, "describe") {
			continue
		}
		failing := newRunner()
		failing.fails(command)
		if err := install(failing); err == nil {
			swallowed = append(swallowed, command)
		}
	}

	if len(swallowed) > 0 {
		t.Errorf("these failed and the install reported success:\n  %s", strings.Join(swallowed, "\n  "))
	}
}

// Teardown goes through the engine's delete before the tree: a bare tree
// removal would leave containers running and volumes orphaned.
func TestThrowawayTeardownDeletesThroughTheEngineFirst(t *testing.T) {
	environment := anEnvironment(t)
	runner := newRunner()

	NewThrowawaySite(runner, nil).Teardown(environment.ProjectPath, "throwaway")

	del, remove := runner.indexOfCommand("ddev delete"), runner.indexOfCommand("rm -rf")
	if del < 0 || remove < 0 {
		t.Fatalf("missing steps:\n%s", runner.transcript())
	}
	if del > remove {
		t.Errorf("the tree went before the containers:\n%s", runner.transcript())
	}
}

// A build that failed before the project was configured has nothing
// registered to delete, and that is not a problem worth reporting as one.
func TestAThrowawayNeverRegisteredIsTornDownAnyway(t *testing.T) {
	environment := anEnvironment(t)
	runner := newRunner()
	runner.fails("ddev delete")

	var said []string
	NewThrowawaySite(runner, func(line string) { said = append(said, line) }).
		Teardown(environment.ProjectPath, "throwaway")

	if !runner.didRun("rm -rf") {
		t.Errorf("the tree survived a failed engine delete:\n%s", runner.transcript())
	}
	if !strings.Contains(strings.Join(said, "\n"), "never have been registered") {
		t.Errorf("the failure was silent: %v", said)
	}
}

// A tree that is not there is nothing to tear down.
func TestAThrowawayThatIsNotThereIsNotTornDown(t *testing.T) {
	runner := newRunner()

	NewThrowawaySite(runner, nil).Teardown("/scratch/never-existed", "throwaway")

	if len(runner.ran) != 0 {
		t.Errorf("it tore down something that does not exist:\n%s", runner.transcript())
	}
}

// A teardown that cannot remove the tree is worth saying and not worth failing
// a finished build over: the artifacts are already written.
func TestATreeThatWillNotGoIsReportedRatherThanFatal(t *testing.T) {
	environment := anEnvironment(t)
	runner := newRunner()
	runner.fails("rm -rf")

	var said []string
	NewThrowawaySite(runner, func(line string) { said = append(said, line) }).
		Teardown(environment.ProjectPath, "throwaway")

	if !strings.Contains(strings.Join(said, "\n"), "could not be removed") {
		t.Errorf("it went silently: %v", said)
	}
}

// The constraint reaches the container, rather than stopping at the flag.
func TestAToolchainConstraintReachesTheComposerRequire(t *testing.T) {
	runner := newRunner().
		answer("php -r", "8.3.14\n").
		answer("describe", `{"raw":{"dbinfo":{"database_type":"mariadb","database_version":"10.11"}}}`)

	if _, err := NewThrowawaySite(runner, nil).CleanInstallAndDump(
		"12", "/tree", "/scratch/throwaway", "throwaway", "/dump.sql.gz",
		"12.0.0-beta1", "^13@dev",
	); err != nil {
		t.Fatalf("install: %v\n%s", err, runner.transcript())
	}

	if !runner.didRun("composer require drush/drush:^13@dev") {
		t.Errorf("the constraint did not reach composer:\n%s", runner.transcript())
	}
	// And not the bare package as well, which would resolve to a release that
	// cannot work and then be overwritten or conflict.
	if runner.didRun("composer require drush/drush --no-interaction") {
		t.Errorf("it required the bare package too:\n%s", runner.transcript())
	}
}

// A toolchain that will not install on a pre-release core says what can be
// done about it, after composer's own words.
//
// Without this the operator gets forty lines of composer resolver output whose
// actual meaning — no release of this tool resolves against this core yet — is
// never stated, and which never mentions that there is a flag.
func TestAFailedToolchainInstallNamesTheFlag(t *testing.T) {
	runner := newRunner().
		answer("php -r", "8.3.14\n").
		fails("composer require")

	_, err := NewThrowawaySite(runner, nil).CleanInstallAndDump(
		"12", "/tree", "/scratch/throwaway", "throwaway", "/dump.sql.gz", "12.0.0-beta1", "",
	)
	if err == nil {
		t.Fatal("a failed toolchain install was swallowed")
	}
	if !strings.Contains(err.Error(), "--"+ToolRequireFlag) {
		t.Errorf("the failure did not name the flag: %v", err)
	}

	// On a released core the same failure gets no such advice: the toolchain
	// resolves there, so this would be a guess dressed as a diagnosis.
	released := newRunner().answer("php -r", "8.3.14\n").fails("composer require")
	_, err = NewThrowawaySite(released, nil).CleanInstallAndDump(
		"11", "/tree", "/scratch/throwaway", "throwaway", "/dump.sql.gz", "11.4.8", "",
	)
	if err == nil {
		t.Fatal("a failed toolchain install was swallowed")
	}
	if strings.Contains(err.Error(), "--"+ToolRequireFlag) {
		t.Errorf("a released core was given pre-release advice: %v", err)
	}
}

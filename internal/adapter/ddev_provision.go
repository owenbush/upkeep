package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/filesystem"
)

// EnsureEnv provisions the environment for (module, core major), or reuses the
// existing one when it is present and healthy.
func (d *DdevContrib) EnsureEnv(module cockpit.Module, coreMajor string) (Environment, error) {
	projectName, err := EngineProjectName(module.Name, coreMajor)
	if err != nil {
		return Environment{}, err
	}
	projectPath := d.projectPath(projectName)

	artifactMeta, err := d.requireArtifactMeta(coreMajor)
	if err != nil {
		return Environment{}, err
	}

	if isDirectory(projectPath) {
		reasons := d.staleReasons(module, coreMajor, artifactMeta, projectName, projectPath)
		if len(reasons) == 0 {
			d.log(fmt.Sprintf("Reusing existing environment %s at %s.", projectName, projectPath))

			return d.reuse(module, coreMajor, projectName, projectPath)
		}

		// Re-provisioning destroys the working copy, so anything held only
		// there stops the rebuild rather than being taken down with it.
		if moduleDir := moduleWorkingCopy(projectPath); isDirectory(moduleDir) {
			status := InspectWorkingCopy(moduleDir, d.runner)
			if status.HasLocalWork() {
				return Environment{}, fmt.Errorf(
					"environment %s is stale and needs re-provisioning, but the module working copy "+
						"has local work:\n  %s\nPush or stash your work, then re-run",
					projectName, strings.Join(status.Describe(), "\n  "),
				)
			}
		}

		d.log(fmt.Sprintf("Existing environment %s is stale — re-provisioning:", projectName))
		for _, reason := range reasons {
			d.log("  - " + reason)
		}
		if err := d.teardownProject(projectName, projectPath); err != nil {
			return Environment{}, err
		}
	}

	return d.provision(module, coreMajor, artifactMeta, projectName, projectPath)
}

// Teardown disposes of the environment for (module, core major), containers
// and volumes included.
func (d *DdevContrib) Teardown(module cockpit.Module, coreMajor string) error {
	projectName, err := EngineProjectName(module.Name, coreMajor)
	if err != nil {
		return err
	}
	projectPath := d.projectPath(projectName)

	// Both, because either half can outlive the other: a deleted tree can
	// leave a registration behind, and an unregistered project can leave a
	// tree. Nothing to tear down is a fact, not a failure.
	if _, registered := d.describe(projectName); !isDirectory(projectPath) && !registered {
		d.log(fmt.Sprintf("Environment %s does not exist — nothing to tear down.", projectName))

		return nil
	}

	if moduleDir := moduleWorkingCopy(projectPath); isDirectory(moduleDir) {
		status := InspectWorkingCopy(moduleDir, d.runner)
		if status.HasLocalWork() {
			return fmt.Errorf(
				"refusing to tear down %s: the module working copy has local work:\n  %s\n"+
					"Push or stash your work first",
				projectName, strings.Join(status.Describe(), "\n  "),
			)
		}
	}

	return d.teardownProject(projectName, projectPath)
}

// staleReasons is the reuse decision: the meta dotfile present, parseable, and
// matching the request and the pinned engine identity — and the engine still
// knowing the project. Any reason forces re-provisioning.
func (d *DdevContrib) staleReasons(
	module cockpit.Module,
	coreMajor string,
	artifactMeta baseartifact.Meta,
	projectName, projectPath string,
) []string {
	metaPath := EnvMetaPath(projectPath)
	contents, err := os.ReadFile(metaPath)
	if err != nil {
		// The meta dotfile is written as the LAST provisioning step, so its
		// absence means an interrupted provision and never a reusable
		// environment.
		return []string{fmt.Sprintf(
			"No %s completion marker — a previous provision did not finish.", EnvMetaFilename,
		)}
	}

	meta, err := EnvMetaFromYAML(string(contents))
	if err != nil {
		return []string{"Environment meta is unreadable: " + err.Error()}
	}

	reasons := meta.StaleReasons(module.Name, coreMajor, artifactMeta.CoreVersion, EngineAddOnVersion)

	if _, registered := d.describe(projectName); !registered {
		reasons = append(reasons, "The engine no longer reports the project (describe failed).")
	}

	return reasons
}

// reuse takes an existing healthy environment, starting it if it is stopped.
func (d *DdevContrib) reuse(
	module cockpit.Module,
	coreMajor, projectName, projectPath string,
) (Environment, error) {
	described, registered := d.describe(projectName)
	if !registered {
		return Environment{}, fmt.Errorf(
			"engine lost project %s between health check and reuse", projectName,
		)
	}

	if described.StringOrNull("status") != "running" {
		if err := d.startStopped(projectName, projectPath); err != nil {
			return Environment{}, err
		}
		if described, registered = d.describe(projectName); !registered {
			return Environment{}, fmt.Errorf("project %s did not come back after start", projectName)
		}
	}

	// Stamp the reuse. `prune --older-than` filters on this: without it, age
	// is time-since-creation and prune deletes environments in daily use.
	if err := StampLastUsed(projectPath, time.Now()); err != nil {
		return Environment{}, err
	}

	return Environment{
		ModuleName:  module.Name,
		CoreMajor:   coreMajor,
		ProjectName: projectName,
		ProjectPath: projectPath,
		PrimaryURL:  described.StringOrNull("primary_url"),
		Reused:      true,
	}, nil
}

// startStopped starts an environment that already exists.
//
// Three things were wrong with doing this as a bare run, all found together
// after a reboot, when the engine stalled starting its file sync and upkeep
// said "starting it" and then nothing:
//
//   - It was silent. The engine's own output is shown only under -v, which is
//     right for composer and wrong for a step that can block, because nothing
//     distinguished stuck from slow.
//   - It inherited the one-hour default, which exists for composer resolves
//     and site installs. Starting a project whose images and volumes already
//     exist takes seconds; ten minutes covers an image pull after an engine
//     upgrade and still fails long before anyone gives up and kills it.
//   - When it did time out, nothing said why.
//
// The file-sync case gets named outright. It is the common one on macOS — an
// unclean shutdown leaves the sync session wedged — and the fix is a single
// safe command, since the host files are the source of truth and only the
// synced copy inside the container runtime is rebuilt.
func (d *DdevContrib) startStopped(projectName, projectPath string) error {
	d.log(fmt.Sprintf(
		"Environment %s is stopped — starting it. This usually takes under a minute; "+
			"-v shows the engine's own output.",
		projectName,
	))

	_, err := d.runner.Run([]string{"ddev", "start", "-y"}, projectPath, startTimeout)
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "Mutagen") {
		return err
	}

	return fmt.Errorf(
		"%w\n\nThe engine stopped while starting Mutagen, its file sync on macOS — usually a session "+
			"left wedged by an unclean shutdown or a reboot. Reset it and re-run:\n"+
			"  cd %s && ddev mutagen reset\nThat rebuilds only the synced copy inside the container "+
			"runtime; the files on disk are untouched",
		err, projectPath,
	)
}

// provision builds an environment from nothing.
//
// Every step is ordered so that the completion marker is the last thing
// written: a crash anywhere before it leaves no marker, and the next ensure
// re-provisions rather than reusing a half-built tree.
func (d *DdevContrib) provision(
	module cockpit.Module,
	coreMajor string,
	artifactMeta baseartifact.Meta,
	projectName, projectPath string,
) (Environment, error) {
	d.log(fmt.Sprintf(
		"Provisioning environment %s (module %s, Drupal %s) ...", projectName, module.Name, coreMajor,
	))

	// Asked before anything is built. Engine project names are global to the
	// machine while the projects root is configurable, so a moved root
	// collides with whatever the old one registered — and the engine refuses
	// that at config time, which is after a codebase has been seeded and a
	// repository cloned. Same refusal, a minute earlier, and with the recovery
	// command in it.
	described, _ := d.describe(projectName)
	registration := ProjectRegistration{
		ProjectName: projectName, RegisteredRoot: described.StringOrNull("approot"),
	}
	if registration.ConflictsWith(projectPath) {
		return Environment{}, registration.ConflictError(projectPath)
	}

	if err := filesystem.EnsureDirectory(d.projectsRoot, filesystem.ModeSharedDir); err != nil {
		return Environment{}, err
	}

	environment, err := d.build(module, coreMajor, artifactMeta, projectName, projectPath)
	if err == nil {
		return environment, nil
	}

	d.log(fmt.Sprintf("Provisioning failed — tearing down partial environment %s.", projectName))
	if cleanup := d.teardownProject(projectName, projectPath); cleanup != nil {
		d.log("Cleanup after failed provisioning also failed: " + cleanup.Error())
	}

	// The pre-flight above catches this when the engine can still describe the
	// old registration. It cannot when the old directory is gone but the
	// record survives — so the engine's own refusal is translated too, rather
	// than surfacing as an unactionable wall of its output.
	if IsRootConflict(err.Error()) {
		return Environment{}, ProjectRegistration{ProjectName: projectName}.
			RootConflictError(projectPath, err)
	}

	return Environment{}, err
}

// build is provisioning's forward path, split out so provision owns the one
// teardown-on-failure and this reads as the sequence it is.
func (d *DdevContrib) build(
	module cockpit.Module,
	coreMajor string,
	artifactMeta baseartifact.Meta,
	projectName, projectPath string,
) (Environment, error) {
	treePath, err := d.layout.TreePath(coreMajor)
	if err != nil {
		return Environment{}, err
	}
	dumpPath, err := d.layout.DumpPath(coreMajor)
	if err != nil {
		return Environment{}, err
	}

	d.log("Seeding codebase from the canonical base tree ...")
	if _, err := d.runner.Run([]string{"cp", "-a", treePath, projectPath}, "", 0); err != nil {
		return Environment{}, err
	}

	d.log("Cloning the module working copy ...")
	if _, err := d.runner.Run([]string{
		"git", "clone", HTTPSURL(module.Project), moduleWorkingCopy(projectPath),
	}, "", 0); err != nil {
		return Environment{}, err
	}

	d.log("Configuring the engine project ...")
	if _, err := d.runner.Run([]string{
		"ddev", "config",
		"--project-type=drupal" + coreMajor,
		"--docroot=web",
		"--project-name=" + projectName,
	}, projectPath, 0); err != nil {
		return Environment{}, err
	}

	d.log(fmt.Sprintf(
		"Installing engine add-on %s at pinned version %s ...", EngineAddOnName, EngineAddOnVersion,
	))
	if _, err := d.runner.Run([]string{
		"ddev", "add-on", "get", EngineAddOnName, "--version", EngineAddOnVersion,
	}, projectPath, 0); err != nil {
		return Environment{}, err
	}
	if err := d.adaptAddOnConfig(projectPath); err != nil {
		return Environment{}, err
	}
	if err := d.ensureFixtureAddOn(projectPath); err != nil {
		return Environment{}, err
	}

	d.log("Starting the environment ...")
	if _, err := d.runner.Run([]string{"ddev", "start", "-y"}, projectPath, 0); err != nil {
		return Environment{}, err
	}

	d.log("Requiring drush (container-side composer) ...")
	if _, err := d.runner.Run([]string{
		"ddev", "composer", "require", "drush/drush", "--no-interaction",
	}, projectPath, 0); err != nil {
		return Environment{}, err
	}

	if err := d.wireModule(module, projectPath); err != nil {
		return Environment{}, err
	}

	d.log("Restoring the clean-install database snapshot ...")
	if _, err := d.runner.Run([]string{
		"ddev", "import-db", "--file=" + dumpPath,
	}, projectPath, 0); err != nil {
		return Environment{}, err
	}

	described, err := d.assertBootstraps(projectName, projectPath, coreMajor)
	if err != nil {
		return Environment{}, err
	}

	// Written LAST: the completion marker. A crash before this line leaves no
	// marker, and the next ensure re-provisions. The write is checked — a
	// silently missing marker would make every later invocation tear this
	// environment down and rebuild it.
	now := time.Now()
	meta := EnvironmentMeta{
		ModuleName:      module.Name,
		CoreMajor:       coreMajor,
		SeedCoreVersion: artifactMeta.CoreVersion,
		AddOnVersion:    EngineAddOnVersion,
		CreatedAt:       now,
		LastUsedAt:      now,
	}
	if err := meta.WriteTo(projectPath); err != nil {
		return Environment{}, err
	}

	return Environment{
		ModuleName:  module.Name,
		CoreMajor:   coreMajor,
		ProjectName: projectName,
		ProjectPath: projectPath,
		PrimaryURL:  described.StringOrNull("primary_url"),
		Reused:      false,
	}, nil
}

// assertBootstraps is the provisioning health gate: the environment must
// bootstrap and report the requested core major, read from the project itself
// rather than assumed from what was seeded.
func (d *DdevContrib) assertBootstraps(
	projectName, projectPath, coreMajor string,
) (EngineDescription, error) {
	version, err := d.runner.Run(
		[]string{"ddev", "drush", "status", "--field=drupal-version"}, projectPath, 0,
	)
	if err != nil {
		return EngineDescription{}, err
	}

	version = strings.TrimSpace(version)
	if !strings.HasPrefix(version, coreMajor+".") {
		return EngineDescription{}, fmt.Errorf(
			"environment %s reports Drupal %q, expected major %s", projectName, version, coreMajor,
		)
	}
	d.log("Environment reports Drupal " + version + ".")

	described, registered := d.describe(projectName)
	if !registered {
		return EngineDescription{}, fmt.Errorf(
			"engine does not report project %s after provisioning", projectName,
		)
	}

	return described, nil
}

// wireModule wires the module working copy into the project via a composer
// path repository: composer resolves the module's dependencies but only ever
// creates a symlink at web/modules/contrib/<module>, and never owns the
// checkout.
func (d *DdevContrib) wireModule(module cockpit.Module, projectPath string) error {
	d.log("Wiring the module working copy via a Composer path repository ...")

	composerPath := filepath.Join(projectPath, "composer.json")
	contents, err := os.ReadFile(composerPath)
	if err != nil {
		return fmt.Errorf("cannot read the project composer.json at %q: %w", composerPath, err)
	}

	wired, err := WithPathRepository(string(contents), "./"+moduleDir)
	if err != nil {
		return err
	}
	// Read-modify-write over a file the environment cannot function without:
	// the rewrite is atomic, so a crash can never leave an unparseable
	// composer.json behind a still-valid completion marker.
	if err := filesystem.Write(composerPath, []byte(wired), filesystem.ModeShared); err != nil {
		return err
	}

	// Pin the exact branch the working copy has checked out: a bare "*@dev"
	// could resolve to a different dev branch published on the Drupal composer
	// endpoint instead of the path repository.
	branch, err := d.git(moduleWorkingCopy(projectPath), "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return err
	}

	return d.requireWorkingCopyBranch(projectPath, module.Name, strings.TrimSpace(branch))
}

// adaptAddOnConfig rewrites the add-on's shipped config for upkeep's
// seeded-tree layout. Re-run after every add-on installation, because an
// installation may clobber the file.
func (d *DdevContrib) adaptAddOnConfig(projectPath string) error {
	configPath := filepath.Join(projectPath, ".ddev", EngineAddOnConfigFilename)

	// One read decides it: the add-on either installed a readable config or it
	// did not, and both spellings of "it did not" are the same problem for the
	// caller — the adaptation cannot proceed.
	contents, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf(
			"cannot read the engine add-on config at %q: the add-on did not install it, "+
				"or it is unreadable: %w",
			configPath, err,
		)
	}

	adapted, err := AdaptContribConfig(string(contents))
	if err != nil {
		return err
	}

	return filesystem.Write(configPath, []byte(adapted), filesystem.ModeShared)
}

// teardownProject disposes of a project per the verified reclamation
// semantics: the engine's own delete first — it removes containers, named
// volumes and per-project images, where a bare tree removal would leave
// containers running — and then the tree.
func (d *DdevContrib) teardownProject(projectName, projectPath string) error {
	d.log(fmt.Sprintf("Deleting engine project %s (containers + volumes) ...", projectName))

	if _, deleted := d.runner.TryRun(
		[]string{"ddev", "delete", "--omit-snapshot", "--yes", projectName}, "", 0,
	); !deleted {
		d.log(
			"Engine delete reported a failure (project may not be registered) — " +
				"continuing with tree removal.",
		)
	}

	if isDirectory(projectPath) {
		if _, err := d.runner.Run([]string{"rm", "-rf", projectPath}, "", 0); err != nil {
			return err
		}
	}
	d.log(fmt.Sprintf("Environment %s torn down.", projectName))

	return nil
}

// describe is the engine's project description, reporting false when the
// project is unknown to it.
func (d *DdevContrib) describe(projectName string) (EngineDescription, bool) {
	output, _ := d.runner.TryRun([]string{"ddev", "describe", projectName, "-j"}, "", 0)

	return DescriptionFromJSON(output)
}

// requireArtifactMeta is the base artifact this environment would be seeded
// from, refusing with the command that builds one when there is none.
func (d *DdevContrib) requireArtifactMeta(coreMajor string) (baseartifact.Meta, error) {
	metaPath, err := d.layout.MetaPath(coreMajor)
	if err != nil {
		return baseartifact.Meta{}, err
	}

	contents, err := os.ReadFile(metaPath)
	if err != nil {
		return baseartifact.Meta{}, fmt.Errorf(
			"no base artifacts for Drupal %s (missing %s). "+
				"Run `upkeep base-artifacts:build --version=%s` first",
			coreMajor, metaPath, coreMajor,
		)
	}

	meta, err := baseartifact.MetaFromYAML(string(contents))
	if err != nil {
		return baseartifact.Meta{}, fmt.Errorf(
			"base artifact meta for Drupal %s is unreadable: %w", coreMajor, err,
		)
	}

	return meta, nil
}

// isDirectory reports whether the path is a directory that exists.
func isDirectory(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}

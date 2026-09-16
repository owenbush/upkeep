package adapter

import (
	"fmt"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/proc"
)

// throwawayTimeout bounds one step of the throwaway install. A resolve and a
// site install are the slow ones and an hour is generous for both.
const throwawayTimeout = time.Hour

// ThrowawaySite is the engine mechanics for the base-artifact build: it brings
// up a throwaway project seeded from a base tree, performs a module-free clean
// install, exports the gzipped database dump, and reports the install
// environment's identity.
//
// Lives here because it is engine knowledge. The base-artifact layer
// orchestrates *what* to build and never names the engine itself, which the
// boundary test enforces over the source.
type ThrowawaySite struct {
	runner proc.Runner
	log    Log
}

// The base-artifact build talks to this through an interface it declares, so
// that package never names the engine. Asserted here, where a drift is a
// compile error in the file that has to change.
var _ baseartifact.InstallSite = (*ThrowawaySite)(nil)

// NewThrowawaySite builds one. A nil log discards progress.
func NewThrowawaySite(runner proc.Runner, log Log) *ThrowawaySite {
	if log == nil {
		log = func(string) {}
	}

	return &ThrowawaySite{runner: runner, log: log}
}

// CleanInstallAndDump seeds a throwaway project from the base tree, installs
// Drupal with the minimal profile, and exports the gzipped dump.
func (s *ThrowawaySite) CleanInstallAndDump(
	coreMajor, treePath, throwawayPath, projectName, dumpPath string,
) (baseartifact.InstallEnvironment, error) {
	// Seeded by tree copy — the path verified byte-identical to a fresh
	// resolve — and never by a second resolve, which could pick up a release
	// made between the two and produce a dump for a tree nobody has.
	s.log("Copying base tree to throwaway install project " + throwawayPath + " ...")
	if _, err := s.runner.Run(
		[]string{"cp", "-a", treePath, throwawayPath}, "", throwawayTimeout,
	); err != nil {
		return baseartifact.InstallEnvironment{}, err
	}

	s.log("Configuring throwaway engine project ...")
	if _, err := s.runner.Run([]string{
		"ddev", "config",
		"--project-type=drupal" + coreMajor,
		"--docroot=web",
		"--project-name=" + projectName,
	}, throwawayPath, throwawayTimeout); err != nil {
		return baseartifact.InstallEnvironment{}, err
	}

	s.log("Starting the throwaway environment ...")
	if _, err := s.runner.Run(
		[]string{"ddev", "start", "-y"}, throwawayPath, throwawayTimeout,
	); err != nil {
		return baseartifact.InstallEnvironment{}, err
	}

	// drush goes into the throwaway copy only; the canonical tree must stay
	// module-free. Run composer inside the container so resolution happens
	// against the same PHP the site will run on.
	s.log("Requiring drush in the throwaway copy (container-side composer) ...")
	if _, err := s.runner.Run([]string{
		"ddev", "composer", "require", "drush/drush", "--no-interaction",
	}, throwawayPath, throwawayTimeout); err != nil {
		return baseartifact.InstallEnvironment{}, err
	}

	s.log("Installing Drupal (minimal profile, module-free) ...")
	if _, err := s.runner.Run([]string{
		"ddev", "drush", "site:install", "minimal", "-y", "--account-pass=admin",
	}, throwawayPath, throwawayTimeout); err != nil {
		return baseartifact.InstallEnvironment{}, err
	}

	phpVersion, err := s.runner.Run(
		[]string{"ddev", "exec", "php", "-r", "echo PHP_VERSION;"}, throwawayPath, throwawayTimeout,
	)
	if err != nil {
		return baseartifact.InstallEnvironment{}, err
	}

	environment := baseartifact.InstallEnvironment{
		PHPVersion: strings.TrimSpace(phpVersion),
		DBEngine:   s.databaseEngine(throwawayPath),
	}
	s.log(fmt.Sprintf(
		"Install environment: PHP %s, DB %s.", environment.PHPVersion, environment.DBEngine,
	))

	s.log("Exporting clean-install DB dump to " + dumpPath + " ...")
	if _, err := s.runner.Run([]string{
		"ddev", "export-db", "--file=" + dumpPath, "--gzip=true",
	}, throwawayPath, throwawayTimeout); err != nil {
		return baseartifact.InstallEnvironment{}, err
	}

	return environment, nil
}

// Teardown disposes of the throwaway project.
//
// The engine's own delete first — it removes containers, every named volume
// and per-project built images — and then the tree. A bare tree removal would
// leave containers running and volumes orphaned.
func (s *ThrowawaySite) Teardown(throwawayPath, projectName string) {
	if !isDirectory(throwawayPath) {
		return
	}

	s.log("Tearing down throwaway install project " + projectName + " ...")
	if _, deleted := s.runner.TryRun(
		[]string{"ddev", "delete", "--omit-snapshot", "--yes", projectName}, "", throwawayTimeout,
	); !deleted {
		// Best effort: the project config may never have been written, if the
		// build failed before it — in which case there is nothing registered
		// to delete and that is not a problem.
		s.log("Engine delete reported a failure (project may never have been registered).")
	}

	if _, err := s.runner.Run(
		[]string{"rm", "-rf", throwawayPath}, "", throwawayTimeout,
	); err != nil {
		// A teardown that cannot remove the tree is worth saying and not worth
		// failing a finished build over: the artifacts are already written,
		// and the scratch directory is named so it can be cleared by hand.
		s.log("The throwaway tree at " + throwawayPath + " could not be removed: " + err.Error())
	}
}

// databaseEngine is what the site was installed against, as "type:version".
//
// Unknown rather than absent when the engine does not say: the meta records it
// for a human reading it later, and "unknown" is the honest answer where a
// blank would read as a field nobody filled in.
func (s *ThrowawaySite) databaseEngine(throwawayPath string) string {
	output, err := s.runner.Run([]string{"ddev", "describe", "-j"}, throwawayPath, throwawayTimeout)
	if err != nil {
		return "unknown"
	}

	described, ok := DescriptionFromJSON(output)
	if !ok {
		return "unknown"
	}

	engine := described.StringOrNull("dbinfo", "database_type")
	if engine == "" {
		return "unknown"
	}

	if version := described.StringOrNull("dbinfo", "database_version"); version != "" {
		return engine + ":" + version
	}

	return engine
}

package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/check"
)

// smokeTimeout bounds the front-page request. It is one HTTP round trip
// against a local container.
const smokeTimeout = 2 * time.Minute

// RunChecks runs the named checks, or the default suite when none are named.
func (d *DdevContrib) RunChecks(environment Environment, checks []check.Type) (check.RunResult, error) {
	if len(checks) == 0 {
		checks = DefaultChecks
	}

	needsToolchain := false
	for _, one := range checks {
		if slices.Contains(ToolchainChecks, one) {
			needsToolchain = true

			break
		}
	}
	if needsToolchain {
		if err := d.ensureCheckToolchain(environment); err != nil {
			return check.RunResult{}, err
		}
	}

	results := make([]check.Result, 0, len(checks))
	for _, one := range checks {
		d.log(fmt.Sprintf("Running check: %s ...", one))
		results = append(results, d.runCheck(environment, one))
	}

	return check.RunResult{Results: results}, nil
}

// runCheck is the one dispatch point for every check type: each arm names both
// how the check is run and, for the command checks, the exact command line.
//
// Exhaustive over the check types with no silent default: an unrecognised one
// is reported as unavailable naming itself, rather than falling through to a
// command that does not exist. Keeping the command lines in the arms is what
// makes that possible without a second, partial match somewhere downstream.
func (d *DdevContrib) runCheck(environment Environment, one check.Type) check.Result {
	switch one {
	case check.Deprecation:
		return d.runDeprecationCheck(environment)

	case check.FunctionalSmoke:
		return d.runSmokeCheck(environment)

	case check.PhpUnit:
		// The engine command as shipped: an existing path argument makes it
		// run exactly that directory. Host-side path; the container's working
		// directory is the project root.
		return d.runCommandCheck(environment, one, []string{
			"ddev", "phpunit", fmt.Sprintf("web/%s/%s", EngineProjectsPath, environment.ModuleName),
		})

	case check.EsLint, check.StyleLint:
		return d.runCommandCheck(environment, one, []string{"ddev", string(one)})

	// Both of these are configured by a file the module may ship, and both
	// discover it from the working directory — so they run against the
	// module's own configuration, as CI does, and only fall back to the
	// gitlab_templates default when the module has none. Which one applies is
	// decided here rather than in the shell: `ddev exec` joins its arguments
	// into one line, so a script with control flow does not survive the trip.
	case check.PhpCs:
		return d.runCommandCheck(environment, one, []string{
			"ddev", "exec", "bash", "-c", PhpcsScript(
				containerModulePath(environment),
				d.ownConfig(environment, PhpcsConfigs, "PHPCS ruleset"),
			),
		})

	case check.PhpStan:
		return d.runCommandCheck(environment, one, []string{
			"ddev", "exec", "bash", "-c", PhpstanScript(
				containerModulePath(environment),
				d.ownConfig(environment, PhpstanConfigs, "PHPStan configuration"),
			),
		})

	case check.ModuleInstall:
		return d.runCommandCheck(environment, one, []string{
			"ddev", "drush", "pm:install", environment.ModuleName, "-y",
		})

	default:
		// Recorded explicitly rather than omitted. A check type this engine
		// has no arm for is a gap in the adapter, and a run that silently
		// skipped it would report a green suite that never ran it.
		return check.NotAvailable(one, fmt.Sprintf(
			"The engine adapter has no way to run the %q check; it was not run.", one,
		))
	}
}

// ownConfig is the configuration file the module ships for a check, if it
// ships one.
//
// Probed one candidate at a time rather than decided in the shell. The
// decision needs a *name* — it is passed to the tool afterwards — and neither
// an `if` nor a variable to hold it survives `ddev exec`, which expands the
// command string before the container's shell runs it.
//
// names are in the tool's own precedence order.
func (d *DdevContrib) ownConfig(environment Environment, names []string, what string) string {
	for _, name := range names {
		_, found := d.runner.TryRun([]string{
			"ddev", "exec", "bash", "-c", ConfigProbe(containerModulePath(environment), name),
		}, environment.ProjectPath, 0)

		if found {
			d.log(fmt.Sprintf("Using the module's own %s (%s), as CI does.", what, name))

			return name
		}
	}

	d.log(fmt.Sprintf("Module ships no %s; using the gitlab_templates default.", what))

	return ""
}

// containerModulePath is the in-container path of the module under
// maintenance, for the phpcs and phpstan invocations.
//
// Checks target exactly that module — never all of DRUPAL_PROJECTS_PATH, where
// composer also materialises the module's real dependencies, whose packaged
// code must not pollute results. $DDEV_DOCROOT and $DRUPAL_PROJECTS_PATH
// expand inside the web container. The module name is quoted rather than
// trusted: it is spliced into a shell script body, so its safety must not
// depend on a validator two packages away.
func containerModulePath(environment Environment) string {
	return `"$DDEV_DOCROOT/$DRUPAL_PROJECTS_PATH"/` + QuoteShellArgument(environment.ModuleName)
}

// runCommandCheck is a check that is just a command line run inside the
// environment: the outcome is data, so a non-zero exit becomes a failed result
// rather than an error, and a timeout becomes a timed-out one.
func (d *DdevContrib) runCommandCheck(
	environment Environment,
	one check.Type,
	command []string,
) check.Result {
	process := d.runner.Capture(command, environment.ProjectPath, checkTimeout)
	if process.TimedOut {
		return check.TimedOut(one, process.Output, process.Duration, checkTimeout)
	}

	return check.FromProcess(one, process.ExitCode, process.Output, process.Duration)
}

// runSmokeCheck requests the environment's primary URL with the module
// enabled, and passes only on HTTP 200.
func (d *DdevContrib) runSmokeCheck(environment Environment) check.Result {
	process := d.runner.Capture(
		[]string{"curl", "-ksS", "-o", "/dev/null", "-w", "%{http_code}", environment.PrimaryURL},
		environment.ProjectPath,
		smokeTimeout,
	)

	if process.TimedOut {
		return check.TimedOut(check.FunctionalSmoke, process.Output, process.Duration, smokeTimeout)
	}
	if process.ExitCode == nil || *process.ExitCode != 0 {
		return check.Result{
			Type:     check.FunctionalSmoke,
			Status:   check.Failed,
			ExitCode: process.ExitCode,
			Output:   "Request failed: " + process.Output,
			Duration: process.Duration,
		}
	}

	httpCode := strings.TrimSpace(process.Output)
	status := check.Failed
	if httpCode == "200" {
		status = check.Passed
	}
	ok := 0

	return check.Result{
		Type:     check.FunctionalSmoke,
		Status:   status,
		ExitCode: &ok,
		Output:   fmt.Sprintf("GET %s -> HTTP %s", environment.PrimaryURL, httpCode),
		Duration: process.Duration,
	}
}

// runDeprecationCheck runs the upgrade-status report, but only when the pinned
// engine provides a command for it.
//
// Otherwise an explicit unavailable result — never a silent omission.
// ddev-drupal-contrib 1.1.5 ships no such command.
func (d *DdevContrib) runDeprecationCheck(environment Environment) check.Result {
	commandPath := filepath.Join(environment.ProjectPath, ".ddev", "commands", "web", "upgrade-status")
	if !isRegularFile(commandPath) {
		return check.NotAvailable(check.Deprecation, fmt.Sprintf(
			"The pinned engine add-on (%s %s) provides no deprecation/upgrade-status command for Drupal %s.",
			EngineAddOnName, EngineAddOnVersion, environment.CoreMajor,
		))
	}

	process := d.runner.Capture([]string{"ddev", "upgrade-status"}, environment.ProjectPath, checkTimeout)
	if process.TimedOut {
		return check.TimedOut(check.Deprecation, process.Output, process.Duration, checkTimeout)
	}

	return check.FromProcess(check.Deprecation, process.ExitCode, process.Output, process.Duration)
}

// ensureCheckToolchain makes sure the check toolchain is present, probing for
// the binaries in vendor/bin.
func (d *DdevContrib) ensureCheckToolchain(environment Environment) error {
	allPresent := true
	for _, binary := range []string{"phpunit", "phpstan", "phpcs"} {
		if !isRegularFile(filepath.Join(environment.ProjectPath, "vendor", "bin", binary)) {
			allPresent = false

			break
		}
	}

	if allPresent {
		// The toolchain is here, but the *module's* dev dependencies may not
		// be: an environment provisioned before it declared one, or before
		// upkeep installed them at all, has the binaries and not the packages.
		// Falling out here is what made the first version of this fix do
		// nothing on every existing environment — which is every environment
		// after the first.
		d.installModuleDevRequirements(environment)

		return nil
	}

	d.log("Provisioning the check toolchain (core-dev, coder, phpstan-drupal) ...")

	if _, err := d.runner.Run([]string{
		"ddev", "composer", "config", "--no-plugins",
		"allow-plugins.dealerdirect/phpcodesniffer-composer-installer", "true",
	}, environment.ProjectPath, 0); err != nil {
		return err
	}

	artifactMeta, err := d.requireArtifactMeta(environment.CoreMajor)
	if err != nil {
		return err
	}

	command := []string{
		"ddev", "composer", "require", "--dev", "--with-all-dependencies", "--no-interaction",
	}
	for _, pkg := range ToolchainPackages {
		command = append(command, baseartifact.PackageFor(pkg, environment.CoreMajor, artifactMeta.CoreVersion))
	}
	if _, err := d.runner.Run(command, environment.ProjectPath, 0); err != nil {
		return err
	}

	d.installModuleDevRequirements(environment)

	return nil
}

// installModuleDevRequirements installs the module's own dev dependencies into
// the site.
//
// Honouring a module's phpcs and phpstan configuration means honouring what
// that configuration references, and those are the *module's* packages:
// field_visibility_conditions' ruleset points at
// ./vendor/phpcompatibility/php-compatibility/…, which its composer.json
// requires and the site does not. CI has them because `composer install` runs
// in the module repository; here the module is a path repository, and composer
// never installs a path dependency's require-dev.
//
// Failure is a warning, not a refusal. A version conflict here would otherwise
// take down phpunit, the install check and the smoke test over a linting
// dependency — and phpcs names the missing sniff itself, clearly, if it comes
// to that.
func (d *DdevContrib) installModuleDevRequirements(environment Environment) {
	packages := notInstalled(
		environment.ProjectPath,
		ModuleDevRequirements(moduleWorkingCopy(environment.ProjectPath)),
	)
	if len(packages) == 0 {
		return
	}

	d.log(fmt.Sprintf(
		"Installing the module's own dev dependencies (%s), which its phpcs and phpstan "+
			"configuration may reference ...",
		strings.Join(packages, ", "),
	))

	command := append([]string{
		"ddev", "composer", "require", "--dev", "--with-all-dependencies", "--no-interaction",
	}, packages...)

	if _, installed := d.runner.TryRun(command, environment.ProjectPath, 0); !installed {
		d.log(DevRequirementsUnavailable(packages))
	}
}

// notInstalled is which of the module's dev dependencies are not already in
// the site's vendor tree.
//
// A directory test on the host, so a reused environment costs nothing to
// re-verify and a composer require only happens when something is genuinely
// absent.
func notInstalled(projectPath string, packages []string) []string {
	missing := []string{}
	for _, pkg := range packages {
		info, err := os.Stat(filepath.Join(projectPath, "vendor", filepath.FromSlash(pkg)))
		if err != nil || !info.IsDir() {
			missing = append(missing, pkg)
		}
	}

	return missing
}

// Serve makes sure the module is installed and returns where to open it.
func (d *DdevContrib) Serve(environment Environment) (ServeResult, error) {
	d.log(fmt.Sprintf("Ensuring module %s is installed ...", environment.ModuleName))

	if _, err := d.runner.Run(
		[]string{"ddev", "drush", "pm:install", environment.ModuleName, "-y"}, environment.ProjectPath, 0,
	); err != nil {
		return ServeResult{}, err
	}

	login, minted := d.runner.TryRun(
		[]string{"ddev", "drush", "uli", "--uri=" + environment.PrimaryURL}, environment.ProjectPath, 0,
	)

	result := ServeResult{URL: environment.PrimaryURL}
	if minted {
		result.LoginURL = strings.TrimSpace(login)
	}

	return result, nil
}

// LoadFixture restores a named fixture into the environment's database.
func (d *DdevContrib) LoadFixture(environment Environment, fixtureName string) error {
	if err := d.ensureFixtureAddOn(environment.ProjectPath); err != nil {
		return err
	}

	d.log(fmt.Sprintf("Loading fixture %q via the engine fixture command ...", fixtureName))

	// The add-on command owns all fixture resolution and load semantics —
	// module scope, the shared library, the snapshot fast path. The adapter
	// only invokes it.
	_, err := d.runner.Run(
		[]string{"ddev", FixtureLoadCommand, fixtureName}, environment.ProjectPath, 0,
	)

	return err
}

// ResolveEnvPath is the project path for a (module, core) pair, or "" when
// there is no completed environment for it.
func (d *DdevContrib) ResolveEnvPath(moduleName, coreMajor string) string {
	projectName, err := EngineProjectName(moduleName, coreMajor)
	if err != nil {
		return ""
	}

	projectPath := d.projectPath(projectName)
	info, err := os.Stat(projectPath)
	if err != nil || !info.IsDir() {
		return ""
	}
	// The meta is the provisioning completion marker, so a directory without
	// one is a partial provision and not an environment.
	if !isRegularFile(EnvMetaPath(projectPath)) {
		return ""
	}

	return projectPath
}

// InspectWorkingCopy reads the git state of the module working copy, or
// reports false when there is no environment to read.
func (d *DdevContrib) InspectWorkingCopy(moduleName, coreMajor string) (WorkingCopyStatus, bool) {
	projectPath := d.ResolveEnvPath(moduleName, coreMajor)
	if projectPath == "" {
		return WorkingCopyStatus{}, false
	}

	return InspectWorkingCopy(moduleWorkingCopy(projectPath), d.runner), true
}

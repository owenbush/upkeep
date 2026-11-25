package adapter

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/proc"
)

// UpgradeStatusPackage is the deprecation check's analyser: the tool Drupal
// itself points maintainers at for next-major readiness.
//
// Installed by upkeep on first use rather than with the toolchain, because the
// pinned engine add-on ships no command for it and most check runs never ask
// for it. Unconstrained, so composer picks the release that supports the
// installed core: 5.x for 10.4 and later, 4.x before that.
const UpgradeStatusPackage = "drupal/upgrade_status"

// upgradeStatusModule is the extension upgrade_status's drush commands live
// in. It has to be enabled for them to exist.
const upgradeStatusModule = "upgrade_status"

// runDeprecationCheck reports what the module must change before the next
// major core.
//
// The engine's own command wins when the add-on provides one; ddev-drupal-
// contrib 1.1.5 does not, so upkeep installs upgrade_status itself and runs
// its analysis. Its exit code is the verdict: 3 when there are messages for
// the next major (or the analysis failed), 0 otherwise — messages that only
// matter for a later major do not fail it.
//
// Static analysis, like everything else upgrade_status does: a removal that
// only shows up when the code runs is PHPUnit's to find, not this check's.
func (d *DdevContrib) runDeprecationCheck(environment Environment) check.Result {
	command := []string{"ddev", "upgrade-status"}
	if !isRegularFile(filepath.Join(environment.ProjectPath, ".ddev", "commands", "web", "upgrade-status")) {
		if reason := d.ensureUpgradeStatus(environment); reason != "" {
			return check.NotAvailable(check.Deprecation, reason)
		}
		command = []string{
			"ddev", "drush",
			UpgradeStatusAnalyzeCommand(lockedVersions(environment.ProjectPath)[UpgradeStatusPackage]),
			environment.ModuleName,
		}
	}

	process := d.runner.Capture(command, environment.ProjectPath, checkTimeout)
	if process.TimedOut {
		return check.TimedOut(check.Deprecation, process.Output, process.Duration, checkTimeout)
	}

	return check.FromProcess(check.Deprecation, process.ExitCode, process.Output, process.Duration)
}

// ensureUpgradeStatus installs and enables upgrade_status, and enables the
// module under maintenance. It returns why the check cannot run, or "".
//
// Unavailable rather than failed when upgrade_status cannot be had: a core
// with no compatible release yet is upgrade_status's gap, not the module's.
//
// The module is enabled best-effort. upgrade_status checks library use only
// for an installed extension and says so as a finding; a module that will not
// install is module_install's failure to report, and the analysis that follows
// says what it could not check rather than hiding it.
func (d *DdevContrib) ensureUpgradeStatus(environment Environment) string {
	if _, installed := lockedVersions(environment.ProjectPath)[UpgradeStatusPackage]; !installed {
		d.log("Installing " + UpgradeStatusPackage + " for the deprecation check ...")
		require := d.runner.Capture([]string{
			"ddev", "composer", "require", "--dev", "--no-interaction", UpgradeStatusPackage,
		}, environment.ProjectPath, proc.DefaultTimeout)
		if !succeeded(require) {
			return fmt.Sprintf(
				"%s could not be installed for Drupal %s, so the deprecation check did not run.\n%s",
				UpgradeStatusPackage, environment.CoreMajor, require.Output,
			)
		}
	}

	enable := d.runner.Capture(
		[]string{"ddev", "drush", "pm:install", "-y", upgradeStatusModule},
		environment.ProjectPath, checkTimeout,
	)
	if !succeeded(enable) {
		return fmt.Sprintf(
			"The %s module could not be enabled, so the deprecation check did not run.\n%s",
			upgradeStatusModule, enable.Output,
		)
	}

	if _, ok := d.runner.TryRun(
		[]string{"ddev", "drush", "pm:install", "-y", environment.ModuleName},
		environment.ProjectPath, checkTimeout,
	); !ok {
		d.log(fmt.Sprintf(
			"Warning: %s could not be enabled; upgrade_status cannot check its library use.",
			environment.ModuleName,
		))
	}

	return ""
}

// UpgradeStatusAnalyzeCommand is the analyse command for an installed
// upgrade_status version.
//
// 5.x renamed it to upgrade-status:analyze and deprecated the old name, to be
// removed in 6.x; 4.x has only the old one. A version that cannot be read —
// a branch alias, or no lock — gets the current name, since an unconstrained
// require installs the newest release.
func UpgradeStatusAnalyzeCommand(version string) string {
	digits := strings.TrimPrefix(strings.TrimPrefix(version, "dev-"), "v")
	if end := strings.IndexAny(digits, ".-"); end >= 0 {
		digits = digits[:end]
	}
	if major, err := strconv.Atoi(digits); err == nil && major < 5 {
		return "upgrade_status:analyze"
	}

	return "upgrade-status:analyze"
}

// succeeded is a captured process that finished with exit 0.
func succeeded(process proc.Captured) bool {
	return !process.TimedOut && process.ExitCode != nil && *process.ExitCode == 0
}

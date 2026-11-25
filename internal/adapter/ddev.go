package adapter

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/proc"
)

// The ddev + ddev-drupal-contrib engine.
//
// Environment layout under the projects root, one directory per
// (module x core-major) pair:
//
//	<projects-root>/upkeep-<module>-d<major>/
//	    (seeded base tree: composer.json, web/, vendor/, ...)
//	    .ddev/            engine project config + pinned add-on
//	    module/           git working copy of the module (adapter/git-owned;
//	                      nothing else ever writes in it)
//	    web/modules/contrib/<module>  -> symlink into module/, made by the
//	                      adapter; the module is never installed as a package
//	    .upkeep-env.yml   the provisioning completion marker, and the identity
//	                      the reuse decision is made against
const (
	// moduleDir is the git working copy inside a project.
	moduleDir = "module"

	// checkTimeout is a generous per-check timebox; a timeout is a failure
	// with a reason rather than a hang.
	checkTimeout = 30 * time.Minute

	// startTimeout is how long starting an *existing* environment may take.
	//
	// Not the one-hour default: that is for resolves and installs, and applied
	// here it turned a wedged start into an hour of silence.
	startTimeout = 10 * time.Minute

	// gitTimeout bounds a local git call. Fetches go through the longer
	// default; these are local and a minute is generous.
	gitTimeout = time.Minute
)

// DefaultChecks is the suite a run executes when no checks are named: the
// engine's static and test checks, then install, smoke and deprecation.
var DefaultChecks = []check.Type{
	check.PhpUnit,
	check.PhpStan,
	check.PhpCs,
	check.EsLint,
	check.StyleLint,
	check.CSpell,
	check.ModuleInstall,
	check.FunctionalSmoke,
	check.Deprecation,
}

// ToolchainChecks need the dev toolchain — the phpunit, phpstan and phpcs
// binaries — present in vendor/.
var ToolchainChecks = []check.Type{check.PhpUnit, check.PhpStan, check.PhpCs}

// ToolchainPackages is what check provisioning installs container-side, with
// --dev, alongside drupal/core-dev pinned to the installed core
// (CoreDevRequirement): the same toolchain drupal.org's GitLab CI uses.
//
// phpstan-drupal, extension-installer and deprecation-rules make the
// gitlab_templates phpstan.neon work as it does in CI.
//
// **drupal/coder is deliberately not here.** core-dev requires it, and the
// version core-dev asks for is the version CI runs. Required bare, composer
// writes the newest coder's own constraint (^9.0), and since only core-dev
// 11.5+ accepts coder 9, a resolve with --with-all-dependencies and the
// environment's pre-release stability moved core-dev to 11.5.0-beta1 under a
// stable 11.4.11 core. phpcs then ran coder 9 while CI ran coder 8.3 —
// reported from liquid_voting, where CI's five ArrayIndentation errors came
// back as zero locally (#74).
var ToolchainPackages = []string{
	"mglaman/phpstan-drupal",
	"phpstan/extension-installer",
	"phpstan/phpstan-deprecation-rules",
}

// CoreDevRequirement is drupal/core-dev pinned to exactly the installed core
// version.
//
// core-dev is tagged in lockstep with core, and each tag pins the standards CI
// installs for that release: 11.4.x requires coder ^8.3.30, 11.5.x coder ^9.
// A caret constraint lets composer pick a core-dev for a different minor — or
// a pre-release, in an environment that accepts them — and with it a different
// coding standard. An exact version carries its own stability, so a
// pre-release core (12.0.0-alpha1) needs no @alpha flag, and a dev branch
// (12.x-dev) is pinned the same way.
func CoreDevRequirement(coreVersion string) string {
	return "drupal/core-dev:" + coreVersion
}

// Log receives an operator-facing progress line.
type Log func(string)

// DdevContrib is the engine implementation.
type DdevContrib struct {
	layout       *baseartifact.Layout
	projectsRoot string
	runner       proc.Runner
	log          Log
}

// NewDdevContrib builds the engine. A nil log discards progress.
func NewDdevContrib(
	layout *baseartifact.Layout,
	projectsRoot string,
	runner proc.Runner,
	log Log,
) *DdevContrib {
	if log == nil {
		log = func(string) {}
	}

	return &DdevContrib{layout: layout, projectsRoot: projectsRoot, runner: runner, log: log}
}

// projectPath is where an environment lives on disk.
func (d *DdevContrib) projectPath(projectName string) string {
	return filepath.Join(strings.TrimRight(d.projectsRoot, "/"), projectName)
}

// moduleWorkingCopy is the git checkout inside an environment.
func moduleWorkingCopy(projectPath string) string {
	return filepath.Join(projectPath, moduleDir)
}

// git runs a git command in the module working copy and returns its stdout.
func (d *DdevContrib) git(dir string, args ...string) (string, error) {
	return d.runner.Run(append([]string{"git", "-C", dir}, args...), "", gitTimeout)
}

// gitProbe runs a git command whose failure is an answer rather than a
// problem.
func (d *DdevContrib) gitProbe(dir string, args ...string) (string, bool) {
	out, ok := d.runner.TryRun(append([]string{"git", "-C", dir}, args...), "", gitTimeout)

	return strings.TrimSpace(out), ok
}

// gitCapture runs a git command and reports the whole outcome, status
// included, without treating any of it as an error.
func (d *DdevContrib) gitCapture(dir string, args ...string) proc.Captured {
	return d.runner.Capture(append([]string{"git", "-C", dir}, args...), "", gitTimeout)
}

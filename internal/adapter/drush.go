package adapter

import (
	"fmt"
	"strings"

	"github.com/owenbush/upkeep/internal/baseartifact"
)

// Everything upkeep knows about the site-management CLI its engine drives,
// which is drush — including what the flag that overrides it is *called*.
//
// The name is here rather than in the command for the ordinary reason: it is
// engine knowledge, and a command that spelled it would be the orchestrator
// reaching past the boundary. So `base-artifacts:build` declares a flag whose
// name it asks for, and genuinely does not know which tool it selects. An
// engine driving something other than drush answers differently here and no
// command changes.

// DrushPackage is the package, named once so a message and a command cannot
// spell it differently.
const DrushPackage = "drush/drush"

// ToolRequireFlag is the flag that overrides how the toolchain is installed.
const ToolRequireFlag = "drush"

// ToolRequireFlagHelp is what `--help` says about it.
const ToolRequireFlagHelp = "Composer constraint for " + DrushPackage +
	", for a core whose dependencies no released version satisfies yet " +
	"(e.g. \"^13@dev\"). Recorded in the artifact meta and reused by every " +
	"environment seeded from this set"

// ToolRequire is the composer argument that installs the toolchain.
//
// Bare when nothing was asked for, which is right for every released core: a
// plain `drush/drush` resolves to the newest release that fits the tree.
func ToolRequire(constraint string) string {
	if strings.TrimSpace(constraint) == "" {
		return DrushPackage
	}

	return DrushPackage + ":" + strings.TrimSpace(constraint)
}

// ToolRequireHint is what to add when installing the toolchain fails, and it
// is only ever about a pre-release core.
//
// A Drupal major in alpha or beta can require a dependency no drush *release*
// has caught up with. Measured: core 12.0.0-beta1 requires
// guzzlehttp/guzzle ^8.0.1, the newest drush release (13.8.0) requires ^7.0,
// and none of drush's tagged releases supports guzzle 8 — so composer's own
// suggestion, `--with-all-dependencies`, cannot help, because no guzzle
// satisfies both sides. A drush development branch can: 13.x-dev accepts
// `^7.0 || ^8.0`.
//
// Naming the branch is left to the operator rather than substituted quietly.
// A base artifact set is the one place a silent substitution is least
// acceptable — every later verdict is a statement about this tree — which is
// the same reason `--stability` is a flag and not a fallback. An unpinned
// development branch is also a thing that can break overnight, and a build
// that chose one without being asked would be the tool making that trade on
// somebody's behalf.
//
// Empty for a released core, and empty once a constraint was given: in both
// cases the toolchain is not the obvious suspect, and advice already taken
// buries whatever composer actually said. The same rule as UnresolvableHint.
func ToolRequireHint(coreVersion, constraint string) string {
	if constraint != "" || baseartifact.StabilityOf(coreVersion) == "" {
		return ""
	}

	return fmt.Sprintf(
		"\n%s has no release that resolves against core %s. A Drupal major in alpha or beta can "+
			"require a dependency no release of it supports yet, and composer's "+
			"--with-all-dependencies cannot bridge that — no version satisfies both sides.\n"+
			"A development branch usually can. To build against one, naming the branch yourself:\n"+
			"  upkeep base-artifacts:build --version=%s --stability=%s --%s='^13@dev'",
		DrushPackage, coreVersion,
		baseartifact.MajorOf(coreVersion), baseartifact.StabilityOf(coreVersion), ToolRequireFlag,
	)
}

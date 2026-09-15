package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/owenbush/upkeep/internal/filesystem"
)

// requireWorkingCopyBranch pins the composer requirement to the branch the
// working copy has checked out, resolving through the path repository.
//
// Every branch switch in the working copy must be followed by this sync: a
// stale pin — "1.0.x-dev" while the checkout is mr-2 — makes every later
// composer resolution in the project unsatisfiable. The partial update also
// materialises dependencies the checked-out branch newly requires in the
// module's own composer.json.
func (d *DdevContrib) requireWorkingCopyBranch(projectPath, moduleName, branch string) error {
	if _, err := d.runner.Run([]string{
		"ddev", "composer", "require",
		fmt.Sprintf("drupal/%s:%s", moduleName, DevConstraintForBranch(branch)),
		"--no-interaction",
	}, projectPath, 0); err != nil {
		return err
	}

	// Composer must symlink to the working copy and never mirror it: a mirror
	// is a copy git does not own, so the next apply would mutate one tree and
	// the checks would run against another.
	installed := filepath.Join(projectPath, "web", "modules", "contrib", moduleName)
	info, err := os.Lstat(installed)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf(
			"module wiring violated the ownership constraint: %q is not a symlink into the working copy "+
				"(composer mirrored the package instead)",
			installed,
		)
	}

	return nil
}

// ensureFixtureAddOn installs the fixture add-on when the environment lacks
// it.
//
// Called during provisioning — new environments get it from birth — and
// lazily on first fixture load, so environments provisioned before the add-on
// became part of the layout get it on first use without re-provisioning.
func (d *DdevContrib) ensureFixtureAddOn(projectPath string) error {
	stampPath := filepath.Join(projectPath, ".ddev", FixtureAddOnStamp)
	expected := FixtureAddOnExpectedStamp()

	// Both, not either. The marker says the add-on's commands are there; the
	// stamp says they are the ones this upkeep expects. Probing only the marker
	// was fine while nothing was published — there was no other version to be
	// holding — and would now let an environment keep an old release for ever,
	// since a command file's presence says nothing about which release wrote
	// it.
	if isRegularFile(filepath.Join(projectPath, ".ddev", FixtureAddOnMarker)) {
		if stamped, err := os.ReadFile(stampPath); err == nil &&
			strings.TrimSpace(string(stamped)) == expected {
			return nil
		}
	}

	source := FixtureAddOnName + " " + FixtureAddOnVersion
	if FixtureAddOnIsOverridden() {
		source = FixtureAddOnSource()
	}
	d.log("Installing fixture add-on " + source + " ...")

	if _, err := d.runner.Run(
		append([]string{"ddev", "add-on", "get"}, FixtureAddOnInstallArguments()...), projectPath, 0,
	); err != nil {
		return err
	}

	// Written after the install, never before: a stamp that outlived a failed
	// install would make the next run skip the retry.
	//
	// The directory is the add-on's own, so it normally exists by now — but
	// only because the install just put a file in it, which is a fact about
	// the add-on's contents rather than about this code. Created rather than
	// assumed; a directory that cannot be made is reported as the write
	// failing, naming the path.
	if err := filesystem.EnsureDirectory(filepath.Dir(stampPath), filesystem.ModeSharedDir); err != nil {
		return err
	}

	return filesystem.Write(stampPath, []byte(expected+"\n"), filesystem.ModeShared)
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

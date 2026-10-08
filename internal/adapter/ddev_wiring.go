package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/owenbush/upkeep/internal/filesystem"
)

// moduleSymlink is where the add-on's checks look for the module: the
// adaptation repoints DRUPAL_PROJECTS_PATH at modules/contrib, and the
// commands target <PROJECTS_PATH>/<module> beneath it.
func moduleSymlink(projectPath, moduleName string) string {
	return filepath.Join(projectPath, "web", EngineProjectsPath, moduleName)
}

// linkWorkingCopy puts the module where Drupal and the add-on look for it.
//
// A symlink we make, rather than one composer makes as a side effect of
// installing the module as a package. Drupal discovers modules by scanning the
// filesystem and the add-on's checks target a path, so neither needs the
// module in composer.lock — and requiring it there cost more than it bought:
//
//   - **It made the environment refuse the work it exists for.** A module
//     declares the cores it *supports*, so installing it as a package
//     enforced that constraint against the seeded core, and an environment
//     for the core whose support you are adding could not be built at all.
//   - **It put a composer resolve on every branch switch.** The requirement
//     pinned <branch>-dev, so applying a merge request, applying a patch,
//     starting work and plain checkout each had to re-pin, each of which could
//     fail on its own.
//
// Relative, so the project tree stays relocatable: an absolute link would
// break the moment the projects root moved, which is a thing `--projects-root`
// invites.
//
// Replaced rather than reused when something is already there. The one thing
// that must never happen is a real directory at this path — that is a copy git
// does not own, so an apply would mutate one tree while the checks read
// another — and a stale link from an earlier layout is no reason to refuse.
func (d *DdevContrib) linkWorkingCopy(projectPath, moduleName string) error {
	link := moduleSymlink(projectPath, moduleName)
	if err := filesystem.EnsureDirectory(filepath.Dir(link), filesystem.ModeSharedDir); err != nil {
		return err
	}

	if info, err := os.Lstat(link); err == nil {
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf(
				"refusing to wire the module: %q is a real directory, not a link into the working "+
					"copy. Something copied the module there, and checks would read that copy while "+
					"git changed the other. Remove it, or re-provision the environment",
				link,
			)
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("cannot replace the module link at %q: %w", link, err)
		}
	}

	// ../../../module — out of modules/contrib, out of web, to the project
	// root the working copy sits in.
	target := filepath.Join("..", "..", "..", moduleDir)
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("cannot link the module working copy into %q: %w", link, err)
	}

	return nil
}

// syncModuleDependencies installs what the checked-out branch of the module
// says it needs.
//
// Every branch switch in the working copy must be followed by this: a merge
// request or a patch can add a dependency, and the checks would then fail on a
// missing class rather than on anything the contribution got wrong. It is the
// half of the old composer pin that was worth keeping — the pin itself only
// existed to make the module resolvable as a package.
//
// drupal/core is not among them (ModuleRequirements drops it), so a module
// that does not yet declare the seeded core still wires.
//
// Nothing to install is the common case — most modules require only core —
// and composer is not run for an empty list, which would add a container round
// trip to every branch switch. The link is still checked either way.
func (d *DdevContrib) syncModuleDependencies(projectPath, moduleName string) error {
	packages := ModuleRequirements(moduleWorkingCopy(projectPath))
	if len(packages) > 0 {
		d.log("Installing the module's own dependencies (" + strings.Join(packages, ", ") + ") ...")
		command := append([]string{"ddev", "composer", "require"}, packages...)
		if _, err := d.runner.Run(append(command, "--no-interaction"), projectPath, 0); err != nil {
			return err
		}
	}

	// Checked whether or not anything was installed. The link is what every
	// check reads the module through, and the cost of looking is one Lstat
	// against a container round trip saved — so there is no reason to make the
	// guarantee conditional on there having been work to do.
	return d.assertModuleIsLinked(projectPath, moduleName)
}

// assertModuleIsLinked holds the ownership constraint after anything composer
// did.
//
// composer installs the module's dependencies into the same modules/contrib
// directory the module is linked into, and `composer/installers` is perfectly
// capable of writing over a path it thinks it owns. A real directory there is
// a copy git does not own: the next apply would mutate the working copy while
// the checks read the copy, and the two would disagree silently.
func (d *DdevContrib) assertModuleIsLinked(projectPath, moduleName string) error {
	link := moduleSymlink(projectPath, moduleName)
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf(
			"module wiring violated the ownership constraint: %q is not a symlink into the working copy",
			link,
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

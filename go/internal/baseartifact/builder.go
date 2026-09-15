package baseartifact

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/filesystem"
	"github.com/owenbush/upkeep/internal/proc"
)

const (
	// buildTimeout bounds one build step. A full resolve and a site install
	// are the slow ones; an hour is generous for both and still fails long
	// before anyone gives up.
	buildTimeout = time.Hour

	// stagingPrefix is where a build in progress lives: a sibling of the live
	// version directory, inside the base-artifacts directory so the finished
	// set moves into place with a rename on the same filesystem rather than a
	// copy.
	//
	// What keeps a build in progress out of the status listing, out of prune,
	// and out of the core inference that gives an unregistered module its
	// versions is the *shape* of the name: VersionsOnDisk admits only whole
	// numbers, and this is not one. A half-built tree must never read as a
	// core somebody can be offered, and that holds with or without the dot —
	// the PHP's comment credits the dot for it, which mutation testing showed
	// is not where the property comes from.
	//
	// The dot is still worth having, for the ordinary reason: it keeps a
	// multi-gigabyte directory out of an operator's `ls` and out of any glob
	// that does not ask for dotfiles.
	stagingPrefix = ".building-d"

	// retiredDir is where the outgoing set waits while the incoming one is
	// moved in.
	retiredDir = "retired"
)

// InstallSite is the engine mechanics a build needs: a throwaway site brought
// up from a base tree, clean-installed, and dumped.
//
// An interface rather than the concrete type because the adapter already
// imports this package — for the artifact layout and the toolchain
// constraints — and Go will not take the cycle. Which is the right way round
// anyway: this package decides *what* to build and must not name the engine,
// and an interface here is the seam the boundary check is about.
type InstallSite interface {
	// CleanInstallAndDump installs Drupal into a throwaway copy of the base
	// tree and writes the gzipped dump, reporting what it installed on.
	CleanInstallAndDump(
		coreMajor, treePath, throwawayPath, projectName, dumpPath string,
	) (InstallEnvironment, error)

	// Teardown disposes of the throwaway project. It reports nothing: a
	// finished build must not fail over cleaning up after itself.
	Teardown(throwawayPath, projectName string)
}

// InstallEnvironment is what a clean install ran on, recorded in the meta so a
// later reader knows what the dump was made against.
type InstallEnvironment struct {
	PHPVersion string
	DBEngine   string
}

// Builder builds the two canonical per-core artifacts: a resolved, module-free
// base tree and a gzipped clean-install SQL dump, under
// <cockpit>/base-artifacts/<major>/ with a meta sidecar and a canonical marker
// so prune never touches them.
//
// The seeding mechanism was verified rather than assumed: copying a pristine
// resolved base tree and layering `composer require` on top is byte-identical
// to a from-scratch resolve. The artifact built here *is* that pristine tree,
// so it is produced by a full create-project riding the shared composer cache,
// and every downstream environment then seeds by tree copy. The throwaway
// install used for the dump is seeded from this tree the same way, never by a
// second resolve, and drush goes only into the throwaway so the canonical tree
// stays module-free.
type Builder struct {
	layout     *Layout
	site       InstallSite
	scratchDir string
	runner     proc.Runner
	log        func(string)
}

// NewBuilder builds a builder. A nil log discards progress.
func NewBuilder(
	layout *Layout,
	site InstallSite,
	scratchDir string,
	runner proc.Runner,
	log func(string),
) *Builder {
	if log == nil {
		log = func(string) {}
	}

	return &Builder{layout: layout, site: site, scratchDir: scratchDir, runner: runner, log: log}
}

// Build produces the artifact set for one core major.
//
// A rebuild is staged beside the live set and never over it. It used to remove
// the existing set first and resolve into the empty directory, so a resolve
// that failed — a network blip, a constraint that no longer resolves — left
// the core with no artifact set at all and every environment for it unusable.
// The expensive, failure-prone part now happens beside the live set, and only
// a rename touches it.
func (b *Builder) Build(coreMajor string, force bool, stability string) (Meta, error) {
	if err := AssertStability(stability); err != nil {
		return Meta{}, err
	}

	// Validated here, at the first opportunity, so an impossible core is
	// refused before the log says anything about building one. The staging
	// layout would refuse it a moment later regardless — VersionDir is the one
	// validation point — so this is about which message the operator gets.
	versionDir, err := b.layout.VersionDir(coreMajor)
	if err != nil {
		return Meta{}, err
	}
	existing := isDirectory(versionDir)

	if existing && !force {
		return Meta{}, fmt.Errorf(
			"base artifacts for core %s already exist at %s. "+
				"Re-run with --force to rebuild deliberately",
			coreMajor, versionDir,
		)
	}

	b.removeStaleStaging(coreMajor)

	stagingRoot, staged, err := b.makeStaging(coreMajor)
	if err != nil {
		return Meta{}, err
	}

	meta, err := b.resolveAndInstall(coreMajor, staged, stability)
	if err != nil {
		// Never leave a partial artifact set behind: an existing version
		// directory must always mean the last build completed.
		b.log("Build failed — removing the staged artifact set at " + stagingRoot)
		b.remove(stagingRoot)
		if existing {
			b.log(fmt.Sprintf(
				"The existing base artifacts for core %s are untouched at %s.", coreMajor, versionDir,
			))
		}

		return Meta{}, err
	}

	if err := b.swapIntoPlace(coreMajor, existing, stagingRoot, staged.VersionDir, versionDir); err != nil {
		return Meta{}, err
	}

	return meta, nil
}

// makeStaging creates the directory a build in progress writes into, and
// resolves every path inside it.
func (b *Builder) makeStaging(coreMajor string) (stagingRoot string, staged Paths, err error) {
	// crypto/rand.Read never returns an error — it crashes the program rather
	// than handing back bytes it cannot vouch for — so there is no branch here
	// to test and none is written.
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)

	stagingRoot = filepath.Join(
		b.layout.Dir, stagingPrefix+coreMajor+"-"+hex.EncodeToString(suffix),
	)

	// The core major was validated by the caller, and this is the same value —
	// so resolving the staged paths here, once, is what lets everything after
	// it read as the sequence it is rather than as a chain of refusals that
	// cannot happen.
	staged, err = NewLayout(stagingRoot).PathsFor(coreMajor)
	if err != nil {
		return "", Paths{}, err
	}

	if err := filesystem.EnsureDirectory(staged.VersionDir, filesystem.ModeSharedDir); err != nil {
		return "", Paths{}, err
	}

	return stagingRoot, staged, nil
}

// swapIntoPlace moves a finished staged set into place: the outgoing one steps
// aside, the incoming one takes the name, the staging directory goes.
//
// Both moves are renames within the base-artifacts directory, so each is
// atomic and the whole swap is bounded by two of them rather than by the
// minutes a resolve and a site install take. The residual window is real but
// small: a process killed between the two renames leaves the core with no
// version directory and both sets inside the staging directory, which is why
// the failure message names it.
func (b *Builder) swapIntoPlace(
	coreMajor string, existing bool, stagingRoot, stagingVersionDir, versionDir string,
) error {
	retired := filepath.Join(stagingRoot, retiredDir)

	if existing {
		b.log(fmt.Sprintf("Retiring the previous base artifacts for core %s ...", coreMajor))
		if err := os.Rename(versionDir, retired); err != nil {
			return fmt.Errorf(
				"the new base artifacts for core %s built successfully, but the existing set at %s "+
					"could not be moved aside: %w\nThe existing set is untouched; the new one is at %s",
				coreMajor, versionDir, err, stagingVersionDir,
			)
		}
	}

	if err := os.Rename(stagingVersionDir, versionDir); err != nil {
		// No branch on whether there was a previous set. Reporting the staging
		// directory as a whole is true either way, and the alternative — a
		// message that names the retired path only sometimes — is a branch
		// that cannot be reached: both renames need write permission on the
		// same two directories, so the second cannot fail over permissions
		// once the first has succeeded.
		return fmt.Errorf(
			"the new base artifacts for core %s built successfully but could not be moved to %s: %w\n"+
				"Nothing has been deleted: the finished set is at %s, and %s holds anything moved "+
				"aside.\nMove the finished set into place by hand",
			coreMajor, versionDir, err, stagingVersionDir, stagingRoot,
		)
	}

	b.log(fmt.Sprintf("Base artifacts for core %s are in place at %s.", coreMajor, versionDir))
	b.remove(stagingRoot)

	return nil
}

// removeStaleStaging collects staging directories left by an earlier build
// that was killed outright.
//
// A crash between the staging creation and either exit path leaves a
// multi-gigabyte tree that nothing else collects: the base-artifacts directory
// is canonical, so prune protects it, and the leading-dot name keeps it out of
// every listing. Removed at the start of the next build for the same core,
// which is the next moment anyone is demonstrably not relying on it.
func (b *Builder) removeStaleStaging(coreMajor string) {
	entries, err := os.ReadDir(b.layout.Dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), stagingPrefix+coreMajor+"-") {
			continue
		}
		directory := filepath.Join(b.layout.Dir, entry.Name())
		b.log("Removing a staging directory left by an interrupted build: " + directory)
		b.remove(directory)
	}
}

// resolveAndInstall is the build itself, writing into the staged paths.
func (b *Builder) resolveAndInstall(coreMajor string, paths Paths, stability string) (Meta, error) {
	coreVersion, err := b.resolveTree(coreMajor, paths.Tree, stability)
	if err != nil {
		return Meta{}, err
	}

	environment, err := b.installAndDump(coreMajor, paths.Tree, paths.Dump)
	if err != nil {
		return Meta{}, err
	}

	meta := Meta{
		CoreVersion: coreVersion,
		CoreMajor:   coreMajor,
		PHPVersion:  environment.PHPVersion,
		DBEngine:    environment.DBEngine,
		BuiltAt:     time.Now(),
	}

	// Checked writes: a missing meta or canonical marker makes the set read as
	// incomplete and stops prune protecting it, while the command reports a
	// successful build. A failure here takes the whole staged set with it.
	contents, err := meta.ToYAML()
	if err != nil {
		return Meta{}, err
	}
	if err := filesystem.Write(paths.Meta, []byte(contents), filesystem.ModeShared); err != nil {
		return Meta{}, err
	}

	if err := filesystem.Write(paths.CanonicalMarker, []byte(
		"This artifact set is canonical: never auto-pruned. "+
			"Rebuild only via `upkeep base-artifacts:build --force`.\n",
	), filesystem.ModeShared); err != nil {
		return Meta{}, err
	}

	return meta, nil
}

// resolveTree resolves the canonical module-free base tree and reports the
// exact core version it landed on.
func (b *Builder) resolveTree(coreMajor, treePath, stability string) (string, error) {
	// A full resolve riding the shared composer cache: this produces the
	// pristine canonical tree every downstream environment copies from. Never
	// a bundled composer — shell out to the one the operator has.
	constraint := ConstraintFor(coreMajor, stability)
	b.log(fmt.Sprintf("Resolving %s into %s ...", constraint, treePath))

	if _, err := b.runner.Run([]string{
		"composer", "create-project", constraint, treePath, "--no-interaction",
	}, "", buildTimeout); err != nil {
		// Composer's own words first, then what can be done about them — the
		// same order a refused push uses. The commonest cause is a core major
		// with no stable release yet, and nothing in composer's output
		// suggests there is a flag for that.
		return "", fmt.Errorf("%w%s", err, UnresolvableHint(coreMajor, stability))
	}

	b.log("Validating resolved base tree (composer validate) ...")
	if _, err := b.runner.Run(
		[]string{"composer", "validate", "--no-interaction"}, treePath, buildTimeout,
	); err != nil {
		return "", err
	}

	lock, err := os.ReadFile(filepath.Join(treePath, "composer.lock"))
	if err != nil {
		return "", fmt.Errorf("the resolved tree has no readable composer.lock: %w", err)
	}

	coreVersion, err := CoreVersionFromLock(string(lock))
	if err != nil {
		return "", err
	}
	b.log("Resolved drupal/core " + coreVersion + ".")

	return coreVersion, nil
}

// installAndDump brings up a throwaway site from the tree and exports the
// clean-install dump.
func (b *Builder) installAndDump(coreMajor, treePath, dumpPath string) (InstallEnvironment, error) {
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)

	projectName := fmt.Sprintf("upkeep-base-d%s-%s", coreMajor, hex.EncodeToString(suffix))
	throwaway := filepath.Join(strings.TrimRight(b.scratchDir, "/"), projectName)

	if err := filesystem.EnsureDirectory(b.scratchDir, filesystem.ModeSharedDir); err != nil {
		return InstallEnvironment{}, err
	}

	environment, err := b.site.CleanInstallAndDump(
		coreMajor, treePath, throwaway, projectName, dumpPath,
	)
	// Torn down whether or not it worked: a failed install leaves containers
	// and volumes exactly as a successful one does.
	b.site.Teardown(throwaway, projectName)
	if err != nil {
		return InstallEnvironment{}, err
	}

	// Checked rather than assumed: an export that reported success and wrote
	// nothing would give every environment for this core an empty database,
	// and the first thing anyone would blame is the module.
	info, err := os.Stat(dumpPath)
	if err != nil || info.Size() == 0 {
		return InstallEnvironment{}, fmt.Errorf(
			"the DB export did not produce a non-empty dump at %q", dumpPath,
		)
	}

	return environment, nil
}

// remove deletes a tree, reporting failure to the log rather than to the
// caller: every caller is already on a path where the outcome is decided.
func (b *Builder) remove(path string) {
	if _, err := b.runner.Run([]string{"rm", "-rf", path}, "", buildTimeout); err != nil {
		b.log("Could not remove " + path + ": " + err.Error())
	}
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}

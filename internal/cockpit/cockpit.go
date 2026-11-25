// Package cockpit is the control-project directory: the module registry, the
// base artifacts, and the caches every command reads and writes.
package cockpit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/owenbush/upkeep/internal/filesystem"
)

// The directory layout, named once.
const (
	RegistryFilename  = "registry.yml"
	BaseArtifactsDir  = "base-artifacts"
	FixturesDir       = "fixtures"
	ProjectsDir       = "projects"
	ResultsDir        = "results"
	DashboardCacheDir = "cache/dashboard"
	PatchCacheDir     = "cache/patches"
	EnvVar            = "UPKEEP_COCKPIT"
)

// Cockpit is a control-project directory holding the module registry, base
// artifacts, and the shared fixture library.
//
// The root is canonicalised on construction, so ".." sequences and symlinked
// spellings are resolved once here instead of surviving into every derived
// path — which also means two spellings of one cockpit compare equal, which
// the prune surface's protected-root check depends on.
type Cockpit struct {
	Root string

	// searchedFrom is the working directory a parent walk started from when
	// the cockpit fell back to cwd and no ancestor held a registry. Empty for
	// a cockpit that was named or found, so only the walk's failure reports
	// itself as one.
	searchedFrom string
}

// New canonicalises root into a cockpit.
func New(root string) (*Cockpit, error) {
	canonical, err := filesystem.Canonicalize(root)
	if err != nil {
		return nil, err
	}

	return &Cockpit{Root: strings.TrimRight(canonical, "/")}, nil
}

// Resolve picks the cockpit directory: an explicit --cockpit flag first, then
// the UPKEEP_COCKPIT environment variable, then the nearest directory at or
// above the current working directory that holds a registry — the way git
// finds .git, so a command run from inside a working copy under
// <cockpit>/projects finds its cockpit without being told.
//
// A walk that finds nothing falls back to the working directory itself rather
// than failing here, because one surface (the drupal.org commands) runs
// without a registry when no cockpit was named; LoadRegistry is where the
// missing registry is reported, and it says the parents were searched too.
//
// A nil option is "the flag was not given"; a pointer to the empty string is
// "the flag was given empty", which is a mistake rather than a default.
func Resolve(option *string) (*Cockpit, error) {
	if option != nil {
		if *option == "" {
			return nil, fmt.Errorf(
				"an empty --cockpit was given. Pass the path to a cockpit directory, or omit --cockpit to use "+
					"$%s or the current directory", EnvVar,
			)
		}

		return New(*option)
	}

	if fromEnv := os.Getenv(EnvVar); fromEnv != "" {
		return New(fromEnv)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf(
			"cannot use the current directory as the cockpit: it is unavailable (it may have been deleted). "+
				"Re-run from an existing directory, or pass --cockpit / $%s: %w", EnvVar, err,
		)
	}

	root, found := discover(cwd)
	where, err := New(root)
	if err != nil {
		return nil, err
	}
	if !found {
		where.searchedFrom = where.Root
	}

	return where, nil
}

// discover walks up from start to the filesystem root looking for a
// directory holding the registry, and reports start itself when none does.
//
// The spelling the shell gave (which keeps a symlinked path such as a
// projects directory linked in from another disk) is walked first, then the
// canonical one when it differs, so a cockpit reachable either way is found.
// New canonicalises whatever is returned, so the cockpit's identity is the
// same as it would be had the directory been named.
func discover(start string) (string, bool) {
	spellings := []string{filepath.Clean(start)}
	if canonical, err := filesystem.Canonicalize(start); err == nil && canonical != spellings[0] {
		spellings = append(spellings, canonical)
	}

	for _, spelling := range spellings {
		for dir := spelling; ; dir = filepath.Dir(dir) {
			if holdsRegistry(dir) {
				return dir, true
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}

	return start, false
}

// holdsRegistry is whether dir has a registry as a regular file — a directory
// named registry.yml is not a cockpit, the same test init applies.
func holdsRegistry(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, RegistryFilename))

	return err == nil && info.Mode().IsRegular()
}

// RegistryPath is where the watchlist lives.
func (c *Cockpit) RegistryPath() string { return filepath.Join(c.Root, RegistryFilename) }

// BaseArtifactsPath is the per-core base tree and dump store.
func (c *Cockpit) BaseArtifactsPath() string { return filepath.Join(c.Root, BaseArtifactsDir) }

// FixturesPath is the shared fixture library.
func (c *Cockpit) FixturesPath() string { return filepath.Join(c.Root, FixturesDir) }

// ProjectsPath is the default projects root.
func (c *Cockpit) ProjectsPath() string { return filepath.Join(c.Root, ProjectsDir) }

// ResultsPath is where check persists per-run results and the dashboard and
// gate read them.
func (c *Cockpit) ResultsPath() string { return filepath.Join(c.Root, ResultsDir) }

// DashboardCachePath is where the dashboard caches remote GitLab and
// drupal.org state per module.
func (c *Cockpit) DashboardCachePath() string { return filepath.Join(c.Root, DashboardCacheDir) }

// PatchCachePath is where the patch commands cache the diff files they
// download, by issue.
func (c *Cockpit) PatchCachePath() string { return filepath.Join(c.Root, PatchCacheDir) }

// LoadRegistry reads and validates the watchlist.
//
// A cockpit that came from an unsuccessful parent walk says so: "not found at
// <cwd>/registry.yml" alone would hide that every directory above was tried.
func (c *Cockpit) LoadRegistry() (*Registry, error) {
	if _, err := os.Stat(c.RegistryPath()); c.searchedFrom != "" && os.IsNotExist(err) {
		return nil, fmt.Errorf(
			"no cockpit found: neither %q nor any directory above it holds %s. Pass --cockpit or set "+
				"$%s to point at a cockpit, or run \"upkeep init\" to create one here",
			c.searchedFrom, RegistryFilename, EnvVar,
		)
	}

	return RegistryFromFile(c.RegistryPath())
}

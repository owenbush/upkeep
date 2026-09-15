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
// the UPKEEP_COCKPIT environment variable, then the current working directory.
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

	return New(cwd)
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
func (c *Cockpit) LoadRegistry() (*Registry, error) { return RegistryFromFile(c.RegistryPath()) }

package dashboard

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/owenbush/upkeep/internal/filesystem"
	"github.com/owenbush/upkeep/internal/naming"
)

// Cache is the file-backed store for dashboard remote state — GitLab
// merge-request listings, drupal.org issue data. One JSON file per module
// under <cockpit>/cache/dashboard/.
//
// Local check results and gate verdicts are NOT cached here: they are always
// resolved fresh so the dashboard reflects the latest check runs.
//
// Files are owner-only: a snapshot serialises the complete upstream payloads
// fetched with the maintainer's PAT, which for any limited-visibility project
// is token-scoped data. Reads are lenient in the same documented way the
// results cache is — a truncated or malformed cache file is a miss the
// dashboard re-fetches over, never a failure demanding a manual rm.
type Cache struct {
	dir string
}

// NewCache opens the store rooted at cacheDir.
func NewCache(cacheDir string) *Cache { return &Cache{dir: cacheDir} }

// Load reads one module's snapshot. It reports false for anything unusable,
// which the caller reads as "no cache".
func (c *Cache) Load(module string) (ModuleSnapshot, bool) {
	path, err := c.path(module)
	if err != nil {
		return ModuleSnapshot{}, false
	}

	// Every unreadable-cache outcome is the same documented miss the dashboard
	// re-fetches over, so none of them may print anything into the operator's
	// table.
	contents, err := os.ReadFile(path)
	if err != nil {
		return ModuleSnapshot{}, false
	}

	return SnapshotFromJSON(string(contents))
}

// Save writes one module's snapshot.
func (c *Cache) Save(module string, snapshot ModuleSnapshot) error {
	path, err := c.path(module)
	if err != nil {
		return err
	}

	rendered, err := snapshot.ToJSON()
	if err != nil {
		return err
	}

	if err := filesystem.EnsureDirectory(c.dir, filesystem.ModePrivateDir); err != nil {
		return err
	}

	return filesystem.Write(path, []byte(rendered), filesystem.ModePrivate)
}

// OldestFetchedAt is the oldest fetch timestamp across the named modules. It
// is the zero time when nothing is cached.
func (c *Cache) OldestFetchedAt(moduleNames []string) time.Time {
	var oldest time.Time
	for _, name := range moduleNames {
		snapshot, loaded := c.Load(name)
		if !loaded {
			continue
		}
		if oldest.IsZero() || snapshot.FetchedAt.Before(oldest) {
			oldest = snapshot.FetchedAt
		}
	}

	return oldest
}

func (c *Cache) path(module string) (string, error) {
	if !naming.IsModuleName(module) {
		return "", fmt.Errorf(
			"the dashboard cache is keyed by module machine name ([a-z][a-z0-9_]*), got %q", module,
		)
	}

	return filepath.Join(c.dir, module+".json"), nil
}

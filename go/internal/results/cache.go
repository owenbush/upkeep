package results

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/filesystem"
	"github.com/owenbush/upkeep/internal/naming"
)

// outputExcerptBytes bounds what is cached: an excerpt for reporting, not a
// full log archive.
const outputExcerptBytes = 4000

var shaPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// CachedResult is one cached local-check outcome for a (module, subject, core)
// at a specific revision.
//
// The revision is part of the identity: a result recorded against an older
// head is stale evidence, and consumers — the dashboard's LOCAL column, the
// fast-lane gate — must compare it against what is there now before trusting
// it.
type CachedResult struct {
	SHA        string
	RecordedAt time.Time
	Result     check.RunResult
}

// Cache is the file-backed store for local check results, shared by the check
// command (writer) and the dashboard and gate (readers).
//
// Layout: <cockpit>/results/<module>/<subject>/<core>/<sha>.json — one file
// per checked revision, so history survives re-checks and staleness is a
// revision comparison, not a timestamp guess.
//
// <subject> is a Key: a merge-request IID, or patch-<nid> for a patch on an
// issue. The two share this store and are kept in separate namespaces
// structurally, because a patch result read as a merge-request result would
// put patch evidence in front of the fast-lane gate.
//
// <sha> is whatever identifies the revision that was checked: a merge
// request's head SHA, or the content hash of the patch file. Both are 7-64 hex
// characters, and both answer the same question — "is this evidence about what
// is there now?" — so both are compared the same way by consumers.
//
// Every component of that path is validated before it becomes a path segment.
// The module name and core version come from the registry (validated at load,
// re-asserted here because this is a public API), and the SHA may be an
// unvalidated remote value from the GitLab API, so it is required to look like
// one before it becomes a filename.
//
// Files are owner-only: the payload embeds up to 4000 bytes of raw check
// output per check — host paths, source fragments, stack traces, database
// diagnostics — which has no business being world-readable.
type Cache struct {
	dir string
}

// NewCache opens the store rooted at resultsDir.
func NewCache(resultsDir string) *Cache { return &Cache{dir: resultsDir} }

// storedCheck is the on-disk shape of one check.
type storedCheck struct {
	Type     string  `json:"type"`
	Status   string  `json:"status"`
	ExitCode *int    `json:"exit_code"`
	Output   string  `json:"output"`
	Duration float64 `json:"duration_seconds"`
}

type storedResult struct {
	SHA        string        `json:"sha"`
	RecordedAt string        `json:"recorded_at"`
	Results    []storedCheck `json:"results"`
}

// Store records a run. A zero recordedAt means now.
func (c *Cache) Store(
	module string,
	key Key,
	coreMajor, sha string,
	result check.RunResult,
	recordedAt time.Time,
) error {
	dir, err := c.entryDir(module, key, coreMajor)
	if err != nil {
		return err
	}
	if err := assertSHA(sha); err != nil {
		return err
	}
	if recordedAt.IsZero() {
		recordedAt = time.Now()
	}

	stored := storedResult{
		SHA:        sha,
		RecordedAt: recordedAt.Format(time.RFC3339),
		Results:    make([]storedCheck, 0, len(result.Results)),
	}
	for _, one := range result.Results {
		output := one.Output
		if len(output) > outputExcerptBytes {
			output = output[:outputExcerptBytes]
		}
		stored.Results = append(stored.Results, storedCheck{
			Type:     string(one.Type),
			Status:   string(one.Status),
			ExitCode: one.ExitCode,
			Output:   output,
			Duration: one.Duration.Seconds(),
		})
	}

	payload, err := json.MarshalIndent(stored, "", "    ")
	if err != nil {
		return fmt.Errorf("encoding the result for %s: %w", sha, err)
	}

	if err := filesystem.EnsureDirectory(dir, filesystem.ModePrivateDir); err != nil {
		return err
	}

	return filesystem.Write(filepath.Join(dir, sha+".json"), append(payload, '\n'), filesystem.ModePrivate)
}

// Find is the result recorded for one exact revision, or nil when never
// checked.
func (c *Cache) Find(module string, key Key, coreMajor, sha string) *CachedResult {
	dir, err := c.entryDir(module, key, coreMajor)
	if err != nil {
		// A read for an impossible identity is a miss, not an error: the
		// caller only wants to know whether a result was recorded.
		return nil
	}
	if err := assertSHA(sha); err != nil {
		return nil
	}

	return read(filepath.Join(dir, sha+".json"))
}

// Latest is the most recently recorded result for the (module, subject, core)
// regardless of revision.
//
// Callers deciding freshness must compare its SHA against what is there now —
// the merge request's current head, or the current patch's content hash.
func (c *Cache) Latest(module string, key Key, coreMajor string) (*CachedResult, error) {
	dir, err := c.entryDir(module, key, coreMajor)
	if err != nil {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		// An unreadable directory lists as empty, exactly like one with no
		// results in it. Distinguished explicitly: reporting it as "never
		// checked" would let the gate deny a merge for a reason that is
		// invisible to the operator.
		return nil, fmt.Errorf(
			"cannot list the cached results in %q — the directory is not readable. Its results are being "+
				"reported as absent, which is not the same as never checked: %w",
			dir, err,
		)
	}

	var newest *CachedResult
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if found := read(filepath.Join(dir, entry.Name())); found != nil {
			if newest == nil || found.RecordedAt.After(newest.RecordedAt) {
				newest = found
			}
		}
	}

	return newest, nil
}

func (c *Cache) entryDir(module string, key Key, coreMajor string) (string, error) {
	if !naming.IsModuleName(module) {
		return "", fmt.Errorf(
			"results are stored per module machine name ([a-z][a-z0-9_]*), got %q", module,
		)
	}
	if !naming.IsCoreMajor(coreMajor) {
		return "", fmt.Errorf("results are stored per core major version number, got %q", coreMajor)
	}

	return filepath.Join(c.dir, module, key.Segment, coreMajor), nil
}

func assertSHA(sha string) error {
	if !shaPattern.MatchString(sha) {
		return fmt.Errorf(
			"a cached result is keyed by the revision it covers (7-64 hex characters), got %q", sha,
		)
	}

	return nil
}

// read is lenient by design: a malformed cache file is a miss, never an error.
func read(file string) *CachedResult {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil
	}

	var stored storedResult
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil
	}
	if stored.SHA == "" || stored.RecordedAt == "" {
		return nil
	}
	recordedAt, err := time.Parse(time.RFC3339, stored.RecordedAt)
	if err != nil {
		return nil
	}

	checks := make([]check.Result, 0, len(stored.Results))
	for _, one := range stored.Results {
		checkType, ok := knownType(one.Type)
		if !ok {
			return nil
		}
		status, ok := knownStatus(one.Status)
		if !ok {
			return nil
		}
		checks = append(checks, check.Result{
			Type:     checkType,
			Status:   status,
			ExitCode: one.ExitCode,
			Output:   one.Output,
			Duration: time.Duration(one.Duration * float64(time.Second)),
		})
	}

	return &CachedResult{SHA: stored.SHA, RecordedAt: recordedAt, Result: check.RunResult{Results: checks}}
}

// knownType and knownStatus refuse a value the enum does not have, matching
// the PHP side's `from()`: a cache file naming a check this build does not
// know is a miss rather than a result with an invented type.
func knownType(value string) (check.Type, bool) {
	for _, known := range []check.Type{
		check.PhpUnit, check.PhpCs, check.PhpStan, check.EsLint, check.StyleLint,
		check.ModuleInstall, check.FunctionalSmoke, check.Deprecation,
	} {
		if string(known) == value {
			return known, true
		}
	}

	return "", false
}

func knownStatus(value string) (check.Status, bool) {
	for _, known := range []check.Status{check.Passed, check.Failed, check.NoTests, check.Unavailable} {
		if string(known) == value {
			return known, true
		}
	}

	return "", false
}

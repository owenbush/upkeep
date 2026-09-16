package maintenance

import "time"

// Category is a disk-inventory category `status --disk` reports and `prune`
// reasons about.
//
// Two of them are canonical by definition and can never be pruned: base
// artifacts and committed fixture dumps.
type Category string

const (
	// BaseArtifact is a per-core-version base artifact directory. Always
	// canonical.
	BaseArtifact Category = "base-artifact"
	// ProjectTree is a provisioned environment tree under the projects root.
	// Disposable.
	ProjectTree Category = "project-tree"
	// Snapshot is a materialised fixture snapshot. A disposable cache.
	Snapshot Category = "snapshot"
	// FixtureDump is a committed .sql.gz dump, in a module's tests/fixtures/
	// or in the cockpit fixture library. Always canonical.
	FixtureDump Category = "fixture-dump"
	// ProjectVolume is a docker named volume belonging to an engine project.
	// Reclaimed only via adapter teardown.
	ProjectVolume Category = "project-volume"
)

var categoryLabels = map[Category]string{
	BaseArtifact:  "base artifact",
	ProjectTree:   "project tree",
	Snapshot:      "materialized snapshot",
	FixtureDump:   "fixture dump",
	ProjectVolume: "project volume",
}

// Label is the category as the maintenance tables print it.
func (c Category) Label() string { return categoryLabels[c] }

// Item is one entry in the disk inventory: a path — or, for volumes, a docker
// volume name — with its category, attribution, measured size, and the facts
// the prune selector needs.
type Item struct {
	Path     string
	Category Category
	Size     int64

	Module      string
	CoreMajor   string
	ProjectName string

	// LastUsedAt is the zero time when nothing records when this was last
	// touched, which the selector reads as "cannot tell" rather than "old".
	LastUsedAt time.Time
	// KeepMarked is whether the operator asked for this to survive a prune.
	KeepMarked bool
}

// Age is how long since this was last used. It reports false when nothing
// records that.
func (i Item) Age(now time.Time) (time.Duration, bool) {
	if i.LastUsedAt.IsZero() {
		return 0, false
	}

	age := now.Sub(i.LastUsedAt)
	if age < 0 {
		return 0, true
	}

	return age, true
}

// Scope is what a prune invocation targets.
//
// Trees and Projects both dispose of environments through the adapter's
// teardown — a tree can never be deleted with a bare rm -rf, which would leave
// its containers running. They differ in what the candidate list itemises:
// Projects additionally itemises the engine projects' named volumes, so the
// reclaim total includes them.
type Scope string

const (
	ScopeTrees     Scope = "trees"
	ScopeSnapshots Scope = "snapshots"
	ScopeProjects  Scope = "projects"
	ScopeAll       Scope = "all"
)

// Categories is what this scope covers.
func (s Scope) Categories() []Category {
	switch s {
	case ScopeTrees:
		return []Category{ProjectTree}
	case ScopeSnapshots:
		return []Category{Snapshot}
	case ScopeProjects:
		return []Category{ProjectTree, ProjectVolume}
	case ScopeAll:
		return []Category{ProjectTree, ProjectVolume, Snapshot}
	default:
		return nil
	}
}

// Outcome is what a confirmed prune actually did: bytes freed, items deleted,
// and items skipped with the reason — an unresolvable environment is never
// guessed at.
type Outcome struct {
	FreedBytes int64
	Deleted    []Item
	Skipped    []SkippedItem
}

// SkippedItem is one thing a prune left alone, and why.
type SkippedItem struct {
	Item   Item
	Reason string
}

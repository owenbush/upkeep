package maintenance

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/filesystem"
)

// Selector is pure candidate selection over an inventory snapshot.
//
// This is the safety boundary of the prune surface: whatever flags are passed,
// an item is only ever a candidate when NONE of the protection rules apply.
//
// Protection rules, each independently sufficient to exclude:
//   - the category is a base artifact or a fixture dump (canonical by
//     definition);
//   - the item is keep-marked (a .keep marker exists next to it);
//   - its path lies under a protected root — the cockpit's base-artifacts/ and
//     fixtures/ directories — which guards against the scanner mislabelling
//     something;
//   - its path contains a tests/fixtures/ segment (committed module dumps);
//   - it is the tree, or a volume, of an environment holding a keep-marked
//     snapshot: pruning the tree would destroy the kept snapshot with it, so
//     the keep mark escalates to the whole environment. Committed dumps do
//     NOT escalate — the module clone is regenerable from upstream.
type Selector struct {
	// protectedRoots are absolute directory prefixes nothing may ever be
	// selected from.
	protectedRoots []string
}

// NewSelector guards the given roots.
func NewSelector(protectedRoots []string) *Selector {
	return &Selector{protectedRoots: protectedRoots}
}

// SelectInput is one selection run.
type SelectInput struct {
	Items []Item
	Scope Scope
	// OlderThan admits only items at least this old. Items of unknown age are
	// excluded when a filter is given. Zero means no filter.
	OlderThan time.Duration
	Now       time.Time
	// KeepLatest retains, per project, this many newest unprotected snapshots.
	KeepLatest int
}

// Select is the deletion candidates, grouped by the scope's category order.
func (s *Selector) Select(in SelectInput) []Item {
	keptProjects := projectsWithKeepMarkedSnapshots(in.Items)
	inScopeCategories := map[Category]int{}
	for order, category := range in.Scope.Categories() {
		inScopeCategories[category] = order
	}

	candidates := []Item{}
	for _, item := range in.Items {
		if _, covered := inScopeCategories[item.Category]; !covered {
			continue
		}
		if s.ProtectionReason(item) != "" {
			continue
		}
		if keepMarkEscalates(item, keptProjects) {
			continue
		}
		candidates = append(candidates, item)
	}

	// keep-latest budgets are computed over ALL unprotected snapshots, before
	// the age filter: the newest snapshots fill the budget even when they are
	// too young to be deletion candidates anyway.
	candidates = applyKeepLatest(candidates, in.KeepLatest)

	kept := candidates[:0:0]
	for _, item := range candidates {
		if oldEnough(item, in.OlderThan, in.Now) {
			kept = append(kept, item)
		}
	}

	// Stable-order by the scope's category order — trees before volumes before
	// snapshots — so the rendered candidate list reads grouped.
	sort.SliceStable(kept, func(a, b int) bool {
		return inScopeCategories[kept[a].Category] < inScopeCategories[kept[b].Category]
	})

	return kept
}

// ProtectionReason is why an item may never be deleted, or "" when it is
// disposable.
//
// Exported so the executor can re-assert it immediately before deletion.
func (s *Selector) ProtectionReason(item Item) string {
	switch item.Category {
	case BaseArtifact:
		return "canonical base artifact"
	case FixtureDump:
		return "committed fixture dump"
	}

	if item.KeepMarked {
		return "keep-marked (.keep)"
	}

	for _, root := range s.protectedRoots {
		// Path identity, not string equality: an item reaching here under an
		// equivalent-but-differently-spelled path — a ".." sequence, a
		// symlinked component — is the same file and must be as protected.
		if samePath(root, item.Path) {
			return fmt.Sprintf("under protected root %s", strings.TrimRight(root, "/"))
		}
	}

	if strings.Contains(item.Path, "/tests/fixtures/") {
		return "inside a committed tests/fixtures/ directory"
	}

	return ""
}

// samePath reports whether path is root or lies under it, compared on
// canonical paths.
//
// A path so malformed that it cannot be canonicalised at all falls back to the
// lexical prefix test, which can only ever protect more, never less.
func samePath(root, path string) bool {
	within, err := filesystem.IsWithin(root, path)
	if err == nil {
		return within
	}

	prefix := strings.TrimRight(root, "/")

	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// projectsWithKeepMarkedSnapshots is the project names whose snapshot store
// holds a keep-marked snapshot.
func projectsWithKeepMarkedSnapshots(items []Item) map[string]bool {
	projects := map[string]bool{}
	for _, item := range items {
		if item.Category == Snapshot && item.KeepMarked && item.ProjectName != "" {
			projects[item.ProjectName] = true
		}
	}

	return projects
}

func keepMarkEscalates(item Item, keptProjects map[string]bool) bool {
	if item.Category != ProjectTree && item.Category != ProjectVolume {
		return false
	}

	return item.ProjectName != "" && keptProjects[item.ProjectName]
}

func oldEnough(item Item, olderThan time.Duration, now time.Time) bool {
	if olderThan <= 0 {
		return true
	}

	age, known := item.Age(now)

	return known && age >= olderThan
}

// applyKeepLatest retains, per project, the N newest unprotected snapshots.
func applyKeepLatest(candidates []Item, keepLatest int) []Item {
	if keepLatest <= 0 {
		return candidates
	}

	// By index rather than by value, because two snapshots of one project can
	// be equal in every field and Go has no object identity for a struct.
	byProject := map[string][]int{}
	for index, item := range candidates {
		if item.Category == Snapshot {
			byProject[item.ProjectName] = append(byProject[item.ProjectName], index)
		}
	}

	retained := map[int]bool{}
	for _, indexes := range byProject {
		newestFirst := append([]int{}, indexes...)
		sort.SliceStable(newestFirst, func(a, b int) bool {
			// The zero time is the earliest there is, so a snapshot nothing
			// records a use for sorts oldest for free — it can never fill a
			// retention budget ahead of one that is demonstrably recent.
			return candidates[newestFirst[a]].LastUsedAt.After(candidates[newestFirst[b]].LastUsedAt)
		})
		for _, index := range newestFirst[:min(keepLatest, len(newestFirst))] {
			retained[index] = true
		}
	}

	kept := []Item{}
	for index, item := range candidates {
		if item.Category == Snapshot && retained[index] {
			continue
		}
		kept = append(kept, item)
	}

	return kept
}

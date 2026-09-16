package maintenance

import (
	"slices"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

func tree(project string, age time.Duration) Item {
	return Item{
		Path: "/home/me/.upkeep/projects/" + project, Category: ProjectTree, Size: 900 << 20,
		ProjectName: project, LastUsedAt: ago(age),
	}
}

func snapshot(project, name string, age time.Duration) Item {
	return Item{
		Path:        "/home/me/.upkeep/projects/" + project + "/.upkeep/snapshots/" + name + ".sql",
		Category:    Snapshot,
		Size:        40 << 20,
		ProjectName: project,
		LastUsedAt:  ago(age),
	}
}

func volume(project string, age time.Duration) Item {
	return Item{
		Path: project + "-mariadb", Category: ProjectVolume, Size: 300 << 20,
		ProjectName: project, LastUsedAt: ago(age),
	}
}

func selector() *Selector {
	return NewSelector([]string{"/home/me/cockpit/base-artifacts", "/home/me/cockpit/fixtures"})
}

func paths(items []Item) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.Path)
	}

	return out
}

// Whatever flags are passed, an item is only ever a candidate when none of the
// protection rules apply. This is the safety boundary.
func TestNothingCanonicalIsEverACandidate(t *testing.T) {
	items := []Item{
		{Path: "/home/me/cockpit/base-artifacts/11", Category: BaseArtifact, Size: 2 << 30},
		{Path: "/home/me/modules/pathauto/tests/fixtures/dump.sql.gz", Category: FixtureDump, Size: 1 << 20},
		// Mislabelled by the scanner, but still under a protected root.
		{Path: "/home/me/cockpit/base-artifacts/11/tree", Category: ProjectTree, Size: 1 << 30},
		{Path: "/home/me/cockpit/fixtures/library.sql.gz", Category: Snapshot, Size: 1 << 20},
		// A committed dump the scanner called a snapshot.
		{Path: "/home/me/modules/token/tests/fixtures/x.sql.gz", Category: Snapshot, Size: 1 << 20},
		// Keep-marked.
		{Path: "/home/me/.upkeep/projects/upkeep-pathauto-d11", Category: ProjectTree, KeepMarked: true},
	}

	for _, scope := range []Scope{ScopeTrees, ScopeSnapshots, ScopeProjects, ScopeAll} {
		got := selector().Select(SelectInput{Items: items, Scope: scope, Now: now})
		if len(got) != 0 {
			t.Errorf("scope %s selected %v", scope, paths(got))
		}
	}

	for _, item := range items {
		if reason := selector().ProtectionReason(item); reason == "" {
			t.Errorf("%s (%s) has no protection reason", item.Path, item.Category)
		}
	}
}

// An item reaching here under an equivalent-but-differently-spelled path is
// the same file and must be as protected.
func TestAProtectedRootIsMatchedOnPathIdentityNotSpelling(t *testing.T) {
	spellings := []string{
		"/home/me/cockpit/base-artifacts/11",
		"/home/me/cockpit/base-artifacts/../base-artifacts/11",
		"/home/me/cockpit/./base-artifacts/11",
		"/home/me/cockpit/base-artifacts",
		"/home/me/cockpit/base-artifacts/",
	}

	for _, path := range spellings {
		item := Item{Path: path, Category: ProjectTree}
		if reason := selector().ProtectionReason(item); reason == "" {
			t.Errorf("%q was not recognised as the protected root", path)
		}
	}

	// And a sibling that merely starts with the same characters is not under
	// it.
	sibling := Item{Path: "/home/me/cockpit/base-artifacts-old/11", Category: ProjectTree}
	if reason := selector().ProtectionReason(sibling); reason != "" {
		t.Errorf("%q was protected by a prefix match: %s", sibling.Path, reason)
	}
}

// Pruning the tree would destroy the kept snapshot with it, so the keep mark
// escalates to the whole environment.
func TestAKeptSnapshotProtectsItsWholeEnvironment(t *testing.T) {
	kept := snapshot("upkeep-pathauto-d11", "baseline", 90*24*time.Hour)
	kept.KeepMarked = true

	items := []Item{
		tree("upkeep-pathauto-d11", 90*24*time.Hour),
		volume("upkeep-pathauto-d11", 90*24*time.Hour),
		kept,
		snapshot("upkeep-pathauto-d11", "scratch", 90*24*time.Hour),
		// A different environment is untouched by it.
		tree("upkeep-token-d11", 90*24*time.Hour),
	}

	got := selector().Select(SelectInput{Items: items, Scope: ScopeAll, Now: now})

	for _, item := range got {
		if item.ProjectName == "upkeep-pathauto-d11" && item.Category != Snapshot {
			t.Errorf("%s was selected despite a kept snapshot in its environment", item.Path)
		}
	}
	if !slices.Contains(paths(got), "/home/me/.upkeep/projects/upkeep-token-d11") {
		t.Errorf("the unrelated environment was protected too: %v", paths(got))
	}
	// The kept snapshot's sibling is still disposable — the escalation is to
	// the environment's tree and volumes, not to every snapshot in it.
	if !slices.Contains(paths(got), kept.Path[:strings.LastIndex(kept.Path, "/")+1]+"scratch.sql") {
		t.Errorf("the unmarked snapshot was protected: %v", paths(got))
	}
}

// A committed dump does not escalate: the module clone is regenerable from
// upstream.
func TestACommittedDumpDoesNotProtectItsEnvironment(t *testing.T) {
	items := []Item{
		tree("upkeep-pathauto-d11", 90*24*time.Hour),
		{
			Path:        "/home/me/.upkeep/projects/upkeep-pathauto-d11/web/modules/pathauto/tests/fixtures/x.sql.gz",
			Category:    FixtureDump,
			ProjectName: "upkeep-pathauto-d11",
			KeepMarked:  true,
		},
	}

	got := selector().Select(SelectInput{Items: items, Scope: ScopeTrees, Now: now})
	if len(got) != 1 {
		t.Fatalf("got %v", paths(got))
	}
}

// A typo like --older-than=30 must never silently mean "30 seconds".
func TestTheAgeFilterExcludesAnythingYoungerAndAnythingUnknown(t *testing.T) {
	unknownAge := tree("upkeep-webform-d11", 0)
	unknownAge.LastUsedAt = time.Time{}

	items := []Item{
		tree("upkeep-pathauto-d11", 40*24*time.Hour),
		tree("upkeep-token-d11", 5*24*time.Hour),
		unknownAge,
	}

	got := selector().Select(SelectInput{
		Items: items, Scope: ScopeTrees, OlderThan: 30 * 24 * time.Hour, Now: now,
	})

	if !slices.Equal(paths(got), []string{"/home/me/.upkeep/projects/upkeep-pathauto-d11"}) {
		t.Errorf("got %v, want only the one older than the filter", paths(got))
	}
}

// With no filter, an item of unknown age is still a candidate — the filter is
// what makes the unknown disqualifying.
func TestWithNoAgeFilterUnknownAgeIsStillACandidate(t *testing.T) {
	unknownAge := tree("upkeep-webform-d11", 0)
	unknownAge.LastUsedAt = time.Time{}

	got := selector().Select(SelectInput{Items: []Item{unknownAge}, Scope: ScopeTrees, Now: now})
	if len(got) != 1 {
		t.Errorf("got %v", paths(got))
	}
}

// Per project, the N newest unprotected snapshots are retained.
func TestKeepLatestRetainsTheNewestPerProject(t *testing.T) {
	items := []Item{
		snapshot("a", "newest", 1*time.Hour),
		snapshot("a", "middle", 2*time.Hour),
		snapshot("a", "oldest", 3*time.Hour),
		snapshot("b", "only", 5*time.Hour),
	}

	got := selector().Select(SelectInput{Items: items, Scope: ScopeSnapshots, Now: now, KeepLatest: 2})

	names := paths(got)
	if len(names) != 1 || !strings.HasSuffix(names[0], "oldest.sql") {
		t.Errorf("got %v, want only project a's oldest", names)
	}
}

// The budget is computed over ALL unprotected snapshots, before the age
// filter: the newest fill it even when they are too young to be candidates.
func TestTheKeepLatestBudgetIsFilledBeforeTheAgeFilterApplies(t *testing.T) {
	items := []Item{
		snapshot("a", "yesterday", 24*time.Hour),
		snapshot("a", "ancient-1", 90*24*time.Hour),
		snapshot("a", "ancient-2", 91*24*time.Hour),
	}

	got := selector().Select(SelectInput{
		Items: items, Scope: ScopeSnapshots, OlderThan: 30 * 24 * time.Hour, Now: now, KeepLatest: 1,
	})

	// Yesterday's fills the budget; both ancient ones are then candidates.
	if len(got) != 2 {
		t.Fatalf("got %v", paths(got))
	}
	for _, path := range paths(got) {
		if strings.Contains(path, "yesterday") {
			t.Errorf("the retained snapshot was selected: %v", paths(got))
		}
	}
}

// A snapshot nothing records a use for must not fill a retention budget ahead
// of one that is demonstrably recent.
func TestAnUnknownLastUseDoesNotWinARetentionSlot(t *testing.T) {
	unknown := snapshot("a", "unknown", 0)
	unknown.LastUsedAt = time.Time{}

	items := []Item{unknown, snapshot("a", "recent", time.Hour)}

	got := selector().Select(SelectInput{Items: items, Scope: ScopeSnapshots, Now: now, KeepLatest: 1})

	if len(got) != 1 || !strings.Contains(got[0].Path, "unknown") {
		t.Errorf("got %v, want the unknown one selected and the recent one kept", paths(got))
	}
}

// Trees and Projects differ in what the candidate list itemises: Projects also
// itemises the named volumes, so the reclaim total includes them.
func TestTheScopeDecidesWhatIsItemised(t *testing.T) {
	items := []Item{
		tree("upkeep-pathauto-d11", time.Hour),
		volume("upkeep-pathauto-d11", time.Hour),
		snapshot("upkeep-pathauto-d11", "s", time.Hour),
	}

	for scope, want := range map[Scope][]Category{
		ScopeTrees:     {ProjectTree},
		ScopeSnapshots: {Snapshot},
		ScopeProjects:  {ProjectTree, ProjectVolume},
		ScopeAll:       {ProjectTree, ProjectVolume, Snapshot},
	} {
		got := selector().Select(SelectInput{Items: items, Scope: scope, Now: now})

		categories := []Category{}
		for _, item := range got {
			categories = append(categories, item.Category)
		}
		if !slices.Equal(categories, want) {
			t.Errorf("scope %s itemised %v, want %v", scope, categories, want)
		}
	}
}

// Trees before volumes before snapshots, so the rendered list reads grouped.
func TestCandidatesComeOutInTheScopesCategoryOrder(t *testing.T) {
	// Deliberately given in the wrong order.
	items := []Item{
		snapshot("upkeep-pathauto-d11", "s", time.Hour),
		volume("upkeep-pathauto-d11", time.Hour),
		tree("upkeep-pathauto-d11", time.Hour),
	}

	got := selector().Select(SelectInput{Items: items, Scope: ScopeAll, Now: now})

	categories := []Category{}
	for _, item := range got {
		categories = append(categories, item.Category)
	}
	if !slices.Equal(categories, []Category{ProjectTree, ProjectVolume, Snapshot}) {
		t.Errorf("got %v", categories)
	}
}

func TestAnUnknownScopeSelectsNothing(t *testing.T) {
	got := selector().Select(SelectInput{
		Items: []Item{tree("upkeep-pathauto-d11", time.Hour)}, Scope: Scope("nonsense"), Now: now,
	})

	if len(got) != 0 {
		t.Errorf("got %v", paths(got))
	}
}

// Selection must not mutate what it was handed.
func TestSelectionDoesNotReorderTheCallersSlice(t *testing.T) {
	items := []Item{
		snapshot("a", "s", time.Hour),
		tree("upkeep-pathauto-d11", time.Hour),
	}
	before := paths(items)

	selector().Select(SelectInput{Items: items, Scope: ScopeAll, Now: now})

	if !slices.Equal(paths(items), before) {
		t.Errorf("the caller's slice came back as %v", paths(items))
	}
}

// A clock that ran backwards — a machine that slept, a file restored from a
// backup — must not produce a negative age that reads as "older than any
// filter".
func TestAnItemUsedInTheFutureHasNoAgeRatherThanANegativeOne(t *testing.T) {
	future := tree("upkeep-pathauto-d11", -48*time.Hour)

	age, known := future.Age(now)
	if !known {
		t.Fatal("a recorded last-use read as unknown")
	}
	if age != 0 {
		t.Errorf("age %v, want it clamped to nothing", age)
	}

	// And it is therefore not a candidate under any age filter.
	got := selector().Select(SelectInput{
		Items: []Item{future}, Scope: ScopeTrees, OlderThan: time.Second, Now: now,
	})
	if len(got) != 0 {
		t.Errorf("got %v", paths(got))
	}
}

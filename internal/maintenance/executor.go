package maintenance

import (
	"fmt"
	"os"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cockpit"
)

// Teardown is the adapter's environment disposal, as the executor needs it.
//
// Narrowed to the one method rather than taking the whole engine: this is the
// only destructive thing a prune may do, and a type that cannot reach the
// other fifteen methods cannot accidentally provision something mid-prune.
type Teardown interface {
	Teardown(module cockpit.Module, coreMajor string) error
}

// Executor carries out a confirmed prune over selector-approved candidates.
//
// Environment disposal ALWAYS goes through the adapter's teardown, never a raw
// tree removal — that would leave the engine project's containers running and
// its named volumes allocated. Volume candidates are reclaimed by their
// project's teardown and never touched directly. Snapshot candidates are plain
// file removals: the artifact and its sidecar.
type Executor struct {
	selector *Selector
	teardown Teardown
	modules  map[string]cockpit.Module
	log      func(string)
}

// NewExecutor builds an executor. A nil log discards progress.
func NewExecutor(
	selector *Selector,
	teardown Teardown,
	registryModules map[string]cockpit.Module,
	log func(string),
) *Executor {
	if log == nil {
		log = func(string) {}
	}

	return &Executor{selector: selector, teardown: teardown, modules: registryModules, log: log}
}

// Execute deletes the candidates and reports what it actually did.
//
// Defence in depth: every item is re-checked against the selector's protection
// rules immediately before anything is deleted, and a protected item anywhere
// in the list aborts the whole run without deleting anything. The candidate
// list arrives from the selector, so a protected item in it means selection
// was bypassed — which is a bug, and the safe response to a bug in a
// destructive path is to stop.
func (e *Executor) Execute(candidates []Item) (Outcome, error) {
	for _, item := range candidates {
		if reason := e.selector.ProtectionReason(item); reason != "" {
			return Outcome{}, fmt.Errorf(
				"refusing to prune protected item %q (%s) — candidate selection was bypassed; "+
					"aborting without deleting anything",
				item.Path, reason,
			)
		}
	}

	outcome := Outcome{}

	// Trees first, because a volume is reclaimed by its project's teardown and
	// so has to know whether that teardown happened.
	tornDown := e.tearDownTrees(candidates, &outcome)
	e.accountForVolumes(candidates, tornDown, &outcome)
	e.removeSnapshots(candidates, &outcome)

	return outcome, nil
}

// tearDownTrees disposes of every environment candidate, and reports which
// projects it managed to.
func (e *Executor) tearDownTrees(candidates []Item, outcome *Outcome) map[string]bool {
	tornDown := map[string]bool{}

	for _, item := range candidates {
		if item.Category != ProjectTree {
			continue
		}

		module, coreMajor, resolved := e.resolveEnvironment(item)
		if !resolved {
			outcome.Skipped = append(outcome.Skipped, SkippedItem{item,
				"cannot attribute this tree to a registered (module x core) pair — refusing to guess; " +
					"tear it down manually"})

			continue
		}

		e.log(fmt.Sprintf(
			"Tearing down environment %s (module %s, Drupal %s) via the adapter ...",
			nameOrPath(item), module.Name, coreMajor,
		))
		if err := e.teardown.Teardown(module, coreMajor); err != nil {
			outcome.Skipped = append(outcome.Skipped, SkippedItem{item, err.Error()})

			continue
		}

		tornDown[item.ProjectName] = true
		outcome.FreedBytes += item.Size
		outcome.Deleted = append(outcome.Deleted, item)
	}

	return tornDown
}

// accountForVolumes credits the volumes their project's teardown reclaimed.
//
// Never deleted directly. A volume whose tree was not pruned in this run is
// still in use by an environment that still exists, and removing it out from
// under one is how a working environment becomes a broken one.
func (e *Executor) accountForVolumes(candidates []Item, tornDown map[string]bool, outcome *Outcome) {
	for _, item := range candidates {
		if item.Category != ProjectVolume {
			continue
		}

		if !tornDown[item.ProjectName] {
			outcome.Skipped = append(outcome.Skipped, SkippedItem{item,
				"volumes are reclaimed via their project's teardown; its tree was not pruned in this run"})

			continue
		}

		outcome.FreedBytes += item.Size
		outcome.Deleted = append(outcome.Deleted, item)
	}
}

// removeSnapshots deletes materialised snapshots and their sidecars.
func (e *Executor) removeSnapshots(candidates []Item, outcome *Outcome) {
	for _, item := range candidates {
		if item.Category != Snapshot {
			continue
		}

		e.log("Removing materialized snapshot " + item.Path + " ...")

		// Accounted for only after a confirmed removal: reporting bytes as
		// reclaimed when the removal failed over-reports a destructive
		// operation, which is the wrong direction to be wrong in.
		if err := remove(item.Path); err != nil {
			outcome.Skipped = append(outcome.Skipped, SkippedItem{item,
				"the snapshot file could not be removed — check its permissions"})

			continue
		}

		// The sidecar is bookkeeping, so failing to remove it is worth saying
		// and not worth failing the snapshot over — the bytes the operator
		// asked to reclaim are already gone.
		if metaPath, ok := adapter.SnapshotMetaPathFor(item.Path); ok {
			if err := remove(metaPath); err != nil {
				e.log(fmt.Sprintf(
					"Snapshot removed, but its sidecar %s could not be removed.", metaPath,
				))
			}
		}

		outcome.FreedBytes += item.Size
		outcome.Deleted = append(outcome.Deleted, item)
	}
}

// resolveEnvironment maps a tree candidate back to the (module, core major)
// identity the adapter's teardown needs.
//
// The meta dotfile's attribution when there is one, and otherwise a registry
// lookup over the registered (module x core) pairs whose project name matches
// the directory. An unresolvable tree is never guessed at: teardown is
// destructive and names an environment, so guessing wrong would dispose of a
// different one than the operator was shown.
func (e *Executor) resolveEnvironment(item Item) (cockpit.Module, string, bool) {
	if item.Module != "" && item.CoreMajor != "" {
		module, registered := e.modules[item.Module]
		if !registered {
			// Attributed but unwatched: the registry is a watchlist, not a
			// gate, so an environment for a module nobody is watching is still
			// this tool's to tear down.
			module = cockpit.Module{
				Name:         item.Module,
				Project:      cockpit.ProjectFor(item.Module),
				CoreVersions: []string{item.CoreMajor},
				Watched:      false,
			}
		}

		return module, item.CoreMajor, true
	}

	if item.ProjectName != "" {
		// Iterated in whatever order the map gives, which is safe here and
		// nowhere else: engine project names are injective over machine names
		// — the only transformation is underscore to hyphen, and a hyphen is
		// not a legal machine name — so at most one entry can ever match and
		// the answer does not depend on the order. Sorting first was dead
		// weight, which mutation testing reported as such.
		for _, module := range e.modules {
			for _, coreMajor := range module.CoreVersions {
				name, err := adapter.EngineProjectName(module.Name, coreMajor)
				if err == nil && name == item.ProjectName {
					return module, coreMajor, true
				}
			}
		}
	}

	return cockpit.Module{}, "", false
}

// nameOrPath is how to refer to an environment in a progress line.
func nameOrPath(item Item) string {
	if item.ProjectName != "" {
		return item.ProjectName
	}

	return item.Path
}

// remove deletes a path, treating one that is already gone as removed: the
// caller wants it absent, and it is.
func remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

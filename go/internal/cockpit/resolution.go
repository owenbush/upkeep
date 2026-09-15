package cockpit

import (
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/owenbush/upkeep/internal/naming"
)

// ResolveModule is the module a command should act on, whether or not the cockpit
// watches it.
//
// The registry does two jobs and they were the same job: it says how to find a
// module on GitLab and which cores to test it on, *and* it says which modules
// the dashboard surveys. Only the second is what a maintainer means by
// curating one.
//
// The first job is now largely derivable. project/<name> is drupal.org's
// convention, and upkeep already assumed exactly that for anything it found
// outside the registry; the cores a run can use are the ones base artifacts
// exist for, which is a fact about the disk rather than about a config file. So
// a module nobody registered is workable, and the registry becomes the
// watchlist it was always being used as.
//
// What is *not* dropped is the refusal. "Module is not registered" was
// protecting configuration this can now derive, but it was also the only thing
// catching "pathuato", and quietly turning a typo into a clone of
// project/pathuato would make the tool worse rather than freer. So a name that
// cannot be a Drupal machine name is refused outright, and a derived name that
// turns out not to exist gets told what it nearly matched.
//
// A registry entry wins: a maintainer's core_versions is a deliberate
// statement about what they support and outranks anything inferred.
//
// coresOnDisk are the base-artifact versions, ascending as the artifact layout
// returns them.
func ResolveModule(registered map[string]Module, name string, coresOnDisk []string) (Module, error) {
	if module, found := registered[name]; found {
		return module, nil
	}

	if !naming.IsModuleName(name) {
		return Module{}, fmt.Errorf(
			"not a Drupal module machine name: %q. Expected lower-case letters, digits and underscores, "+
				"starting with a letter — e.g. \"pathauto\" or \"field_visibility_conditions\"", name,
		)
	}

	if len(coresOnDisk) == 0 {
		return Module{}, fmt.Errorf(
			"%q is not in the registry, so upkeep works out what to check it against — and there are no "+
				"base artifacts to check against yet.\nBuild one first:\n  upkeep base-artifacts:build "+
				"--version=11", name,
		)
	}

	// Newest first, because the first entry *is* the default: core_versions[0]
	// is what a run picks when --version is absent, and the artifact layout
	// sorts ascending. Left as it came off the disk, asking about an
	// unregistered module would silently answer for the oldest core built on
	// this machine — which is the least interesting question you could ask
	// about whether a module still works.
	newestFirst := slices.Clone(coresOnDisk)
	slices.Reverse(newestFirst)

	return Module{Name: name, Project: ProjectFor(name), CoreVersions: newestFirst, Watched: false}, nil
}

// ProjectFor is the drupal.org project path for a module machine name.
//
// The convention, and already relied on elsewhere in the tool. A module whose
// project path is *not* this is exactly what a registry entry is for.
func ProjectFor(name string) string { return "project/" + name }

// IsRegistered reports whether this module came from the registry rather than
// being derived.
func IsRegistered(registered map[string]Module, name string) bool {
	_, found := registered[name]

	return found
}

// ProjectFailure says why a module's GitLab project could not be resolved —
// and, when the name was derived rather than watched, what it probably should
// have been.
//
// One wording for both places that report this, because two sentences for one
// condition is how a tool comes to look like several tools. And the suggestion
// belongs *here*, at the failure, rather than at resolution time: until
// drupal.org says there is nothing at project/<name>, a name upkeep has never
// heard of is an ordinary thing to ask about, which is the entire point of the
// registry becoming a watchlist.
//
// A watched module that fails to resolve gets no suggestion. Its name is one
// the maintainer wrote down deliberately, so the problem is the API's answer,
// not the spelling.
func ProjectFailure(registered map[string]Module, module Module, apiMessage string) string {
	message := fmt.Sprintf(
		"Cannot resolve the GitLab project for module %q (%s): %s", module.Name, module.Project, apiMessage,
	)

	if IsRegistered(registered, module.Name) {
		return message
	}

	if meant, found := DidYouMean(registered, module.Name); found {
		return message + fmt.Sprintf("\nDid you mean %q?", meant)
	}

	return message
}

// DidYouMean is the registered name a failed lookup most likely meant, if any.
//
// Only consulted when the project could not be resolved, because until then a
// name upkeep has never heard of is an ordinary thing to ask about — that is
// the whole point of the change. It becomes a typo only once drupal.org says
// there is nothing there.
//
// Levenshtein rather than a prefix match: the mistakes that happen are
// transpositions and dropped letters ("pathuato", "pathaut"), which a prefix
// test misses entirely. The threshold scales with length so short names are
// not matched to everything.
func DidYouMean(registered map[string]Module, name string) (string, bool) {
	best := ""
	bestDistance := -1

	// Sorted, so two candidates at the same distance resolve the same way
	// every run rather than however the map happened to iterate.
	candidates := make([]string, 0, len(registered))
	for candidate := range registered {
		candidates = append(candidates, candidate)
	}
	slices.Sort(candidates)

	for _, candidate := range candidates {
		distance := levenshtein(name, candidate)
		if bestDistance < 0 || distance < bestDistance {
			bestDistance = distance
			best = candidate
		}
	}
	if bestDistance < 0 {
		return "", false
	}

	threshold := utf8.RuneCountInString(name) / 4
	if threshold < 2 {
		threshold = 2
	}

	return best, bestDistance <= threshold
}

// levenshtein is the edit distance between two strings, counted in bytes as
// PHP's own does, so the threshold means the same thing on both sides.
func levenshtein(a, b string) int {
	// One row of the matrix, since only the previous row is ever read.
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}

	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			substitution := previous[j-1]
			if a[i-1] != b[j-1] {
				substitution++
			}
			current[j] = min(min(previous[j]+1, current[j-1]+1), substitution)
		}
		previous, current = current, previous
	}

	return previous[len(b)]
}

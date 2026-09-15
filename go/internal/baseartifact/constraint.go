// Package baseartifact is the per-core base tree and clean-install dump: where
// it lives on disk, what it records about itself, and what constraint it is
// built from.
package baseartifact

import (
	"fmt"
	"slices"
	"strings"

	"github.com/owenbush/upkeep/internal/drupal"
)

// Stabilities are composer's stability names, loosest first.
//
// A stability is a *minimum*: ^12@alpha still prefers a stable 12 once one
// exists, so a cockpit built this way does not stay on the alpha after release
// — it stops being pre-release at the next rebuild.
var Stabilities = []string{"dev", "alpha", "beta", "RC", "stable"}

// ConstraintFor is the package constraint `composer create-project` is given.
//
// ^12 resolves to nothing while Drupal 12 is in alpha — packagist carries
// exactly one 12.x release, 12.0.0-alpha1 — so a build against it fails with
// composer's own "could not find a matching version" and no hint that anything
// could be done about it.
//
// That is not an edge case for this tool. A Drupal major spends months in
// alpha and beta, and **that is precisely when compatibility work happens**:
// the Project Update Bot compatibility merge requests the fast lane exists to
// merge are about the *unreleased* core. Being unable to build an environment
// for it means being unable to answer the question the tool is most often
// asked.
//
// Deliberate rather than automatic. Falling back to a pre-release when a
// stable constraint finds nothing would quietly build something different from
// what was asked for, and a base artifact set is the thing every later verdict
// is measured against — the one place a silent substitution is least
// acceptable. So the stability is a flag, and the resolved version is recorded
// in the meta either way: the core version comes from the lock, so an alpha
// build reads 12.0.0-alpha1 and cannot be mistaken for a release.
//
// An empty stability leaves the constraint bare.
func ConstraintFor(coreMajor, stability string) string {
	constraint := fmt.Sprintf("drupal/recommended-project:^%s", coreMajor)
	if stability == "" {
		return constraint
	}

	return constraint + "@" + stability
}

// AssertStability refuses a stability composer does not know.
func AssertStability(stability string) error {
	if stability == "" || slices.Contains(Stabilities, stability) {
		return nil
	}

	return fmt.Errorf(
		"unknown stability %q. Composer knows: %s", stability, strings.Join(Stabilities, ", "),
	)
}

// StabilityOf is the stability of a resolved core version, or "" when it is
// stable.
//
// The maintenance-free half. Building against a pre-release is a decision —
// hence the flag — but everything *downstream* of that decision should follow
// the tree that was actually built rather than ask again. The environment
// records the base artifact's resolved core version (e.g. 12.0.0-alpha1), so
// the toolchain constraint is derived from it: drupal/core-dev:^12 also
// resolves to nothing while 12 is in alpha, and would have failed the first
// check on an environment built with --stability=alpha.
//
// Composer's own parsing rules answer this, so there is no table of majors or
// suffixes to keep current — 13, 14 and anything after them work with no
// change here.
func StabilityOf(coreVersion string) string {
	if stability := drupal.Stability(coreVersion); stability != "stable" {
		return stability
	}

	return ""
}

// PackageFor is a toolchain package constraint carrying whatever stability
// that core version implies: drupal/core-dev:^12@alpha from 12.0.0-alpha1, and
// plain drupal/core-dev:^11 from a release.
//
// Only templates that interpolate the core major get the suffix. The stability
// belongs to *core's* constraint, and drupal/coder@alpha — with no version
// constraint at all — would tell composer that any alpha of a package
// unrelated to the seeded core is acceptable.
func PackageFor(packageTemplate, coreMajor, coreVersion string) string {
	if !strings.Contains(packageTemplate, "%s") {
		return packageTemplate
	}

	constraint := fmt.Sprintf(packageTemplate, coreMajor)
	if stability := StabilityOf(coreVersion); stability != "" {
		return constraint + "@" + stability
	}

	return constraint
}

// UnresolvableHint is what to add to a failed resolve, when the run asked for
// stable releases only.
//
// Empty once a stability *was* given: the constraint is then not the obvious
// suspect, and repeating the suggestion that was already taken would bury
// whatever composer actually said.
func UnresolvableHint(coreMajor, stability string) string {
	if stability != "" {
		return ""
	}

	return fmt.Sprintf(
		"\nIf core %s has no stable release yet, there is nothing for \"^%s\" to resolve to. A Drupal major "+
			"spends months in alpha and beta, which is when compatibility work happens — to build against the "+
			"pre-release:\n  upkeep base-artifacts:build --version=%s --stability=alpha",
		coreMajor, coreMajor, coreMajor,
	)
}

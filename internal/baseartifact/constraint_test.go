package baseartifact

import (
	"slices"
	"strings"
	"testing"
)

// ^12 resolves to nothing while Drupal 12 is in alpha, and a Drupal major
// spends months there — which is exactly when compatibility work happens.
func TestTheStabilitySuffixIsOptedIntoNotDefaulted(t *testing.T) {
	if got := ConstraintFor("12", ""); got != "drupal/recommended-project:^12" {
		t.Errorf("got %q — a stability was added nobody asked for", got)
	}
	if got := ConstraintFor("12", "alpha"); got != "drupal/recommended-project:^12@alpha" {
		t.Errorf("got %q", got)
	}
}

func TestOnlyComposersOwnStabilitiesAreAccepted(t *testing.T) {
	for _, stability := range []string{"", "dev", "alpha", "beta", "RC", "stable"} {
		if err := AssertStability(stability); err != nil {
			t.Errorf("%q was refused: %v", stability, err)
		}
	}

	for _, stability := range []string{"rc", "ALPHA", "prerelease", "nightly", "12"} {
		err := AssertStability(stability)
		if err == nil {
			t.Errorf("%q was accepted", stability)

			continue
		}
		if !strings.Contains(err.Error(), "alpha") {
			t.Errorf("the refusal for %q does not list what composer knows: %v", stability, err)
		}
	}
}

// The downstream half is derived from the tree that was actually built, so 13,
// 14 and anything after them need no change here.
func TestTheStabilityIsDerivedFromTheResolvedVersion(t *testing.T) {
	for version, want := range map[string]string{
		"12.0.0-alpha1": "alpha",
		"13.0.0-beta2":  "beta",
		"12.0.0-rc1":    "RC",
		"12.x-dev":      "dev",
		"11.4.6":        "",
		"1.0.2":         "",
	} {
		if got := StabilityOf(version); got != want {
			t.Errorf("%s: got %q, want %q", version, got, want)
		}
	}
}

// drupal/core-dev:^12 resolves to nothing while 12 is in alpha, and would have
// failed the first check on an environment built with --stability=alpha.
func TestAToolchainPackageInheritsTheSeededCoresStability(t *testing.T) {
	if got := PackageFor("drupal/core-dev:^%s", "12", "12.0.0-alpha1"); got != "drupal/core-dev:^12@alpha" {
		t.Errorf("got %q", got)
	}
	if got := PackageFor("drupal/core-dev:^%s", "11", "11.4.6"); got != "drupal/core-dev:^11" {
		t.Errorf("got %q", got)
	}
}

// drupal/coder@alpha carries no version constraint at all, and would tell
// composer that any alpha of a package unrelated to the seeded core is
// acceptable.
func TestAPackageWithNoVersionConstraintNeverGetsAStability(t *testing.T) {
	for _, pkg := range []string{"drupal/coder", "phpstan/phpstan", "drupal/upgrade_status"} {
		if got := PackageFor(pkg, "12", "12.0.0-alpha1"); got != pkg {
			t.Errorf("got %q, want %q untouched", got, pkg)
		}
	}
}

// Falling back to a pre-release when a stable constraint finds nothing would
// quietly build something different from what was asked for — so the hint is
// advice, and the build still fails.
func TestTheHintIsOfferedOnlyWhenStabilityWasNotAlreadyGiven(t *testing.T) {
	hint := UnresolvableHint("12", "")
	if !strings.Contains(hint, "--stability=alpha") {
		t.Errorf("hint %q does not name the flag", hint)
	}
	if !strings.Contains(hint, "--version=12") {
		t.Errorf("hint %q does not name the core", hint)
	}

	// Once a stability was given the constraint is not the obvious suspect,
	// and repeating advice already taken buries what composer said.
	if got := UnresolvableHint("12", "alpha"); got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}

// Loosest first, because a stability is a minimum.
func TestTheStabilitiesAreOrderedLoosestFirst(t *testing.T) {
	if !slices.Equal(Stabilities, []string{"dev", "alpha", "beta", "RC", "stable"}) {
		t.Errorf("got %v", Stabilities)
	}
}

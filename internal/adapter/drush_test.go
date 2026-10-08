package adapter

import (
	"strings"
	"testing"
)

// Nothing asked for is the bare package, which is what every released core
// wants: composer picks the newest release that fits the tree.
func TestNoConstraintIsTheBarePackage(t *testing.T) {
	for _, nothing := range []string{"", "   ", "\t"} {
		if got := ToolRequire(nothing); got != DrushPackage {
			t.Errorf("ToolRequire(%q) = %q, want %q", nothing, got, DrushPackage)
		}
	}
}

// A constraint becomes composer's package:constraint, trimmed — a flag value
// with a stray space would otherwise reach the container as two arguments'
// worth of nonsense in one.
func TestAConstraintBecomesAPackageConstraint(t *testing.T) {
	if got := ToolRequire("^13@dev"); got != "drush/drush:^13@dev" {
		t.Errorf("got %q", got)
	}
	if got := ToolRequire("  ^13@dev  "); got != "drush/drush:^13@dev" {
		t.Errorf("untrimmed: %q", got)
	}
}

// The hint fires only where it is the likely diagnosis: a pre-release core,
// and nobody having named a constraint yet.
func TestTheToolchainHintFiresOnlyForAnUnconstrainedPreRelease(t *testing.T) {
	hint := ToolRequireHint("12.0.0-beta1", "")
	if hint == "" {
		t.Fatal("a pre-release core with no constraint got no hint")
	}
	// It must name the flag and carry a runnable line: the whole point is that
	// composer's forty lines never mention there is a flag.
	for _, want := range []string{
		"--" + ToolRequireFlag + "='^13@dev'", // pasteable, with a branch that works
		"--version=12",                        // the core this run was about
		"--stability=beta",                    // and the stability it was built at
		DrushPackage,
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("%q is missing from the hint:%s", want, hint)
		}
	}

	// A released core: the toolchain resolves, so a failure is something else
	// and this advice would be a guess.
	if got := ToolRequireHint("11.4.8", ""); got != "" {
		t.Errorf("a released core got a hint:%s", got)
	}
	// A constraint was already given: repeating the suggestion that was taken
	// buries whatever composer actually said. Same rule as UnresolvableHint.
	if got := ToolRequireHint("12.0.0-beta1", "^13@dev"); got != "" {
		t.Errorf("an answered run got the hint again:%s", got)
	}
}

// The hint echoes the run's own core and stability rather than a fixed
// example, so the line it prints is the line to run — including for the cores
// that do not exist yet.
func TestTheHintEchoesTheRunItCameFrom(t *testing.T) {
	for version, want := range map[string][]string{
		"12.0.0-beta1":  {"--version=12", "--stability=beta"},
		"13.0.0-alpha3": {"--version=13", "--stability=alpha"},
		"14.0.0-rc1":    {"--version=14", "--stability=RC"},
	} {
		hint := ToolRequireHint(version, "")
		for _, fragment := range want {
			if !strings.Contains(hint, fragment) {
				t.Errorf("%s: %q missing:%s", version, fragment, hint)
			}
		}
	}
}

// The flag's name lives here with the rest of the engine's knowledge, and the
// help text names the package it selects — a command cannot say either.
func TestTheFlagNamesTheToolItSelects(t *testing.T) {
	if ToolRequireFlag == "" {
		t.Fatal("the flag has no name")
	}
	if !strings.Contains(ToolRequireFlagHelp, DrushPackage) {
		t.Errorf("the help does not name the package: %q", ToolRequireFlagHelp)
	}
}

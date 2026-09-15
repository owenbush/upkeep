package adapter

import (
	"os"
	"strings"
)

// Identity of the ddev-upkeep add-on: the fixture commands the adapter shells
// to for loading a fixture. Installed into every environment during
// provisioning, and lazily on first use for environments provisioned before
// the add-on became part of the layout.
//
// The command name is declared once, here, and both the invocation and the
// installed-probe are built from it. They used to be two strings and they
// disagreed: the adapter ran `ddev upkeep-fixture-load` while the add-on
// published `ddev fixture-load`, so --fixture could not have worked and the
// probe looked for a file that is never installed, re-fetching the add-on on
// every single call. One fact, two strings, two repositories, nothing
// comparing them.
//
// The name it settled on is the namespaced one, because ddev gives every
// add-on's host commands one flat namespace per project and "fixture-load"
// claims ground this add-on has no business claiming.
//
// **The release is pinned**, exactly as the engine add-on is. A published
// add-on with no pin means every environment silently tracks whatever its
// latest release happens to be, so a breaking change over there — the command
// rename above would have been one — arrives in every environment with no
// warning and nothing that could have caught it.
const (
	// FixtureAddOnName is the published add-on source, as `ddev add-on get`
	// accepts it.
	FixtureAddOnName = "owenbush/ddev-upkeep"

	// FixtureAddOnVersion is the pinned release. Bumping it re-installs the
	// add-on in every existing environment on next use, because the stamp
	// below stops matching.
	FixtureAddOnVersion = "1.0.0"

	// FixtureAddOnStamp is where the installed version is recorded, under the
	// directory the add-on owns.
	//
	// upkeep's own file rather than ddev's add-on metadata: the location and
	// shape of that metadata is a ddev implementation detail that has moved
	// before, and a probe that silently stops finding it would read as "not
	// installed" and reinstall on every call — which is the bug this has
	// already had once, from the other direction.
	FixtureAddOnStamp = "upkeep/.upkeep-addon-version"

	// FixtureAddOnSourceEnv overrides the add-on source — a local checkout
	// path during add-on development.
	FixtureAddOnSourceEnv = "UPKEEP_ADDON_SOURCE"

	// FixtureLoadCommand is the host command the add-on publishes for loading
	// a fixture, as it is invoked and as it is named on disk.
	FixtureLoadCommand = "upkeep-fixture-load"

	// FixtureAddOnMarker is a file the add-on installs into <project>/.ddev/ —
	// its presence is the "already installed" probe.
	//
	// Derived from the command name: ddev names a host command after the file
	// it came from, so the probe and the invocation cannot drift apart.
	FixtureAddOnMarker = "commands/host/" + FixtureLoadCommand
)

// FixtureAddOnSource is where the add-on comes from for this run.
func FixtureAddOnSource() string {
	if override := os.Getenv(FixtureAddOnSourceEnv); override != "" {
		return override
	}

	return FixtureAddOnName
}

// FixtureAddOnIsOverridden reports whether the add-on comes from somewhere
// other than the published release.
func FixtureAddOnIsOverridden() bool { return FixtureAddOnSource() != FixtureAddOnName }

// FixtureAddOnInstallArguments are the `ddev add-on get` arguments.
//
// --version only for the published add-on: an override is a local checkout or
// an arbitrary source, where a release tag means nothing and naming one is an
// error rather than a constraint.
func FixtureAddOnInstallArguments() []string {
	if FixtureAddOnIsOverridden() {
		return []string{FixtureAddOnSource()}
	}

	return []string{FixtureAddOnName, "--version", FixtureAddOnVersion}
}

// FixtureAddOnExpectedStamp is what an environment should have recorded once
// this add-on is installed.
//
// An override stamps its source, so moving between a local checkout and the
// published release re-installs rather than trusting whichever landed first —
// developing the two repositories together is exactly when a stale add-on is
// hardest to notice.
func FixtureAddOnExpectedStamp() string {
	if FixtureAddOnIsOverridden() {
		return "source:" + FixtureAddOnSource()
	}

	return FixtureAddOnVersion
}

// FixtureAddOnMarkerMatchesCommand is the invariant the two constants exist to
// keep: the probe looks for the file the invocation names.
//
// Checked rather than assumed, because the two disagreeing is precisely the
// bug that made --fixture unable to work while every unit test stayed green.
func FixtureAddOnMarkerMatchesCommand() bool {
	return strings.HasSuffix(FixtureAddOnMarker, "/"+FixtureLoadCommand)
}

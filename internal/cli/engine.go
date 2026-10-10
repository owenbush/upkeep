package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/cockpit"
)

// Engine builds the environment engine for this invocation, with its progress
// wired to where the command wants it.
//
// The one place a command gets an engine. It never names or constructs one:
// the factory arrives from the composition root, which is what keeps engine
// selection to a single place and the boundary check meaningful.
//
// Stage lines — upkeep's own "provisioning…", "running check…" — go to stderr
// with the rest of the diagnostics. The child's own output goes through the
// live status line, which is nothing at all when stderr is not a terminal.
//
// A deliberate divergence from the PHP, which sends stage lines to stdout on
// the commands whose payload is a table. Those lines are progress, not
// results: `upkeep check pathauto 12 > verdict.txt` should leave the
// provisioning transcript on the terminal and put the verdict in the file, and
// on the PHP side it does not. Everything a command *answers* with still goes
// to stdout.
func Engine(cmd *cobra.Command, engines adapter.Factory, where *cockpit.Cockpit) (adapter.Engine, error) {
	status := NewLiveStatus(cmd.ErrOrStderr(), IsTerminal(cmd.ErrOrStderr()), Verbose(cmd))

	return engines.Build(
		where,
		Flag(cmd, FlagProjectsRoot),
		func(line string) {
			// Cleared first, so a stage line never lands on top of whatever
			// the last child was saying.
			status.Clear()
			fmt.Fprintln(cmd.ErrOrStderr(), line)
		},
		status.Line,
		status.Clear,
	)
}

// QuietEngine is an engine that reports nothing at all.
//
// For the two commands whose stdout is somebody else's: `env:path` prints a
// path meant to be captured with `cd $(upkeep env:path …)`, and `exec` passes
// through the output of your own command. A status line in either would end up
// inside the thing you asked for — and these two only ever look a path up,
// which prints nothing worth watching anyway.
func QuietEngine(
	cmd *cobra.Command, engines adapter.Factory, where *cockpit.Cockpit,
) (adapter.Engine, error) {
	return engines.Build(where, Flag(cmd, FlagProjectsRoot), nil, nil, nil)
}

// AssertProjectsRoot refuses a projects root that could never work, before
// anything slow happens.
//
// The engine factory refuses it too, but only once a command reaches for an
// engine — which on the patch commands is after a drupal.org round trip and a
// file download. A configuration mistake the run can never recover from should
// cost nothing, so it is checked as soon as the cockpit is known.
func AssertProjectsRoot(cmd *cobra.Command, where *cockpit.Cockpit) error {
	_, err := adapter.ResolveProjectsRoot(Flag(cmd, FlagProjectsRoot), where.Root)

	return err
}

// FlagVerbose asks for every line of a child's output rather than the latest.
const FlagVerbose = "verbose"

// AddVerbose adds -v.
func AddVerbose(cmd *cobra.Command) {
	cmd.Flags().BoolP(FlagVerbose, "v", false,
		"Show every line of the engine's own output rather than the latest")
}

// Verbose reports whether -v was given.
func Verbose(cmd *cobra.Command) bool { return Switched(cmd, FlagVerbose) }

// RequireEnvironment is the provisioned environment's path for a (module,
// core) pair, refusing when there is none.
//
// Refused rather than provisioned: the commands that use this are the ones
// that look at an environment rather than make one, and quietly spending ten
// minutes building a site because somebody typed a path command would be a
// surprise nobody asked for. The refusal names the commands that do build one.
func RequireEnvironment(engine adapter.Engine, moduleName, coreMajor string) (string, error) {
	path := engine.ResolveEnvPath(moduleName, coreMajor)
	if path == "" {
		return "", noEnvironment(moduleName, coreMajor)
	}

	return path, nil
}

// ArtifactBuilder builds the base-artifact builder for this invocation, with
// its progress wired where the command wants it.
//
// The scratch directory is held to the same $HOME containment rule as the
// projects root, and for the same functional reason: the throwaway install
// project is bind-mounted into the Docker VM, and macOS providers share only
// the home directory — a scratch directory outside it produces an install that
// cannot start, with an error about the container rather than about the path.
func ArtifactBuilder(
	cmd *cobra.Command, engines adapter.Factory, where *cockpit.Cockpit,
) (*baseartifact.Builder, error) {
	scratchDir, err := adapter.RequireUnderHome(
		Flag(cmd, FlagScratchDir), "scratch directory", "--scratch-dir")
	if err != nil {
		return nil, err
	}

	status := NewLiveStatus(cmd.ErrOrStderr(), IsTerminal(cmd.ErrOrStderr()), Verbose(cmd))

	return engines.BuildArtifacts(
		where,
		scratchDir,
		func(line string) {
			status.Clear()
			fmt.Fprintln(cmd.ErrOrStderr(), line)
		},
		status.Line,
		status.Clear,
	), nil
}

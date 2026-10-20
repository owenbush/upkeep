package command

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
)

// The gate on the commands that act in public under the maintainer's name.
//
// `merge` has refused without a terminal since it was written: no terminal
// means no explicit per-merge-request affirmative is possible, and without one
// nothing merges. That is what makes the one-approval stance structural rather
// than a note in a guide.
//
// `publish` and `needs-work` had no such property, and they are the other two
// commands whose effects land on somebody else's project — a merge request
// opened under your name, a comment a contributor will read and act on. A
// script, a pipe or an agent could reach either, and the mistake they make is
// not a broken build but a wrong thing said to a stranger in public.
//
// **So: a terminal, or `--yes`.** Not a hard refusal like `merge`'s, because
// deliberate scripted publishing is a legitimate thing somebody may want and
// `--yes` is already this tool's word for "I meant this" (`prune`). The
// difference between the two is the point: a merge consumes an approval that
// only a human can give, while a publish is an action a human can authorise in
// advance.
//
// `merge` keeps its own refusal. Giving it a `--yes` would weaken the stance
// DA policy depends on.

// addWriteConfirmation gives a command the --yes flag the gate below reads.
func addWriteConfirmation(cmd *cobra.Command, what string) {
	cmd.Flags().BoolP("yes", "y", false,
		"Go ahead without a terminal to confirm at — "+what+" is deliberate")
}

// assertDeliberateWrite refuses an outward-facing write nobody is present to
// confirm.
//
// what completes "…, and there is no terminal here to confirm at", so it reads
// as a sentence about the specific thing about to happen rather than a generic
// warning somebody has to go and look up.
func assertDeliberateWrite(cmd *cobra.Command, what string) error {
	if cli.Switched(cmd, "yes") || cli.InteractiveInput(cmd) {
		return nil
	}

	return fmt.Errorf(
		"%s, and there is no terminal here to confirm at. Re-run with --yes if that is "+
			"what you meant", what)
}

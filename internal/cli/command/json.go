package command

import (
	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/report"
)

// emitJSON wraps a payload in the envelope and writes it to stdout.
//
// The envelope is built here rather than in internal/cli because this is where
// the version lives: ldflags set `internal/cli/command.Version`, a path named
// in .goreleaser.yaml and verified against a real signed release, and
// internal/cli cannot import this package. So internal/cli owns "one JSON
// document on stdout" and this owns what goes in it.
//
// It returns only an error, never an exit code. The caller decides that, which
// matters for `check`: a failed suite emits its payload *and* exits 1, and a
// helper that returned OK would quietly turn every red check green.
func emitJSON(cmd *cobra.Command, name string, data any, warnings []string) error {
	return cli.EmitDocument(cmd, report.NewEnvelope(Version, name, data, warnings))
}

// emitCheckJSON is the structured half of renderCheckSummary.
//
// Both paths in `check` and the one in `patch:check` go through here, so a
// verdict reads the same however the run was reached. The caller still returns
// workflow.ForRun(run) afterwards: the document says what failed and the exit
// code says that something did, and an agent should be able to act on the
// code alone.
func emitCheckJSON(
	cmd *cobra.Command, run check.RunResult, subject report.Subject,
) error {
	return emitJSON(cmd, "check",
		report.CheckRun(run, subject, cli.Flag(cmd, "fixture")), nil)
}

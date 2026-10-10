package command

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/maintenance"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewBaseArtifactsStatus builds the base-artifacts:status command.
//
// Colon-named rather than a subcommand group, which is what this framework
// would reach for. The names are the PHP's, and they are in scripts, in the
// docs, and in the recovery lines other commands print — `upkeep
// base-artifacts:build --version=11` is what a failed provision tells you to
// run. A rewrite that quietly renamed them would be a rewrite somebody has to
// migrate to.
func NewBaseArtifactsStatus() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "base-artifacts:status",
		Short: "List which core versions have base artifacts, with build dates and sizes",
		Args:  cobra.NoArgs,
	}
	cli.AddCockpit(cmd)
	cmd.RunE = cli.Run(runBaseArtifactsStatus)

	return cmd
}

func runBaseArtifactsStatus(cmd *cobra.Command, _ []string) (int, error) {
	where, err := cli.RequireCockpit(cmd)
	if err != nil {
		return 0, err
	}

	records, err := baseartifact.NewScanner(
		baseartifact.NewLayout(where.BaseArtifactsPath()),
	).Scan()
	if err != nil {
		return 0, err
	}

	if len(records) == 0 {
		cli.Printf(cmd,
			"No base artifacts built yet under %s. Run `upkeep base-artifacts:build --version=N`.\n",
			where.BaseArtifactsPath())

		return workflow.OK, nil
	}

	rows := make([][]string, 0, len(records))
	for _, record := range records {
		rows = append(rows, []string{
			record.Version,
			artifactState(record),
			metaField(record, func(meta baseartifact.Meta) string { return meta.CoreVersion }),
			metaField(record, func(meta baseartifact.Meta) string {
				return meta.BuiltAt.Format("2006-01-02 15:04:05 MST")
			}),
			metaField(record, func(meta baseartifact.Meta) string { return meta.PHPVersion }),
			metaField(record, func(meta baseartifact.Meta) string { return meta.DBEngine }),
			maintenance.HumanBytes(record.TreeBytes),
			maintenance.HumanBytes(record.DumpBytes),
		})
	}

	cli.Table{
		Headers: []string{
			"Core", "State", "Exact core", "Built", "PHP", "DB engine", "Tree size", "Dump size",
		},
		Rows: rows,
	}.Render(cmd.OutOrStdout())

	return workflow.OK, nil
}

// artifactState says whether a set can be used, and when it cannot, what is
// missing from it.
//
// Naming the missing pieces rather than saying "incomplete": the recovery
// differs — a missing dump is a rebuild, a missing canonical marker is a file
// to touch — and an operator staring at a broken set needs to know which.
func artifactState(record baseartifact.Record) string {
	if record.Complete {
		return "complete (canonical)"
	}

	return "incomplete: missing " + strings.Join(record.Missing, ", ")
}

// metaField is a fact from the set's meta, or "-" when there is no readable
// meta to take it from.
//
// A dash rather than a blank: an incomplete set still occupies a row, and an
// empty cell reads as a field somebody forgot rather than one that cannot be
// known.
func metaField(record baseartifact.Record, read func(baseartifact.Meta) string) string {
	if record.Meta == nil {
		return "-"
	}
	if value := read(*record.Meta); value != "" {
		return value
	}

	return "-"
}

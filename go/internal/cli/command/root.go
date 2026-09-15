package command

import (
	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
)

// Version is the application version, stamped at build time.
var Version = "dev"

// NewRoot assembles the command tree.
//
// Every command that needs an environment is handed the engine factory here
// and never constructs one itself. This function and the factory it is given
// are the only places engine selection happens.
func NewRoot(engines adapter.Factory) *cobra.Command {
	root := &cobra.Command{
		Use:     "upkeep",
		Short:   "Maintenance orchestrator for contributed Drupal modules",
		Version: Version,
		// Nothing to do with no arguments but show what there is.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	// `--version` is the *target core selector* on every command that takes
	// it, so the application-level one is removed rather than left to collide.
	// The number is still reachable, as `upkeep version`, because a bug report
	// needs it.
	root.SetVersionTemplate("{{.Version}}\n")
	root.Flags().Bool("version", false, "")
	_ = root.Flags().MarkHidden("version")

	root.AddCommand(
		NewInit(),
		NewModules(),
		NewVersion(),
	)

	return root
}

// NewVersion prints the application version.
//
// A command rather than a root flag, because `--version` is spoken for: on
// check, review, dev and the rest it selects the target core major, and one
// spelling cannot mean both.
func NewVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the upkeep version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println(Version)
		},
	}
}

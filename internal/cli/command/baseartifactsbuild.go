package command

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewBaseArtifactsBuild builds the base-artifacts:build command.
//
// Produces the canonical per-core artifact set: a resolved base tree and a
// clean-install database dump. Every environment for that core is seeded from
// it, which is what makes a check of one module comparable with a check of
// another — and what makes a rebuild something to be careful with.
func NewBaseArtifactsBuild(engines adapter.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use: "base-artifacts:build",
		Short: "Build the canonical per-core base artifacts: " +
			"resolved base tree and clean-install DB dump",
		Long: `Builds the artifact set every environment for a core is seeded from.

  upkeep base-artifacts:build --version=11
  upkeep base-artifacts:build --version=12 --stability=alpha
  upkeep base-artifacts:build --version=11 --force

A rebuild is staged beside the live set and swapped in at the end, so a
build that fails costs the attempt and leaves the existing set untouched.`,
		Args: cobra.NoArgs,
	}
	// --version, like every other core-version selector. The application's own
	// -V/--version is removed at the root rather than left to collide.
	cmd.Flags().String(cli.FlagVersion, "",
		"Drupal core major version to build artifacts for (e.g. 11)")
	cmd.Flags().Bool("force", false, "Deliberately rebuild over an existing artifact set")
	cmd.Flags().String("stability", "", fmt.Sprintf(
		"Lowest release stability to accept (%s). Needed while a core major is still in "+
			"alpha or beta, which is when compatibility work happens",
		strings.Join(baseartifact.Stabilities, ", ")))
	cli.AddScratchDir(cmd)
	cli.AddCockpit(cmd)
	cli.AddVerbose(cmd)
	registerStabilityCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, _ []string) (int, error) {
		return runBaseArtifactsBuild(cmd, engines)
	})

	return cmd
}

func runBaseArtifactsBuild(cmd *cobra.Command, engines adapter.Factory) (int, error) {
	// The strict cockpit: this writes the artifact set the whole tool measures
	// against, so a cockpit that merely looks like one is not enough.
	where, err := cli.RequireCockpit(cmd)
	if err != nil {
		return 0, err
	}

	coreMajor := cli.Flag(cmd, cli.FlagVersion)
	if coreMajor == "" {
		return 0, fmt.Errorf("the --version option is required (e.g. --version=11)")
	}

	// Resolved before the build rather than after it, because the report needs
	// both paths and asking for them afterwards produces two error branches
	// that a successful build has already made unreachable.
	//
	// The refusal it can produce is the build's too — Build validates the core
	// major itself, and has to, being reachable from elsewhere — so this
	// branch is about where the paths come from, not about catching anything
	// the build would miss.
	paths, err := baseartifact.NewLayout(where.BaseArtifactsPath()).PathsFor(coreMajor)
	if err != nil {
		return 0, err
	}

	builder, err := cli.ArtifactBuilder(cmd, engines, where)
	if err != nil {
		return 0, err
	}

	// --stability is a flag and never a fallback. Substituting a pre-release
	// when a stable constraint resolves nothing would make every later verdict
	// a statement about a tree nobody asked for, and a base artifact set is
	// the one place a silent substitution is least acceptable.
	meta, err := builder.Build(coreMajor, cli.Switched(cmd, "force"), cli.Flag(cmd, "stability"))
	if err != nil {
		return 0, err
	}

	cli.Printf(cmd,
		"Base artifacts for Drupal %s built: core %s, PHP %s, DB %s.\nTree: %s\nDump: %s\n",
		meta.CoreMajor, meta.CoreVersion, meta.PHPVersion, meta.DBEngine, paths.Tree, paths.Dump)

	return workflow.OK, nil
}

// registerStabilityCompletion suggests composer's stability names — a closed
// set upkeep already holds, so completing it costs nothing.
func registerStabilityCompletion(cmd *cobra.Command) {
	_ = cmd.RegisterFlagCompletionFunc("stability",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return baseartifact.Stabilities, cobra.ShellCompDirectiveNoFileComp
		})
}

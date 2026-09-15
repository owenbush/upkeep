package command

import (
	"net/http"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/maintenance"
)

// Version is the application version, stamped at build time.
var Version = "dev"

// Surface is every seam the command tree reaches the world through: the
// engine, the two APIs, the operator's terminal and browser, the disk.
//
// A struct rather than a parameter list. Each command takes only the seams it
// uses — that is what makes them testable — but the *root* takes all of them,
// and as a positional list it had reached the length where adding one meant
// editing a dozen call sites that each said nothing about which argument was
// which. `PatchSurface` is the subset the patch commands share.
type Surface struct {
	Engines    adapter.Factory
	Clients    cli.GitlabClients
	Issues     IssueClients
	Prompts    func(*cobra.Command) cli.Prompt
	Volumes    Volumes
	Sizer      maintenance.Sizer
	Downloader *http.Client
	Browser    cli.Browser
}

// NewRoot assembles the command tree.
//
// Every command that needs an environment is handed the engine factory here
// and never constructs one itself. This function and the factory it is given
// are the only places engine selection happens.
func NewRoot(surface Surface) *cobra.Command {
	engines, clients, issues := surface.Engines, surface.Clients, surface.Issues
	volumes, sizer := surface.Volumes, surface.Sizer

	patchSurface := PatchSurface{
		Engines: engines, Clients: clients, Issues: issues,
		Prompts: surface.Prompts, Downloader: surface.Downloader,
	}

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
		NewBaseArtifactsStatus(),
		NewCheck(engines, clients),
		NewDashboard(clients, issues),
		NewDev(engines),
		NewEnvPath(engines),
		NewExec(engines),
		NewExplain(),
		NewInit(),
		NewIssue(clients, issues, surface.Browser),
		NewIssues(clients, issues),
		NewMerge(clients, surface.Prompts),
		NewModules(),
		NewNeedsWork(clients, surface.Browser),
		NewPatchApply(patchSurface),
		NewPatchCheck(patchSurface),
		NewPatchPromote(patchSurface),
		NewPatches(clients, issues),
		NewPrune(engines, volumes, sizer),
		NewPublish(engines, clients, issues),
		NewReview(engines, clients),
		NewStart(engines, issues),
		NewStatus(volumes, sizer),
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

package command

import (
	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewReview builds the review command.
//
// Puts a merge request onto a browsable site: resolve the context, ensure the
// (module x core) environment, apply the merge request, serve — then print the
// site URL, and the one-time login URL when the engine can mint one.
//
// Exit codes: 0 the site is up with the merge request applied, 2 upkeep could
// not do it. 1 is reserved for red checks and never returned here, because
// review runs none.
func NewReview(engines adapter.Factory, clients cli.GitlabClients) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review <module> <mr>",
		Short: "Apply a merge request to a running site and print its browsable URL",
		Long: "Apply a merge request to a running site and print its browsable URL.\n\n" +
			"Runs no checks — this is for looking at the change in a browser. " +
			"Use `upkeep check` for a verdict.",
		Args: cobra.ExactArgs(2),
	}
	addMrSurface(cmd)
	cli.AddModuleCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runReview(cmd, engines, clients, args[0], args[1])
	})

	return cmd
}

func runReview(
	cmd *cobra.Command, engines adapter.Factory, clients cli.GitlabClients,
	moduleName, rawIID string,
) (int, error) {
	where, context, err := resolveMrContext(cmd, clients, moduleName, rawIID)
	if err != nil {
		return 0, err
	}
	describeContext(cmd, context, cli.Flag(cmd, cli.FlagVersion))

	engine, err := cli.Engine(cmd, engines, where)
	if err != nil {
		return 0, err
	}

	environment, err := ensureEnvironment(cmd, engine, context.Module, context.CoreMajor)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Merge request")
	if err := engine.ApplyMr(environment, context.MergeRequest); err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Serve")
	serve, err := engine.Serve(environment)
	if err != nil {
		return 0, err
	}

	cli.Printf(cmd, "MR !%d (%q) is live for review on Drupal core %s.\n",
		context.MergeRequest.IID, context.MergeRequest.Title, context.CoreMajor)
	cli.Printf(cmd, "  Site URL:   %s\n", serve.URL)
	if serve.LoginURL != "" {
		cli.Printf(cmd, "  Login URL:  %s  (one-time)\n", serve.LoginURL)
	}
	cli.Println(cmd, "")

	return workflow.OK, nil
}

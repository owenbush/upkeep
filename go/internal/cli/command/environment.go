package command

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/proc"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewEnvPath builds the env:path command.
func NewEnvPath(engines adapter.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env:path <module>",
		Short: "Print the absolute path of a module's environment directory",
		Long: "Print the absolute path of a module's environment directory.\n\n" +
			"stdout carries the path and nothing else, so `cd $(upkeep env:path pathauto)` works.",
		Args: cobra.ExactArgs(1),
	}
	addEnvironmentFlags(cmd)
	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		path, err := locateEnvironment(cmd, engines, args[0])
		if err != nil {
			return 0, err
		}
		cli.Println(cmd, path)

		return workflow.OK, nil
	})

	return cmd
}

// NewExec builds the exec command.
func NewExec(engines adapter.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <module> -- <command>...",
		Short: "Run a command in a module's environment directory",
		Long: "Run a command in a module's environment directory.\n\n" +
			"Exit codes follow the CLI-wide contract rather than the child's raw code: 0 the command " +
			"succeeded, 1 it exited non-zero, 2 upkeep could not run it — an unresolvable module, an " +
			"untracked core, no provisioned environment, or a child that never started.\n\n" +
			"stdout belongs to the wrapped command; upkeep's own words go to stderr.",
		Args: cobra.MinimumNArgs(2),
	}
	addEnvironmentFlags(cmd)
	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runExec(cmd, engines, args[0], args[1:])
	})

	return cmd
}

// NewDev builds the dev command.
func NewDev(engines adapter.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dev <module>",
		Short: "Prepare an environment for active development",
		Long: "Prepare an environment for active development: provision it if needed, optionally " +
			"check out a branch, and print where it is.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().String("branch", "", "Branch to check out in the module working copy")
	addEnvironmentFlags(cmd)
	cli.AddVerbose(cmd)
	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runDev(cmd, engines, args[0])
	})

	return cmd
}

// addEnvironmentFlags is the surface every environment command shares.
func addEnvironmentFlags(cmd *cobra.Command) {
	cli.AddTargetCore(cmd)
	cli.AddCockpit(cmd)
	cli.AddProjectsRoot(cmd)
	cli.AddModuleCompletion(cmd)
}

// resolveSubject is the module and core an environment command acts on.
func resolveSubject(
	cmd *cobra.Command, name string,
) (where *cockpit.Cockpit, module cockpit.Module, coreMajor string, err error) {
	where, err = cli.Cockpit(cmd)
	if err != nil {
		return nil, cockpit.Module{}, "", err
	}
	if err := cli.AssertProjectsRoot(cmd, where); err != nil {
		return nil, cockpit.Module{}, "", err
	}

	modules, err := cli.Modules(where)
	if err != nil {
		return nil, cockpit.Module{}, "", err
	}

	module, err = cli.ResolveModule(where, modules, name)
	if err != nil {
		return nil, cockpit.Module{}, "", err
	}

	coreMajor, err = cli.TargetCore(cmd, module)
	if err != nil {
		return nil, cockpit.Module{}, "", err
	}

	return where, module, coreMajor, nil
}

// locateEnvironment is the path of an existing environment, for the two
// commands that look at one rather than make one.
func locateEnvironment(cmd *cobra.Command, engines adapter.Factory, name string) (string, error) {
	where, module, coreMajor, err := resolveSubject(cmd, name)
	if err != nil {
		return "", err
	}

	// Quiet, because both callers' stdout is somebody else's.
	engine, err := cli.QuietEngine(cmd, engines, where)
	if err != nil {
		return "", err
	}

	return cli.RequireEnvironment(engine, module.Name, coreMajor)
}

func runExec(
	cmd *cobra.Command, engines adapter.Factory, name string, command []string,
) (int, error) {
	path, err := locateEnvironment(cmd, engines, name)
	if err != nil {
		return 0, err
	}

	exitCode, err := proc.Passthrough(
		command, path, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
	)
	if err != nil {
		return 0, err
	}

	return workflow.ForChildProcess(exitCode), nil
}

func runDev(cmd *cobra.Command, engines adapter.Factory, name string) (int, error) {
	where, module, coreMajor, err := resolveSubject(cmd, name)
	if err != nil {
		return 0, err
	}

	engine, err := cli.Engine(cmd, engines, where)
	if err != nil {
		return 0, err
	}

	environment, err := engine.EnsureEnv(module, coreMajor)
	if err != nil {
		return 0, err
	}

	if branch := cli.Flag(cmd, "branch"); branch != "" {
		if err := engine.CheckoutBranch(environment, branch); err != nil {
			return 0, err
		}
	}

	modulePath := filepath.Join(environment.ProjectPath, "module")

	cli.Println(cmd, "")
	cli.Printf(cmd, "  Environment  %s\n", environment.ProjectName)
	cli.Printf(cmd, "  Module path  %s\n", modulePath)
	cli.Printf(cmd, "  Site URL     %s\n", environment.PrimaryURL)
	cli.Println(cmd, "")
	cli.Printf(cmd, "  cd %s\n", modulePath)
	cli.Println(cmd, "")

	return workflow.OK, nil
}

package command

import (
	"sort"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewModules builds the modules command.
func NewModules() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "modules",
		Short: "List the modules registered in the cockpit module registry",
		Args:  cobra.NoArgs,
	}
	cli.AddCockpit(cmd)
	cmd.RunE = cli.Run(runModules)

	return cmd
}

func runModules(cmd *cobra.Command, _ []string) (int, error) {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return 0, err
	}
	modules, err := cli.Modules(where)
	if err != nil {
		return 0, err
	}

	if len(modules) == 0 {
		cli.Printf(cmd, "No modules registered yet. Add entries to %s.\n", where.RegistryPath())

		return workflow.OK, nil
	}

	rows := make([][]string, 0, len(modules))
	for _, module := range sortedModules(modules) {
		rows = append(rows, []string{
			module.Name, module.Project, joinCores(module.CoreVersions),
		})
	}

	cli.Table{
		Headers: []string{"Module", "Project", "Core versions"},
		Rows:    rows,
	}.Render(cmd.OutOrStdout())

	return workflow.OK, nil
}

// sortedModules is the registry in name order, so two runs of a listing
// command agree with each other.
func sortedModules(modules map[string]cockpit.Module) []cockpit.Module {
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)

	ordered := make([]cockpit.Module, 0, len(names))
	for _, name := range names {
		ordered = append(ordered, modules[name])
	}

	return ordered
}

// joinCores renders a module's tracked cores.
func joinCores(cores []string) string {
	joined := ""
	for i, core := range cores {
		if i > 0 {
			joined += ", "
		}
		joined += core
	}

	return joined
}

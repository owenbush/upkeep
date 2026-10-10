package command

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewModulesUntrack builds the modules:untrack command.
//
// The last hand-edit on this surface. `modules:add` registers, `modules:track`
// changes the cores, and stopping altogether was a line to delete in an editor
// — which `modules:track`'s own refusal said in as many words when asked to
// remove the last core a module tracked.
//
// It writes without asking, like `modules:track`. Nothing on disk is touched:
// the registry is a watchlist rather than a gate, so an unwatched module drops
// out of the surveys and every subject command still works on it. The
// environments, the cached results and the base artifacts are `prune`'s
// business, and that one does ask.
//
// The core list is a judgement somebody made and lives nowhere else — GitLab
// holds the project path, so `modules:add` restores that from the authority
// rather than from this file — so the report carries the cores it removed, in
// the shape of the command that puts them back.
func NewModulesUntrack() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "modules:untrack <module>",
		Short: "Stop watching a module: remove its entry from the cockpit module registry",
		Long: `Removes a module's registry entry, so the survey commands stop covering it.

  upkeep modules:untrack jumplinks

Nothing on disk is removed and nothing stops working: the registry is a
watchlist, not a gate, so ` + "`check`, `review`, `dev`" + ` and the rest still take the
module by name. What changes is that ` + "`dashboard`, `patches`, `modules`," + `
` + "`status`" + ` and ` + "`prune`" + ` no longer iterate it.

To reclaim its environments and cached results, run ` + "`upkeep prune`" + `. To change
which cores it tracks rather than stop watching it, run ` + "`upkeep modules:track`" + `.`,
		Args: cobra.ExactArgs(1),
	}
	cli.AddCockpit(cmd)
	cli.AddWatchedModuleCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runModulesUntrack(cmd, args)
	})

	return cmd
}

func runModulesUntrack(cmd *cobra.Command, args []string) (int, error) {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return 0, err
	}
	registered, err := cli.Modules(where)
	if err != nil {
		return 0, err
	}

	// The strict lookup: there is nothing to remove for a module the registry
	// does not carry, and reporting one as unwatched would be reporting an
	// edit that never happened.
	module, err := workflow.RequireModule(registered, args[0])
	if err != nil {
		return 0, err
	}

	removed, err := cockpit.NewEditor(where.RegistryPath()).Remove(module.Name)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "%s is no longer watched: dashboard, patches, modules, status and "+
		"prune will not cover it.", removed.Name)
	// Subject commands are unaffected, and saying so is the difference between
	// this reading as "stopped watching" and as "uninstalled".
	cli.Progressf(cmd, "Nothing on disk was removed, and check, review and dev still take it "+
		"by name. Run `upkeep prune` to reclaim its environments and cached results.")
	cli.Progressf(cmd, "Put it back with: upkeep modules:add %s --core-versions=%s",
		removed.Name, strings.Join(removed.CoreVersions, ","))

	return workflow.OK, nil
}

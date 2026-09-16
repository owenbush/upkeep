package command

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/maintenance"
	"github.com/owenbush/upkeep/internal/workflow"
)

// Volumes is the engine-side volume listing the status and prune surfaces
// fold into an inventory.
//
// An interface so those commands can be exercised without a container runtime,
// and so the one thing they want from the engine here — a list, never a
// deletion — is visible in the type.
type Volumes interface {
	Volumes() []adapter.ProjectVolume
}

// NewStatus builds the status command.
func NewStatus(volumes Volumes, sizer maintenance.Sizer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report cockpit state; --disk itemizes real measured disk usage",
		Long: "Report cockpit state.\n\n" +
			"--disk itemizes real measured disk usage per module, core version and category: " +
			"project trees, materialized snapshots, engine volumes, base artifacts and fixture dumps, " +
			"with totals.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().Bool("disk", false,
		"Itemize disk usage (project trees, materialized snapshots, engine volumes, "+
			"base artifacts, fixture dumps) with totals")
	cli.AddCockpit(cmd)
	cli.AddProjectsRoot(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runStatus(cmd, volumes, sizer)
	})

	return cmd
}

func runStatus(cmd *cobra.Command, volumes Volumes, sizer maintenance.Sizer) (int, error) {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return 0, err
	}
	// Loaded, not merely stat()ed: a registry that exists but does not parse
	// is reported here, with the reason, rather than further down.
	modules, err := cli.Modules(where)
	if err != nil {
		return 0, err
	}

	projectsRoot, err := adapter.ResolveProjectsRoot(cli.Flag(cmd, cli.FlagProjectsRoot), where.Root)
	if err != nil {
		return 0, err
	}

	scanner := maintenance.NewScanner(where, projectsRoot, sizer)
	items := scanner.Scan()
	// A directory that could not be read is reported as such, not folded into
	// the totals as if it were empty.
	for _, warning := range scanner.Warnings() {
		cli.Warnf(cmd, "%s", warning)
	}

	trees := 0
	for _, item := range items {
		if item.Category == maintenance.ProjectTree {
			trees++
		}
	}
	items = append(items, maintenance.ItemsForVolumes(items, volumes.Volumes())...)

	if !cli.Switched(cmd, "disk") {
		cli.Printf(cmd, "Cockpit: %s (%d registered module(s))\n", where.Root, len(modules))
		cli.Printf(cmd, "Projects root: %s (%d environment(s))\n", projectsRoot, trees)
		cli.Printf(cmd,
			"Total tracked disk usage: %s across %d item(s). Use --disk for the breakdown.\n",
			maintenance.HumanBytes(totalBytes(items)), len(items))

		return workflow.OK, nil
	}

	renderDisk(cmd, items)

	return workflow.OK, nil
}

// renderDisk itemises the inventory, grouped so a module's things sit together.
func renderDisk(cmd *cobra.Command, items []maintenance.Item) {
	now := time.Now()

	sorted := append([]maintenance.Item(nil), items...)
	sort.Slice(sorted, func(a, b int) bool {
		return inventoryOrder(sorted[a]) < inventoryOrder(sorted[b])
	})

	rows := make([][]string, 0, len(sorted))
	for _, item := range sorted {
		rows = append(rows, []string{
			orDash(item.Module, "-"),
			orDash(item.CoreMajor, "-"),
			item.Category.Label(),
			item.Path,
			itemAge(item, now),
			protection(item),
			maintenance.HumanBytes(item.Size),
		})
	}

	cli.Table{
		Headers: []string{"Module", "Core", "Category", "Item", "Age", "Protection", "Size"},
		Rows:    rows,
	}.Render(cmd.OutOrStdout())

	byCategory := map[maintenance.Category]int64{}
	for _, item := range items {
		byCategory[item.Category] += item.Size
	}
	categories := make([]string, 0, len(byCategory))
	for category := range byCategory {
		categories = append(categories, string(category))
	}
	sort.Strings(categories)

	cli.Println(cmd, "")
	for _, category := range categories {
		typed := maintenance.Category(category)
		cli.Printf(cmd, "  %-22s %s\n",
			typed.Label()+"s:", maintenance.HumanBytes(byCategory[typed]))
	}
	cli.Printf(cmd, "  %-22s %s\n", "total:", maintenance.HumanBytes(totalBytes(items)))
}

// inventoryOrder sorts by module, then core, then category, then path.
//
// Unattributed items sort last, because they are the ones an operator has to
// decide about by hand and a listing that buried them among the ordinary rows
// would make them easy to miss.
func inventoryOrder(item maintenance.Item) string {
	module := item.Module
	if module == "" {
		module = "￿"
	}

	return module + "\x00" + item.CoreMajor + "\x00" + string(item.Category) + "\x00" + item.Path
}

// protection is why prune will leave something alone, or blank when it will
// not.
func protection(item maintenance.Item) string {
	if item.KeepMarked {
		return "keep"
	}
	if item.Category == maintenance.BaseArtifact || item.Category == maintenance.FixtureDump {
		return "canonical"
	}

	return ""
}

// itemAge is how long since something was last used, to the resolution a
// prune decision is made at.
//
// A question mark when nothing records it, and never a number: the selector
// reads unknown age as "cannot tell", so a table that printed 0d there would
// show the one thing prune will not act on as the oldest thing on the disk.
func itemAge(item maintenance.Item, now time.Time) string {
	age, known := item.Age(now)
	if !known {
		return "?"
	}
	if age >= 24*time.Hour {
		return fmt.Sprintf("%dd", int(age.Hours())/24)
	}

	return fmt.Sprintf("%dh", int(age.Hours()))
}

// orDash is a cell's value, or the placeholder when there is none.
//
// The placeholder is the caller's, because the two tables use different ones:
// the inventory's hyphen means "does not apply to this kind of item", while
// the issue list's en dash means "drupal.org did not say".
func orDash(value, placeholder string) string {
	if value == "" {
		return placeholder
	}

	return value
}

func totalBytes(items []maintenance.Item) int64 {
	var total int64
	for _, item := range items {
		total += item.Size
	}

	return total
}

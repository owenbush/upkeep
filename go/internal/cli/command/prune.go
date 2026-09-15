package command

import (
	"errors"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/maintenance"
	"github.com/owenbush/upkeep/internal/workflow"
)

// scopeFlags maps each scope flag to the scope it names. One flag per scope so
// the choice is explicit: "prune everything disposable" and "prune the
// snapshots" are different intentions and neither is a default.
var scopeFlags = []struct {
	flag        string
	scope       maintenance.Scope
	description string
}{
	{
		"trees", maintenance.ScopeTrees,
		"Prune disposable environment trees (disposed via the adapter teardown, which also " +
			"releases the engine project)",
	},
	{
		"snapshots", maintenance.ScopeSnapshots,
		"Prune materialized fixture snapshots (their committed .sql.gz dumps can rebuild them " +
			"at any time)",
	},
	{
		"projects", maintenance.ScopeProjects,
		"Prune whole engine projects: trees plus their named volumes, via the adapter teardown",
	},
	{"all", maintenance.ScopeAll, "Prune everything disposable: trees, volumes, and snapshots"},
}

// NewPrune builds the prune command.
//
// Dry-run by default. Without --yes it lists what it would delete and deletes
// nothing, because this is the only destructive command and the plan is worth
// reading before it is carried out.
func NewPrune(engines adapter.Factory, volumes Volumes, sizer maintenance.Sizer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune --trees|--snapshots|--projects|--all",
		Short: "Reclaim disposable state; dry-run unless --yes",
		Long: `Reclaim disposable state (environment trees, engine projects, materialized
snapshots). Dry-run by default: without --yes the command only lists deletion
candidates with their reclaimable sizes.

Protected regardless of any flag combination:

  * base artifacts (<cockpit>/base-artifacts/) — canonical, expensive to rebuild
  * committed fixture dumps (module tests/fixtures/*.sql.gz, <cockpit>/fixtures/*.sql.gz)
  * keep-marked items: touch <project>/.keep to keep a whole environment, or
    <artifact>.keep (e.g. materialized/<name>.sql.keep) to keep one snapshot.

Environments are always disposed through the engine adapter (containers and
named volumes released with the tree); pruned state regenerates on demand.`,
		Args: cobra.NoArgs,
	}

	for _, scope := range scopeFlags {
		cmd.Flags().Bool(scope.flag, false, scope.description)
	}
	cmd.Flags().String("older-than", "",
		"Only prune items unused for at least this long (e.g. 30d, 12h); "+
			"items of unknown age are then excluded")
	cmd.Flags().Int("keep-latest", 0,
		"Snapshots: keep this many newest snapshots per project regardless of age")
	cmd.Flags().BoolP("yes", "y", false,
		"Actually delete. Without this flag the command is a dry run and deletes NOTHING")
	cli.AddCockpit(cmd)
	cli.AddProjectsRoot(cmd)
	cli.AddVerbose(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, _ []string) (int, error) {
		return runPrune(cmd, engines, volumes, sizer)
	})

	return cmd
}

func runPrune(
	cmd *cobra.Command, engines adapter.Factory, volumes Volumes, sizer maintenance.Sizer,
) (int, error) {
	scope, err := scopeFromFlags(cmd)
	if err != nil {
		return 0, err
	}

	olderThan, err := olderThanFromFlag(cmd)
	if err != nil {
		return 0, err
	}
	keepLatest, _ := cmd.Flags().GetInt("keep-latest")

	where, err := cli.Cockpit(cmd)
	if err != nil {
		return 0, err
	}
	// Parsed up front, before anything is scanned or any plan is shown: this
	// is the only destructive command, and discovering a malformed registry
	// halfway through would abort a run the operator has already been shown a
	// plan for.
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
	// An under-reported inventory can only under-delete, so the run continues
	// — but the operator is told what could not be looked at, because "there
	// was nothing to reclaim" and "I could not look" are different answers.
	for _, warning := range scanner.Warnings() {
		cli.Warnf(cmd, "%s", warning)
	}

	if slices.Contains(scope.Categories(), maintenance.ProjectVolume) {
		items = append(items, maintenance.ItemsForVolumes(items, volumes.Volumes())...)
	}

	selector := maintenance.NewSelector(scanner.ProtectedRoots())
	candidates := selector.Select(maintenance.SelectInput{
		Items:      items,
		Scope:      scope,
		OlderThan:  olderThan,
		Now:        time.Now(),
		KeepLatest: keepLatest,
	})

	if len(candidates) == 0 {
		cli.Println(cmd, "Nothing to prune: no unprotected items match the given scope and filters.")

		return workflow.OK, nil
	}

	renderPlan(cmd, candidates)

	if !cli.Switched(cmd, "yes") {
		cli.Println(cmd, "")
		cli.Println(cmd, "Dry run: nothing was deleted. Re-run with --yes to reclaim.")

		return workflow.OK, nil
	}

	engine, err := cli.Engine(cmd, engines, where)
	if err != nil {
		return 0, err
	}

	outcome, err := maintenance.NewExecutor(selector, engine, modules, func(line string) {
		cli.Progressf(cmd, "%s", line)
	}).Execute(candidates)
	if err != nil {
		return 0, err
	}

	for _, skipped := range outcome.Skipped {
		cli.Warnf(cmd, "Skipped %s: %s", skipped.Item.Path, skipped.Reason)
	}
	cli.Printf(cmd, "Pruned %d item(s), reclaimed %s.\n",
		len(outcome.Deleted), maintenance.HumanBytes(outcome.FreedBytes))

	return workflow.OK, nil
}

// renderPlan is what would be deleted, and what that gets back.
func renderPlan(cmd *cobra.Command, candidates []maintenance.Item) {
	now := time.Now()

	rows := make([][]string, 0, len(candidates))
	var total int64
	for _, item := range candidates {
		total += item.Size
		rows = append(rows, []string{
			item.Category.Label(),
			orDash(item.Module),
			orDash(item.CoreMajor),
			item.Path,
			itemAge(item, now),
			maintenance.HumanBytes(item.Size),
		})
	}

	cli.Table{
		Headers: []string{"Category", "Module", "Core", "Item", "Age", "Reclaimable"},
		Rows:    rows,
	}.Render(cmd.OutOrStdout())

	cli.Printf(cmd, "\nTotal reclaimable: %s across %d item(s).\n",
		maintenance.HumanBytes(total), len(candidates))
}

// scopeFromFlags is the one scope this run targets.
//
// Exactly one, and no default. Two scopes at once would be ambiguous about
// what the plan covers, and a default would make the destructive command the
// one somebody runs by accident.
func scopeFromFlags(cmd *cobra.Command) (maintenance.Scope, error) {
	var picked []maintenance.Scope
	for _, scope := range scopeFlags {
		if cli.Switched(cmd, scope.flag) {
			picked = append(picked, scope.scope)
		}
	}

	if len(picked) != 1 {
		return "", errors.New(
			"pick exactly one prune scope: --trees, --snapshots, --projects, or --all",
		)
	}

	return picked[0], nil
}

// olderThanFromFlag is the age filter, or no filter at all.
func olderThanFromFlag(cmd *cobra.Command) (time.Duration, error) {
	raw := cli.Flag(cmd, "older-than")
	if raw == "" {
		return 0, nil
	}

	return maintenance.ParseDuration(raw)
}

package command

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/config"
	"github.com/owenbush/upkeep/internal/dashboard"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gate"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
	"github.com/owenbush/upkeep/internal/workflow"
)

// IssueClients hands out the drupal.org client a command should use.
//
// An interface for the same reason the GitLab one is: a command is given how
// to talk to drupal.org rather than deciding, and a test can hand over
// something that answers without a network.
type IssueClients interface {
	Issues() dashboard.IssueReader
	// Warnings is what the last scan could not read. That client degrades by
	// returning *less data*, which at the call site is indistinguishable from
	// there being less data.
	Warnings() []string
}

// NewDashboard builds the dashboard command.
//
// One table of everything open across the watched modules, a row per (issue,
// module branch). Classification is not done here: the rows come from the row
// factory, the same pipeline the fast-lane merge consumes, so the dashboard's
// READY-AUTO and the merge command's cannot drift apart.
func NewDashboard(clients cli.GitlabClients, issues IssueClients) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dashboard [module]",
		Short: "Per-module overview of everything open; name a module for its rows",
		Long: `Where to start. With no arguments it summarises every watched module — how
many merge requests and patch issues are open, how many are ready, and how many
have never been checked.

Name a module to see its individual rows. Each row carries a NEXT column with
the command to run for it.

  upkeep dashboard                    every module, one line each
  upkeep dashboard pathauto           that module's rows, with what to do about each
  upkeep dashboard pathauto --refresh re-fetch first (goes to the network)
  upkeep dashboard --all              every row of every module
  upkeep dashboard pathauto -v        the gate's own reason tokens

Cached by default, so repeat runs are instant.
upkeep explain <term> defines any column or status.`,
		Args: cobra.MaximumNArgs(1),
	}

	cli.AddCockpit(cmd)
	cli.AddCoreFilter(cmd)
	cli.AddVerbose(cmd)
	cmd.Flags().String("refresh", "",
		"Re-fetch remote data: --refresh for all modules, --refresh=<module> for one")
	cmd.Flags().Lookup("refresh").NoOptDefVal = refreshAllSentinel
	cmd.Flags().Bool("no-patches", false,
		"Omit patch rows: only merge requests, as the dashboard showed before patch "+
			"contributions were included")
	cmd.Flags().Bool("all", false,
		"Every row of every module, rather than the per-module overview")
	cli.AddModuleCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runDashboard(cmd, clients, issues, args)
	})

	return cmd
}

// refreshAllSentinel is what a bare --refresh means, as distinct from
// --refresh=<module>. A flag library cannot express "present with no value"
// otherwise, and the two mean different things: one re-fetches the cockpit,
// the other re-fetches one module.
const refreshAllSentinel = "\x00all"

func runDashboard(
	cmd *cobra.Command, clients cli.GitlabClients, issues IssueClients, args []string,
) (int, error) {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return 0, err
	}
	modules, err := cli.Modules(where)
	if err != nil {
		return 0, err
	}

	// Naming a module narrows everything about the run: which module is shown
	// in detail, and — with a bare --refresh — which one is re-fetched.
	// Refreshing a whole cockpit to look at one module would be the expensive
	// half of a command whose whole point was to be specific.
	only := ""
	if len(args) == 1 {
		module, err := workflow.RequireModule(modules, args[0])
		if err != nil {
			return 0, err
		}
		only = module.Name
		modules = map[string]cockpit.Module{only: module}
	}

	refreshAll, refreshModule, err := refreshTargets(cmd)
	if err != nil {
		return 0, err
	}
	detailed := only != "" || cli.Switched(cmd, "all")

	plan := dashboardRun{
		where:         where,
		modules:       modules,
		versionFilter: cli.Flag(cmd, cli.FlagVersion),
		withPatches:   !cli.Switched(cmd, "no-patches"),
		refreshAll:    refreshAll,
		refreshModule: refreshModule,
	}

	rows, snapshots, err := plan.gather(cmd, clients, issues)
	if err != nil {
		return 0, err
	}

	cli.ReportScanWarnings(cmd, issues.Warnings())

	if len(rows) == 0 {
		cli.Println(cmd, nothingOpen(plan.versionFilter))

		return workflow.OK, nil
	}

	palette := cli.NewPalette(cli.IsTerminal(cmd.OutOrStdout()))

	if detailed {
		renderRows(cmd, palette, rows, cli.Verbose(cmd))
		renderFooter(cmd, palette, rows, snapshots)
		renderDetailHints(cmd, palette, rows, cli.Verbose(cmd))

		return workflow.OK, nil
	}

	renderOverview(cmd, palette, rows, snapshots)
	renderFooter(cmd, palette, rows, snapshots)
	cli.Println(cmd, palette.Paint(cli.Grey,
		"upkeep dashboard <module> for one module's rows · --all for every row"))

	return workflow.OK, nil
}

// refreshTargets is what this run re-fetches: everything, one module, or
// nothing.
func refreshTargets(cmd *cobra.Command) (all bool, module string, err error) {
	if !cmd.Flags().Changed("refresh") {
		return false, "", nil
	}

	// `--refresh=` names no module, which is a mistake worth saying rather
	// than a silent no-op: somebody typing it meant to refresh something.
	if cli.Flag(cmd, "refresh") == "" {
		return false, "", errors.New(
			"--refresh= names no module. Use --refresh to re-fetch everything, " +
				"or --refresh=<module> for one",
		)
	}

	// A bare --refresh re-fetches everything in scope, which is already just
	// the named module when one was named: naming narrows the run before this
	// is asked, so there is no separate "refresh only that one" to express.
	if cli.Flag(cmd, "refresh") == refreshAllSentinel {
		return true, "", nil
	}

	return false, cli.Flag(cmd, "refresh"), nil
}

// dashboardRun is one invocation's inputs.
type dashboardRun struct {
	where         *cockpit.Cockpit
	modules       map[string]cockpit.Module
	versionFilter string
	withPatches   bool
	refreshAll    bool
	refreshModule string
}

// gather is one pass per module, in name order: resolve its snapshot — from
// the cache, or by fetching — then turn it straight into rows.
//
// Every module therefore leaves the loop having produced either its rows or a
// failure row. There is no third outcome to defend against later.
func (r dashboardRun) gather(
	cmd *cobra.Command, clients cli.GitlabClients, issues IssueClients,
) ([]dashboard.Row, map[string]dashboard.ModuleSnapshot, error) {
	cache := dashboard.NewCache(r.where.DashboardCachePath())
	factory := dashboard.NewRowFactory(
		results.NewCache(r.where.ResultsPath()),
		gate.NewFastLane(config.BotPatternForCore),
	)

	names := make([]string, 0, len(r.modules))
	for name := range r.modules {
		names = append(names, name)
	}
	sort.Strings(names)

	snapshots := map[string]dashboard.ModuleSnapshot{}
	rows := []dashboard.Row{}
	var client *gitlab.Client

	for _, name := range names {
		module := r.modules[name]

		snapshot, cached := dashboard.ModuleSnapshot{}, false
		if !r.refreshAll && r.refreshModule != name {
			snapshot, cached = cache.Load(name)
		}

		if !cached {
			// Reading is anonymous when there is no token, and the dashboard
			// is a read — a private project will simply read as missing,
			// which the row says.
			if client == nil {
				client = cli.ReadingClient(cmd, clients)
			}

			cli.Progressf(cmd, "Fetching %s ...", name)
			fetched, failure := dashboard.FetchModule(client, issues.Issues(), module, time.Now())
			if failure != nil {
				rows = append(rows, dashboard.RowForModuleFailure(name, failure))

				continue
			}

			snapshot = fetched
			if err := cache.Save(name, snapshot); err != nil {
				// A snapshot that cannot be cached still makes rows; the only
				// cost is that the next run fetches again.
				cli.Warnf(cmd, "%s", err)
			}
		}

		snapshots[name] = snapshot

		for _, row := range factory.Rows(dashboard.RowsInput{
			Module:        module,
			Project:       snapshot.Project(),
			MergeRequests: snapshot.MergeRequests(),
			VersionFilter: r.versionFilter,
			Snapshot:      &snapshot,
		}) {
			// --no-patches keeps the merge-request-only view the dashboard had
			// before patch contributions were rows. A filter over one row set
			// rather than a second one left out: a row carrying both a patch
			// and a merge request is a merge-request row that also mentions a
			// patch.
			//
			// A module failure never reaches here — it is appended above, on
			// the path that skips this loop — so there is no exemption for one
			// to write. The PHP carries that exemption anyway; it is
			// unreachable there for the same reason.
			if !r.withPatches && row.MergeRequest == nil {
				continue
			}
			rows = append(rows, row)
		}
	}

	return rows, snapshots, nil
}

// nothingOpen is what to say when there is nothing to show.
func nothingOpen(versionFilter string) string {
	if versionFilter == "" {
		return "No open contributions across the watched modules."
	}

	return fmt.Sprintf(
		"No open contributions targeting core %s across the watched modules.", versionFilter)
}

// renderRows is the detailed table.
func renderRows(cmd *cobra.Command, palette cli.Palette, rows []dashboard.Row, verbose bool) {
	cells := make([][]string, 0, len(rows))
	groups := make([]string, 0, len(rows))
	for _, row := range rows {
		cells = append(cells, row.TableCells(verbose))
		groups = append(groups, row.Module)
	}

	cli.Table{
		Headers: []string{
			"MODULE", "ISSUE", "VERSION", "TITLE", "MR", "PATCH", "CI", "LOCAL", "STATUS", "NEXT",
		},
		Rows:      cells,
		GroupKeys: groups,
		Colourise: func(cells []string) []string { return colourRow(palette, cells) },
	}.Render(cmd.OutOrStdout())
}

// The detailed table's columns, named so a cell moving is a failure rather
// than a surprise.
const (
	colMR = 4 + iota
	colPatch
	colCI
	colLocal
	colStatus
	colNext
)

// colourRow emphasises the cells that carry a verdict.
func colourRow(palette cli.Palette, cells []string) []string {
	painted := append([]string(nil), cells...)

	// MR — a landing is the one cell that says the work is done.
	if strings.Contains(cells[colMR], "merged") {
		painted[colMR] = palette.Paint(cli.Green, cells[colMR])
	}

	// PATCH — the flag, not the count: the number is context, the arrow is the
	// claim that the branch is behind the issue.
	switch {
	case strings.Contains(cells[colPatch], "↑"):
		painted[colPatch] = palette.Highlight(cli.Yellow, cells[colPatch], "↑")
	case cells[colPatch] == "–":
		painted[colPatch] = palette.Paint(cli.Grey, cells[colPatch])
	}

	painted[colCI] = paintByPrefix(palette, cells[colCI], map[string]cli.Colour{
		"pass": cli.Green, "fail": cli.Red, "–": cli.Grey,
	})

	// LOCAL carries the cores it is about — "pass 10,11", "pass 11 · ? 10" —
	// so it is matched on its leading word rather than compared whole. A
	// partial pass is amber, not green: it names a core nobody checked.
	switch {
	case strings.Contains(cells[colLocal], "·"):
		painted[colLocal] = palette.Paint(cli.Yellow, cells[colLocal])
	case strings.HasPrefix(cells[colLocal], "pass"):
		painted[colLocal] = palette.Paint(cli.Green, cells[colLocal])
	case strings.HasPrefix(cells[colLocal], "fail"):
		painted[colLocal] = palette.Paint(cli.Red, cells[colLocal])
	default:
		painted[colLocal] = palette.Paint(cli.Grey, cells[colLocal])
	}

	painted[colStatus] = palette.Paint(statusColour(cells[colStatus]), cells[colStatus])

	// NEXT is always a command, and the point of the row, so it is the thing
	// that stands out.
	painted[colNext] = palette.Paint(cli.Cyan, cells[colNext])

	return painted
}

// statusColour reads both vocabularies, because -v swaps the phrase for the
// gate's own tokens and both should read the same way: green means go, red
// means stopped, amber means your move.
func statusColour(status string) cli.Colour {
	for _, green := range []string{"READY-AUTO", "merged", "ready to merge"} {
		if strings.HasPrefix(status, green) {
			return cli.Green
		}
	}
	for _, red := range []string{"BLOCKED", "conflicts"} {
		if strings.HasPrefix(status, red) {
			return cli.Red
		}
	}
	if strings.Contains(status, "failed") {
		return cli.Red
	}
	for _, amber := range []string{"REVIEW", "needs", "checks are stale"} {
		if strings.HasPrefix(status, amber) {
			return cli.Yellow
		}
	}

	return ""
}

// paintByPrefix paints a cell by its leading word.
func paintByPrefix(palette cli.Palette, cell string, byPrefix map[string]cli.Colour) string {
	for prefix, colour := range byPrefix {
		if strings.HasPrefix(cell, prefix) {
			return palette.Paint(colour, cell)
		}
	}

	return cell
}

// renderOverview is one line per module, aggregated from exactly the rows the
// detailed view would print.
//
// Aggregated rather than recounted, on purpose. A module whose overview says
// three READY must show three READY when drilled into, and the only way to
// guarantee that is for both to be the same list.
func renderOverview(
	cmd *cobra.Command,
	palette cli.Palette,
	rows []dashboard.Row,
	snapshots map[string]dashboard.ModuleSnapshot,
) {
	byModule := map[string][]dashboard.Row{}
	order := []string{}
	for _, row := range rows {
		if _, seen := byModule[row.Module]; !seen {
			order = append(order, row.Module)
		}
		byModule[row.Module] = append(byModule[row.Module], row)
	}

	now := time.Now()
	cells := make([][]string, 0, len(order))
	for _, module := range order {
		age := "never"
		if snapshot, cached := snapshots[module]; cached {
			age = snapshot.AgeLabel(now)
		}
		cells = append(cells,
			dashboard.SummaryFromRows(module, byModule[module]).TableCells(age))
	}

	cli.Table{
		Headers: []string{
			"MODULE", "BRANCHES", "MRS", "PATCH ISSUES", "READY", "CI FAILED", "UNCHECKED", "CACHED",
		},
		Rows:      cells,
		Colourise: func(cells []string) []string { return colourOverview(palette, cells) },
	}.Render(cmd.OutOrStdout())
}

// The overview's columns.
const (
	sumBranches = 1 + iota
	sumMRs
	sumPatchIssues
	sumReady
	sumCIFailed
	sumUnchecked
	sumCached
)

// colourOverview emphasises the three cells that carry a verdict.
//
// READY means you can act right now, CI FAILED means nobody here can, and
// UNCHECKED is the queue of work that would turn one into the other. Counts
// and dashes stay muted so those three stand out.
func colourOverview(palette cli.Palette, cells []string) []string {
	painted := append([]string(nil), cells...)

	for column, colour := range map[int]cli.Colour{
		sumReady: cli.Green, sumCIFailed: cli.Red, sumUnchecked: cli.Yellow,
	} {
		if cells[column] == "–" {
			painted[column] = palette.Paint(cli.Grey, cells[column])

			continue
		}
		painted[column] = palette.Paint(colour, cells[column])
	}

	for _, column := range []int{sumBranches, sumMRs, sumPatchIssues} {
		if cells[column] == "–" {
			painted[column] = palette.Paint(cli.Grey, cells[column])
		}
	}
	painted[sumCached] = palette.Paint(cli.Grey, cells[sumCached])

	return painted
}

// renderFooter is what the table covers, and how old it is.
func renderFooter(
	cmd *cobra.Command,
	palette cli.Palette,
	rows []dashboard.Row,
	snapshots map[string]dashboard.ModuleSnapshot,
) {
	openMRs, patchIssues, modules := footerCounts(rows)

	oldest := time.Time{}
	for _, snapshot := range snapshots {
		if oldest.IsZero() || snapshot.FetchedAt.Before(oldest) {
			oldest = snapshot.FetchedAt
		}
	}
	cached := "never"
	if !oldest.IsZero() {
		cached = dashboard.ModuleSnapshot{FetchedAt: oldest}.AgeLabel(time.Now())
	}

	segments := []string{fmt.Sprintf("%d open MRs", openMRs)}
	if patchIssues > 0 {
		segments = append(segments,
			fmt.Sprintf("%d patch %s", patchIssues, plural(patchIssues, "issue", "issues")))
	}
	segments = append(segments,
		fmt.Sprintf("%d %s", modules, plural(modules, "module", "modules")),
		"cached "+cached,
		"--refresh to update",
	)

	cli.Println(cmd, "")
	cli.Println(cmd, palette.Paint(cli.Grey, strings.Join(segments, " · ")))
}

// footerCounts is what the table covers: open merge requests, issues carrying
// patches, and modules.
//
// Counted by identity rather than by row, because one merge request can appear
// on more than one row and a footer that said "4 open MRs" over three would be
// describing the table's shape rather than the work.
//
// A landed merge request is not open. It is attached to a row precisely
// *because* it landed — that is how a row says the work is already done — so
// counting it would inflate the number a maintainer reads as their queue.
func footerCounts(rows []dashboard.Row) (openMRs, patchIssues, modules int) {
	mrKeys := map[string]bool{}
	patchKeys := map[string]bool{}
	moduleNames := map[string]bool{}

	for _, row := range rows {
		moduleNames[row.Module] = true
		for _, mergeRequest := range row.MergeRequests {
			if mergeRequest.State == "merged" {
				continue
			}
			mrKeys[fmt.Sprintf("%s:%d", row.Module, mergeRequest.IID)] = true
		}
		if row.IssueNid != 0 && row.PatchCount > 0 {
			patchKeys[fmt.Sprintf("%s:%d", row.Module, row.IssueNid)] = true
		}
	}

	return len(mrKeys), len(patchKeys), len(moduleNames)
}

// renderDetailHints is what to read, under the table a maintainer has just
// been handed.
//
// The overview has always carried a hint and the drill-down carried none,
// which is backwards: the overview is a summary a person acts on by drilling
// in, while the drill-down is where the actual work is chosen.
func renderDetailHints(
	cmd *cobra.Command, palette cli.Palette, rows []dashboard.Row, verbose bool,
) {
	ready := 0
	for _, row := range rows {
		if row.IsReadyAuto() {
			ready++
		}
	}

	hints := []string{}
	if ready > 0 {
		hints = append(hints, fmt.Sprintf("%d %s ready to merge · upkeep merge --fast-lane",
			ready, plural(ready, "row", "rows")))
	}
	hints = append(hints,
		"Run the command in NEXT for any row · upkeep explain <term> for what a column means")
	if !verbose {
		hints = append(hints,
			"-v shows the gate's own reason tokens instead of the plain-English status")
	}

	for _, hint := range hints {
		cli.Println(cmd, palette.Paint(cli.Grey, hint))
	}
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}

	return many
}

// DrupalClients is the production drupal.org client source.
//
// One client for the whole run, so its warnings accumulate across every module
// a refresh touches and the shortfall is reported once at the end.
type DrupalClients struct{ client *drupal.Client }

// NewDrupalClients builds it.
func NewDrupalClients() *DrupalClients {
	return &DrupalClients{client: drupal.NewClient(nil, "")}
}

// Issues is the reader a refresh scans with.
func (c *DrupalClients) Issues() dashboard.IssueReader { return c.client }

// Warnings is what the scan could not read.
func (c *DrupalClients) Warnings() []string { return c.client.Warnings() }

package command

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/dashboard"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/patches"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewPatches builds the patches command.
//
// Surfaces the drupal.org issues in Needs Review / RTBC whose contribution is
// not reachable from the merge-request dashboard — the patch files that never
// became a branch, and the issues where a patch sits alongside a merge
// request.
//
// An issue is withheld only when a merge request genuinely *carries* the work:
// it asserts authorship of the issue and it has a non-empty diff. Neither half
// is incidental — an empty draft that merely mentions an issue used to hide
// every patch on it, which is how a patch-only report came to hide patches.
func NewPatches(clients cli.GitlabClients, issues IssueClients) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "patches",
		Short: "Show drupal.org issues carrying patch files, and how they relate to merge requests",
		Long: `Issues carrying patch files, and how each relates to a merge request — the
contributions the MR-centric dashboard cannot see.

  upkeep patches                  every registered module
  upkeep patches --module=pathauto
  upkeep patches --without-mr     only what no branch carries

To check one: upkeep patch:check <module> <issue>. An MR shown as "empty"
carries no commits, so any patch beside it is the only work there is —
see upkeep explain "empty MR".`,
		Args: cobra.NoArgs,
	}
	cli.AddCockpit(cmd)
	cmd.Flags().String("module", "", "Only scan this module (default: all registered modules)")
	cmd.Flags().Bool("without-mr", false,
		"Only issues no merge request carries — omit those where a patch sits alongside a real MR")
	cli.AddModuleFlagCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, _ []string) (int, error) {
		return runPatches(cmd, clients, issues)
	})

	return cmd
}

// scannedStatuses is the pair a patch contribution sits in.
//
// Narrower than the whole open queue on purpose: this report is about work
// somebody has posted and is waiting on, which is what those two statuses
// mean. "What could I work on?" is a different question, and `issues` answers
// it.
func scannedStatuses() []drupal.IssueStatus { return drupal.AwaitingReviewStatuses() }

func runPatches(cmd *cobra.Command, clients cli.GitlabClients, issues IssueClients) (int, error) {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return 0, err
	}
	modules, err := cli.Modules(where)
	if err != nil {
		return 0, err
	}

	// A survey command, so it narrows *into* the watchlist rather than taking
	// any module: a report of "every module" has to be a report of a set
	// somebody declared.
	if only := cli.Flag(cmd, "module"); only != "" {
		module, err := workflow.RequireModule(modules, only)
		if err != nil {
			return 0, err
		}
		modules = map[string]cockpit.Module{only: module}
	}

	source := issues.Issues()
	cache := dashboard.NewCache(where.DashboardCachePath())
	withoutMr := cli.Switched(cmd, "without-mr")

	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)

	var reported []patches.Contribution
	for _, name := range names {
		found := source.ProjectIssues(name, scannedStatuses())
		if len(found) == 0 {
			continue
		}

		for _, contribution := range contributionsFor(
			cmd, clients, cache, modules[name], found,
		) {
			kind := contribution.Kind()
			// Reachable from the dashboard, so it has no business here.
			if kind.IsCoveredByMergeRequest() {
				continue
			}
			if withoutMr && kind.HasSubstantiveMergeRequest() {
				continue
			}
			reported = append(reported, contribution)
		}
	}

	cli.ReportScanWarnings(cmd, issues.Warnings())

	if len(reported) == 0 {
		cli.Progressf(cmd,
			"Nothing to report — every Needs Review / RTBC issue is covered by a merge request "+
				"that carries changes.")

		return workflow.OK, nil
	}

	renderPatches(cmd, reported)
	cli.Println(cmd, "")
	cli.Println(cmd, patchesSummary(reported))

	return workflow.OK, nil
}

// contributionsFor pairs a module's scanned issues with the merge requests
// that claim them.
//
// From the dashboard snapshot when there is one: those hold single-merge-
// request detail payloads, so their diff refs — and with them emptiness — are
// already settled. A snapshot written by an older upkeep has no diff refs and
// reads as unknown, which counts as substantive: stale data narrows the report
// back to its previous behaviour rather than mislabelling.
func contributionsFor(
	cmd *cobra.Command,
	clients cli.GitlabClients,
	cache *dashboard.Cache,
	module cockpit.Module,
	found []drupal.Issue,
) []patches.Contribution {
	if snapshot, cached := cache.Load(module.Name); cached {
		return patches.Pair(module.Name, found, snapshot.MergeRequests(), snapshot.ForkNids)
	}

	// Read anonymously: drupalcode serves a public project's merge requests
	// and forks without a credential. The PHP demanded one here and gave up
	// the whole cross-reference without it — listing every Needs Review / RTBC
	// issue with an empty MR column, which is the noise this report exists to
	// cut. Reported from the port and fixed there too.
	client := cli.ReadingClient(cmd, clients)

	project, failure := client.Project(module.Project)
	if failure != nil {
		return patches.Pair(module.Name, found, nil, nil)
	}

	mergeRequests, failure := client.OpenMergeRequests(*project)
	if failure != nil {
		return patches.Pair(module.Name, found, nil, nil)
	}

	// Merged ones too. An open issue whose work has already landed reads
	// exactly like an untouched one when only open merge requests are fetched,
	// and Project Update Bot compatibility issues — kept open on purpose so
	// the bot can post again — are mostly that shape.
	if merged, failure := client.MergedMergeRequests(*project, mergedPatchLimit); failure == nil {
		mergeRequests = append(mergeRequests, merged...)
	}

	forkNids, failure := client.IssueForkNids(*project)
	if failure != nil {
		forkNids = nil
	}

	return resolveEmptiness(
		client, *project, patches.Pair(module.Name, found, mergeRequests, forkNids))
}

// mergedPatchLimit is how far back the landing search goes — the same bound
// the dashboard refresh uses, and for the same reason: enough to answer "has
// this issue's work already landed?" without walking a decade of history.
const mergedPatchLimit = 100

// resolveEmptiness re-fetches the merge requests whose emptiness the list
// endpoint could not answer.
//
// GitLab omits diff_refs from list payloads and offers no bulk alternative, so
// this costs one request per candidate — bounded to merge requests already
// paired with a scanned issue, since one on any other issue cannot change a
// row. A fetch that fails leaves the emptiness unknown, which is treated as
// real work.
func resolveEmptiness(
	client *gitlab.Client, project gitlab.Project, contributions []patches.Contribution,
) []patches.Contribution {
	for index, contribution := range contributions {
		for at, mergeRequest := range contribution.MergeRequests {
			if mergeRequest.CarriesChanges() != gitlab.Unknown {
				continue
			}
			detail, failure := client.MergeRequest(project, mergeRequest.IID)
			// The detail payload is strictly richer than the listed one, so it
			// replaces rather than augments — but only once it has identified
			// itself as the same merge request. A body answering with some
			// other resource must not be written into this row under the
			// listed merge request's identity.
			if failure == nil && detail.IID == mergeRequest.IID {
				contributions[index].MergeRequests[at] = *detail
			}
		}
	}

	return contributions
}

// renderPatches writes the table.
func renderPatches(cmd *cobra.Command, contributions []patches.Contribution) {
	rows := make([][]string, 0, len(contributions))
	for _, contribution := range contributions {
		issue := contribution.Issue

		count := "–"
		if issue.PatchCount() > 0 {
			count = fmt.Sprint(issue.PatchCount())
		}
		latest := "–"
		if newest, found := issue.LatestPatch(); found {
			latest = dashboard.Truncate(newest.Name, 30)
		}

		rows = append(rows, []string{
			contribution.Module,
			"#" + fmt.Sprint(issue.Nid),
			issue.Status.ShortLabel(),
			count,
			latest,
			contribution.MergeRequestCell(),
			dashboard.Truncate(issue.Title, dashboard.DefaultTitleWidth),
		})
	}

	palette := cli.NewPalette(cli.IsTerminal(cmd.OutOrStdout()))
	cli.Table{
		Headers: []string{"MODULE", "ISSUE", "STATUS", "PATCHES", "LATEST PATCH", "MR", "TITLE"},
		Rows:    rows,
		Colourise: func(cells []string) []string {
			return colourPatchRow(palette, cells)
		},
	}.Render(cmd.OutOrStdout())
}

// The patch table's columns.
const (
	patchStatusColumn = 2 + iota
	patchCountColumn
	latestPatchColumn
	patchMRColumn
)

// colourPatchRow decorates a row without changing any cell's visible length.
func colourPatchRow(palette cli.Palette, cells []string) []string {
	painted := append([]string(nil), cells...)

	switch cells[patchStatusColumn] {
	case "RTBC":
		painted[patchStatusColumn] = palette.Paint(cli.Green, cells[patchStatusColumn])
	case "review":
		painted[patchStatusColumn] = palette.Paint(cli.Yellow, cells[patchStatusColumn])
	}

	for _, column := range []int{patchCountColumn, latestPatchColumn} {
		if cells[column] == "–" {
			painted[column] = palette.Paint(cli.Grey, "–")
		}
	}

	switch mergeRequest := cells[patchMRColumn]; {
	case mergeRequest == "–":
		painted[patchMRColumn] = palette.Paint(cli.Grey, mergeRequest)
	// An empty MR is the actionable cell on the row — it is why the patch
	// beside it has gone unreviewed — so it gets the warning colour rather
	// than the muted one a bare dash gets.
	case strings.Contains(mergeRequest, " empty"):
		painted[patchMRColumn] = palette.Paint(cli.Yellow, mergeRequest)
	// Merged with newer work since is an ordinary open contribution again;
	// merged with nothing since needs no work, and the colour says so before
	// the words are read.
	case strings.Contains(mergeRequest, "newer work since"):
		painted[patchMRColumn] = palette.Paint(cli.Yellow, mergeRequest)
	case strings.Contains(mergeRequest, " merged "):
		painted[patchMRColumn] = palette.Paint(cli.Green, mergeRequest)
	}

	return painted
}

// patchesSummary is the footer: how many, of what kinds, across how many
// modules, and what was scanned.
func patchesSummary(contributions []patches.Contribution) string {
	// Counted in first-seen order rather than sorted, so the summary reads in
	// the order the kinds appear in the table above it.
	counts := map[string]int{}
	labels := []string{}
	moduleSeen := map[string]bool{}
	moduleCount := 0

	for _, contribution := range contributions {
		if label := contribution.Kind().SummaryLabel(); label != "" {
			if counts[label] == 0 {
				labels = append(labels, label)
			}
			counts[label]++
		}
		if !moduleSeen[contribution.Module] {
			moduleSeen[contribution.Module] = true
			moduleCount++
		}
	}

	segments := []string{fmt.Sprintf("%d %s",
		len(contributions), plural(len(contributions), "issue", "issues"))}
	for _, label := range labels {
		segments = append(segments, fmt.Sprintf("%d %s", counts[label], label))
	}
	segments = append(segments, fmt.Sprintf("%d %s",
		moduleCount, plural(moduleCount, "module", "modules")))

	scanned := make([]string, 0, len(scannedStatuses()))
	for _, status := range scannedStatuses() {
		scanned = append(scanned, status.ShortLabel())
	}
	segments = append(segments, "statuses: "+strings.Join(scanned, ", "))

	return strings.Join(segments, " · ")
}

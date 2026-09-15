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

// NewIssues builds the issues command.
//
// Every open issue on a module, and what — if anything — has been contributed
// to it. The counterpart to patches and the merge-request dashboard, and the
// thing that makes the other two make sense: both of those start from a
// *contribution*, which answers "what is waiting for me?" and is silent about
// "what could I work on?" — on a real module, most of the queue. Pathauto
// carries 93 open issues, of which the contribution-shaped scan sees 42.
//
// So this scans every open status and treats the contribution as a *column*
// rather than as the price of admission. An Active bug report with nothing
// attached is the most actionable row on the list: it is unclaimed work.
func NewIssues(clients cli.GitlabClients, issues IssueClients) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issues <module>",
		Short: "List a module's open drupal.org issues and what has been contributed to each",
		Long: `Every open issue on a module, whether or not anyone has contributed to it —
the difference from dashboard and patches, which both start from a
contribution.

  upkeep issues pathauto              every open issue
  upkeep issues pathauto --unclaimed  only what nobody has started
  upkeep issues pathauto --status=active

The merge-request column is filled from the dashboard cache, so run
upkeep dashboard --refresh=<module> if it is empty.
To begin work on one: upkeep start <module> <issue>.`,
		Args: cobra.ExactArgs(1),
	}
	cli.AddCockpit(cmd)
	cmd.Flags().String("status", "",
		"Only this status: active, review, needs-work, rtbc, postponed (default: every open status)")
	cmd.Flags().Bool("unclaimed", false,
		"Only issues nobody has contributed to yet — the work that has not started")
	cli.AddModuleCompletion(cmd)
	registerStatusCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runIssues(cmd, clients, issues, args[0])
	})

	return cmd
}

func runIssues(
	cmd *cobra.Command, clients cli.GitlabClients, issues IssueClients, moduleName string,
) (int, error) {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return 0, err
	}
	modules, err := cli.Modules(where)
	if err != nil {
		return 0, err
	}
	module, err := cli.ResolveModule(where, modules, moduleName)
	if err != nil {
		return 0, err
	}

	source := issues.Issues()

	cli.Progressf(cmd, "Reading %s issues from drupal.org ...", module.Name)
	found := source.ProjectIssues(module.Name, selectedStatuses(cmd))

	// Merge requests come from the dashboard cache when there is one, and from
	// a live read when there is not.
	//
	// It used to be cache-only, to spare a cockpit that has never refreshed a
	// credential error. Anonymous reads removed that constraint, and the
	// watchlist split made the gap harmful: an unwatched module has no
	// snapshot and cannot be given one — `dashboard --refresh` surveys the
	// watchlist — so every issue looked unclaimed and NEXT said `upkeep start`
	// on work somebody had already done.
	snapshot, cached := dashboard.NewCache(where.DashboardCachePath()).Load(module.Name)
	mergeRequests, forkNids := snapshot.MergeRequests(), snapshot.ForkNids
	if !cached {
		mergeRequests, forkNids = readMergeRequests(cmd, clients, module)
	}

	contributions := patches.Pair(module.Name, found, mergeRequests, forkNids)
	if cli.Switched(cmd, "unclaimed") {
		contributions = onlyUnclaimed(contributions)
	}

	cli.ReportScanWarnings(cmd, issues.Warnings())

	if len(contributions) == 0 {
		cli.Progressf(cmd, "No matching open issues on %s.", module.Name)

		return workflow.OK, nil
	}

	renderIssues(cmd, contributions)
	cli.Println(cmd, "")
	cli.Println(cmd, issuesSummary(
		contributions,
		len(mergeRequests) == 0 && !cached,
		module.Name,
		cockpit.IsRegistered(modules, module.Name),
	))

	return workflow.OK, nil
}

// readMergeRequests is the module's open merge requests, read live.
//
// Only reached when there is no snapshot to read them from. Needs no
// credential — git.drupalcode.org serves a public project's merge requests and
// forks anonymously — which is what makes this affordable at all; until
// reading without a token was possible, doing it here would have turned issues
// into a command that demands a PAT.
//
// Every failure yields nothing and lets the run continue. The issue queue is
// this command's subject; the contribution column is context, and losing
// context is not worth losing the list for. The footer says so.
//
// The fork map is fetched with them, because it is the only thing that pairs a
// Project Update Bot merge request to its issue — those mention their issue in
// a way the owning-reference rule rejects by design. Without it a
// compatibility issue with a bot MR on it reads as untouched, which on a
// module with many of them is most of the difference.
func readMergeRequests(
	cmd *cobra.Command, clients cli.GitlabClients, module cockpit.Module,
) ([]gitlab.MergeRequest, map[int]int) {
	client := cli.ReadingClient(cmd, clients)

	project, failure := client.Project(module.Project)
	if failure != nil {
		return nil, nil
	}

	mergeRequests, failure := client.OpenMergeRequests(*project)
	if failure != nil {
		return nil, nil
	}

	forkNids, failure := client.IssueForkNids(*project)
	if failure != nil {
		forkNids = nil
	}

	return mergeRequests, forkNids
}

// onlyUnclaimed narrows to the issues nobody has contributed to.
func onlyUnclaimed(contributions []patches.Contribution) []patches.Contribution {
	kept := make([]patches.Contribution, 0, len(contributions))
	for _, contribution := range contributions {
		if contribution.Kind() == patches.Nothing {
			kept = append(kept, contribution)
		}
	}

	return kept
}

// renderIssues writes the table, most actionable first.
func renderIssues(cmd *cobra.Command, contributions []patches.Contribution) {
	// What awaits a maintainer's verdict, then everything else, newest issue
	// first within each group. An RTBC issue is somebody waiting on you; an
	// Active one is waiting on nobody.
	sorted := append([]patches.Contribution(nil), contributions...)
	sort.SliceStable(sorted, func(a, b int) bool {
		left, right := sorted[a].Issue, sorted[b].Issue
		if left.Status.NeedsMaintainer() != right.Status.NeedsMaintainer() {
			return left.Status.NeedsMaintainer()
		}

		return left.Nid > right.Nid
	})

	rows := make([][]string, 0, len(sorted))
	for _, contribution := range sorted {
		issue := contribution.Issue
		rows = append(rows, []string{
			"#" + fmt.Sprint(issue.Nid),
			issue.Status.ShortLabel(),
			orDash(issue.PriorityLabel(), "–"),
			contributionCell(contribution),
			dashboard.Truncate(issue.Title, 44),
			nextIssueCommand(contribution),
		})
	}

	palette := cli.NewPalette(cli.IsTerminal(cmd.OutOrStdout()))
	cli.Table{
		Headers: []string{"ISSUE", "STATUS", "PRIORITY", "CONTRIBUTION", "TITLE", "NEXT"},
		Rows:    rows,
		Colourise: func(cells []string) []string {
			return colourIssueRow(palette, cells)
		},
	}.Render(cmd.OutOrStdout())
}

// The issue table's columns, named so a cell moving is a failure rather than a
// surprise.
const (
	issueStatusColumn = 1 + iota
	issuePriorityColumn
	issueContributionColumn
	issueTitleColumn
	issueNextColumn
)

// unclaimedCell is the word an issue with nothing on it carries.
//
// The word, not a dash: this is the row the command exists to surface. It goes
// in the raw cell because the table measures raw widths and forbids the
// coloriser from changing a cell's visible length.
const unclaimedCell = "unclaimed"

// contributionCell is what has arrived on the issue, in one cell: the merge
// request, the patch count, or the word meaning nobody has started.
func contributionCell(contribution patches.Contribution) string {
	patchCount := contribution.Issue.PatchCount()
	mergeRequest := contribution.MergeRequestCell()

	switch {
	case mergeRequest != "–" && patchCount > 0:
		return fmt.Sprintf("%s, %d patch", mergeRequest, patchCount)
	case mergeRequest != "–":
		return mergeRequest
	case patchCount > 0:
		return fmt.Sprintf("%d patch", patchCount)
	}

	return unclaimedCell
}

// nextIssueCommand is the command for that row.
//
// An unclaimed issue is the one this view exists to surface, so it gets the
// verb that starts work. Anything already carrying a contribution points at
// whichever command evaluates that contribution — the same commands the
// dashboard's NEXT column names, so the two views never suggest different
// things about the same issue.
func nextIssueCommand(contribution patches.Contribution) string {
	if substantive := contribution.SubstantiveMergeRequests(); len(substantive) > 0 {
		return fmt.Sprintf("upkeep check %s %d", contribution.Module, substantive[0].IID)
	}
	if contribution.Issue.PatchCount() > 0 {
		return fmt.Sprintf("upkeep patch:check %s %d", contribution.Module, contribution.Issue.Nid)
	}

	return fmt.Sprintf("upkeep start %s %d", contribution.Module, contribution.Issue.Nid)
}

// colourIssueRow decorates a row without changing any cell's visible length.
func colourIssueRow(palette cli.Palette, cells []string) []string {
	painted := append([]string(nil), cells...)

	switch cells[issueStatusColumn] {
	case "RTBC":
		painted[issueStatusColumn] = palette.Paint(cli.Green, cells[issueStatusColumn])
	case "review":
		painted[issueStatusColumn] = palette.Paint(cli.Yellow, cells[issueStatusColumn])
	case "active":
		painted[issueStatusColumn] = palette.Paint(cli.Cyan, cells[issueStatusColumn])
	default:
		painted[issueStatusColumn] = palette.Paint(cli.Grey, cells[issueStatusColumn])
	}

	switch cells[issuePriorityColumn] {
	case "Critical", "Major":
		painted[issuePriorityColumn] = palette.Paint(cli.Red, cells[issuePriorityColumn])
	case "–":
		painted[issuePriorityColumn] = palette.Paint(cli.Grey, "–")
	}

	// Highlighted rather than muted: unclaimed work is the point of the
	// command, not an absence.
	if cells[issueContributionColumn] == unclaimedCell {
		painted[issueContributionColumn] = palette.Paint(cli.Cyan, unclaimedCell)
	}

	painted[issueNextColumn] = palette.Paint(cli.Cyan, cells[issueNextColumn])

	return painted
}

// issuesSummary is the footer: the counts, and what the list is missing.
func issuesSummary(
	contributions []patches.Contribution, withoutSnapshot bool, module string, watched bool,
) string {
	unclaimed, awaiting := 0, 0
	for _, contribution := range contributions {
		if contribution.Kind() == patches.Nothing {
			unclaimed++
		}
		if contribution.Issue.Status.NeedsMaintainer() {
			awaiting++
		}
	}

	noun := "issues"
	if len(contributions) == 1 {
		noun = "issue"
	}
	segments := []string{
		fmt.Sprintf("%d open %s", len(contributions), noun),
		fmt.Sprintf("%d awaiting you", awaiting),
		fmt.Sprintf("%d unclaimed", unclaimed),
	}

	if withoutSnapshot {
		// Said rather than left to look like "no merge requests exist" — and
		// the suggestion has to be one that works for *this* module. The
		// dashboard surveys the watchlist, so pointing an unwatched module at
		// --refresh would send somebody to a command that refuses them.
		if watched {
			segments = append(segments, "no cached MRs — run `upkeep dashboard --refresh="+module+
				"` to fill the CONTRIBUTION column")
		} else {
			segments = append(segments, "no cached MRs — "+module+
				" is not on the dashboard's watchlist, so the CONTRIBUTION column stays empty; "+
				"`upkeep modules:add` to watch it")
		}
	}

	return strings.Join(segments, " · ") + "\n" +
		"Run the command in NEXT for any row · upkeep explain <term> for what a column means"
}

// selectedStatuses is which open statuses the scan covers.
//
// An unrecognised --status falls back to every open status rather than
// refusing: the flag narrows a listing, and answering a typo with an empty
// table would read as "this module has no active issues".
func selectedStatuses(cmd *cobra.Command) []drupal.IssueStatus {
	requested := strings.ToLower(cli.Flag(cmd, "status"))
	if requested == "" {
		return drupal.OpenStatuses()
	}

	// Every status wearing that label, not the first: two of drupal.org's
	// statuses are both called "postponed" — plain, and "needs more info" —
	// and a scan that took one of them would quietly omit half the postponed
	// queue.
	matched := make([]drupal.IssueStatus, 0, 2)
	for _, status := range drupal.OpenStatuses() {
		if strings.ToLower(statusFlagName(status)) == requested {
			matched = append(matched, status)
		}
	}
	if len(matched) > 0 {
		return matched
	}

	return drupal.OpenStatuses()
}

// statusFlagName is how a status is spelled on the command line: its own short
// label, hyphenated, so the word in the table and the word in the flag are the
// same word.
//
// Matched case-insensitively, which the PHP is not: there the label is
// compared against a lowercased argument, so "RTBC" can never equal "rtbc" and
// `--status=rtbc` — which the help text documents — silently selects every
// open status instead. Silently, because an unrecognised value falls back to
// the full scan rather than refusing. Verified against the real PHP class: no
// spelling of rtbc matches, and every other status does.
func statusFlagName(status drupal.IssueStatus) string {
	return strings.ReplaceAll(status.ShortLabel(), " ", "-")
}

// registerStatusCompletion suggests the statuses, which are the one set of
// values this tool can enumerate without a network.
//
// Deduplicated, because two of drupal.org's open statuses share the short
// label "postponed" — and a prompt offering the same word twice reads as a
// bug in the tool rather than as a fact about drupal.org. Selecting it takes
// both, which is what the flag does.
func registerStatusCompletion(cmd *cobra.Command) {
	_ = cmd.RegisterFlagCompletionFunc("status",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			seen := map[string]bool{}
			names := make([]string, 0, len(drupal.OpenStatuses()))
			for _, status := range drupal.OpenStatuses() {
				if name := statusFlagName(status); !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
			sort.Strings(names)

			return names, cobra.ShellCompDirectiveNoFileComp
		})
}

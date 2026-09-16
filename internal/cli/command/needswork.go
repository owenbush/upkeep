package command

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewNeedsWork builds the needs-work command.
//
// Posts the local check results as a structured comment on the merge request
// and opens the linked drupal.org issue, where the status change to "Needs
// work" is a browser action — the drupal.org API is read-only.
//
// It posts evidence somebody else will read and act on, which is why it never
// runs the checks itself: the comment says what was actually recorded, and a
// run that both produced and published a verdict in one step would make
// "where did this come from?" unanswerable.
func NewNeedsWork(clients cli.GitlabClients, browser cli.Browser) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "needs-work <module> <mr>",
		Short: "Post local check results as a comment on the merge request",
		Long: `Posts the cached check results for a merge request as a comment, then opens
its drupal.org issue so the status can be set to Needs work.

  upkeep check pathauto 42        record the results
  upkeep needs-work pathauto 42   publish them

Posts what was recorded; it never runs the checks itself. --dry-run prints
the comment without posting it.`,
		Args: cobra.ExactArgs(2),
	}
	addMrSurface(cmd)
	cli.AddNoOpen(cmd)
	cmd.Flags().Bool("dry-run", false, "Print the comment without posting it")
	cli.AddModuleCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runNeedsWork(cmd, clients, browser, args[0], args[1])
	})

	return cmd
}

func runNeedsWork(
	cmd *cobra.Command, clients cli.GitlabClients, browser cli.Browser, moduleName, rawIID string,
) (int, error) {
	// The strict path, before the resolution: this command's whole purpose is
	// to write, and a credential error after two round trips is two round
	// trips wasted.
	client, err := cli.WritingClient(cmd, clients)
	if err != nil {
		return 0, err
	}

	where, context, err := resolveMrContextWith(cmd, client, moduleName, rawIID)
	if err != nil {
		return 0, err
	}

	cached, err := evidenceFor(cmd, where, context)
	if err != nil {
		return 0, err
	}

	comment := needsWorkComment(cached, context.CoreMajor, context.MergeRefSHA != "")

	if cli.Switched(cmd, "dry-run") {
		cli.Progressf(cmd, "Comment preview")
		cli.Println(cmd, comment)

		return workflow.OK, nil
	}

	if failure := client.PostNote(context.Project, context.MergeRequest.IID, comment); failure != nil {
		return 0, fmt.Errorf("could not post comment on !%d [%s]: %s",
			context.MergeRequest.IID, failure.ShortCode(), failure.Message)
	}

	cli.Progressf(cmd, "Comment posted on !%d — %s",
		context.MergeRequest.IID, context.MergeRequest.WebURL)

	openIssueForStatus(cmd, browser, context.MergeRequest)

	return workflow.OK, nil
}

// evidenceFor is the cached check result this comment publishes.
//
// Looked up by the *merge revision* — the merge ref's SHA where GitLab
// publishes one, falling back to the head SHA where it does not — because that
// is the key `check` filed it under, and it is what the evidence is actually
// about: the branch merged into the current tip of its target, which is the
// tree CI analyses.
//
// The PHP looks it up by the head SHA alone. Since a merge ref exists for
// every merge request that does not conflict with its target, that lookup
// misses on the ordinary case, falls through to the newest result for the
// merge request, and then declares it stale because its SHA is not the head
// SHA — which it never was. Demonstrated against the real PHP classes: a
// healthy run warns "results were recorded against SHA b…; the MR is now at
// a…" and posts a public comment naming a SHA that is not the branch head.
func evidenceFor(
	cmd *cobra.Command, where *cockpit.Cockpit, context workflow.MrContext,
) (*results.CachedResult, error) {
	cache := results.NewCache(where.ResultsPath())
	key, err := results.MergeRequestKey(context.MergeRequest.IID)
	if err != nil {
		return nil, err
	}

	revision := gitlab.MergeRevision(context.MergeRefSHA, context.MergeRequest.HeadSHA)

	if revision != "" {
		if current := cache.Find(context.Module.Name, key, context.CoreMajor, revision); current != nil {
			return current, nil
		}
	}

	// Nothing for the tree as it stands now. The newest result for this merge
	// request is still worth publishing — a maintainer who ran the checks
	// before the last push has evidence, and saying so is better than refusing
	// — but it is about a different tree and the comment says which.
	latest, err := cache.Latest(context.Module.Name, key, context.CoreMajor)
	if err != nil {
		return nil, err
	}
	if latest == nil {
		return nil, fmt.Errorf(
			"no cached check results for %s !%d (core %s). Run `upkeep check %s %d --version=%s` first",
			context.Module.Name, context.MergeRequest.IID, context.CoreMajor,
			context.Module.Name, context.MergeRequest.IID, context.CoreMajor,
		)
	}

	if revision != "" && latest.SHA != revision {
		cli.Warnf(cmd,
			"Results were recorded against %s; the merge request is now at %s. "+
				"The comment will note the older revision.",
			shortSHA(latest.SHA), shortSHA(revision))
	}

	return latest, nil
}

// openIssueForStatus opens the drupal.org issue, where the status change is a
// browser action.
//
// Nothing to open is ordinary and silent: a merge request claiming no issue is
// a third of pathauto's, and the comment has already been posted.
func openIssueForStatus(cmd *cobra.Command, browser cli.Browser, mergeRequest gitlab.MergeRequest) {
	nid, found := drupal.ExtractIssue(
		mergeRequest.Title, mergeRequest.SourceBranch, mergeRequest.Description)
	if !found || cli.Switched(cmd, cli.FlagNoOpen) {
		return
	}

	issueURL := drupal.IssueURL(nid)
	if browser.Open(issueURL) {
		cli.Progressf(cmd, "Opened drupal.org issue #%d — set the status to Needs work.", nid)

		return
	}
	cli.Progressf(cmd, "Set the issue status at: %s", issueURL)
}

// needsWorkComment is the markdown posted to the merge request.
//
// Markdown rather than prose, because it is read on a web page by somebody who
// did not run the checks: a table says at a glance which check failed, and the
// output of each failure is folded away so a long phpcs run does not bury the
// summary.
func needsWorkComment(cached *results.CachedResult, coreMajor string, againstMergeRef bool) string {
	// Named for what it is. The merge ref is not the branch head, and a reader
	// who goes looking for this SHA in the branch will not find it — which is
	// exactly the confusion that made the PHP's staleness warning wrong.
	revision := "SHA"
	if againstMergeRef {
		revision = "merge ref"
	}

	lines := []string{
		"### upkeep local check results",
		"",
		fmt.Sprintf("Core: %s · %s: `%s` · Recorded: %s",
			coreMajor, revision, shortSHA(cached.SHA),
			cached.RecordedAt.UTC().Format("2006-01-02 15:04")+" UTC"),
		"",
		"| Check | Status | Duration |",
		"|-------|--------|----------|",
	}

	var failed []check.Result
	for _, result := range cached.Result.Results {
		duration := "—"
		if seconds := result.Duration.Seconds(); seconds > 0 {
			duration = fmt.Sprintf("%.1fs", seconds)
		}
		lines = append(lines, fmt.Sprintf("| %s | %s | %s |",
			result.Type, commentStatuses[result.Status], duration))

		if result.Status == check.Failed {
			failed = append(failed, result)
		}
	}

	for _, result := range failed {
		excerpt := result.OutputExcerpt(0)
		if excerpt == "" {
			continue
		}
		lines = append(lines,
			"",
			fmt.Sprintf("<details><summary>%s output</summary>", result.Type),
			"",
			"```",
			excerpt,
			"```",
			"",
			"</details>",
		)
	}

	lines = append(lines,
		"",
		"---",
		"*Posted via [upkeep](https://github.com/owenbush/upkeep)*",
	)

	return strings.Join(lines, "\n")
}

// commentStatuses is a check's outcome as the comment spells it.
//
// A failure is the only one emphasised, because it is the only one the comment
// exists to report — and "no tests" and "unavailable" are said in their own
// words rather than folded into a pass, since neither is evidence the module
// works.
//
// A lookup rather than a switch, and with no fallback arm: the cache refuses a
// status this build does not know, so a status reaching here that is not on
// check.Statuses() cannot happen — and a fallback for it would be a branch no
// test could ever reach. A test holds the table against that list instead, so
// a status added without a word here fails rather than printing a blank cell.
var commentStatuses = map[check.Status]string{
	check.Failed:      "**FAIL**",
	check.Passed:      "pass",
	check.NoTests:     "no tests",
	check.Unavailable: "unavailable",
}

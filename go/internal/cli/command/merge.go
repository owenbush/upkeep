package command

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/config"
	"github.com/owenbush/upkeep/internal/dashboard"
	"github.com/owenbush/upkeep/internal/gate"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
	"github.com/owenbush/upkeep/internal/workflow"
)

// The three answers a prompt accepts. "skip" is first and so is the default:
// a bare Enter, an unrecognised reply, or standard input ending mid-question
// all leave the merge request alone. Nothing merges without somebody typing
// the word.
var mergeAnswers = []string{"skip", "merge", "quit"}

// NewMerge builds the merge command.
//
// The fast-lane merge: it presents the current READY-AUTO rows one at a time
// and, on an explicit per-merge-request approval, performs that single merge —
// one individual action the maintainer could have done in the browser.
//
// The one-approval stance is structural rather than a convention. There is one
// prompt, one approval and one API call per merge request; no batch mode, no
// flag that merges without asking, and no way to answer once for several rows.
// Immediately before each merge the merge request is re-fetched with an
// unmemoised client, and anything that moved demotes the row with its reasons
// instead of merging. The merge call itself carries the expected head SHA, so
// GitLab rejects races the re-check cannot see.
func NewMerge(clients cli.GitlabClients, prompts func(*cobra.Command) cli.Prompt) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "merge --fast-lane",
		Short: "Fast-lane merge: prompt per READY-AUTO merge request",
		Long: `Walks the current READY-AUTO rows one at a time, asking for an explicit
approval per merge request.

  upkeep merge --fast-lane

Only bot compatibility MRs with green CI and green local checks are ever
offered. There is no batch mode and no unattended flag, by Drupal Association
policy — see the README's policy stance.`,
		Args: cobra.NoArgs,
	}
	cmd.Flags().Bool("fast-lane", false,
		"Required: run the fast-lane loop (the only mode; named explicitly because it performs merges)")
	cli.AddCockpit(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, _ []string) (int, error) {
		return runMerge(cmd, clients, prompts(cmd))
	})

	return cmd
}

// tally is what a fast-lane run did.
type tally struct {
	merged            int
	handedToBrowser   int
	skipped           int
	demoted           int
	failed            int
	credentialFailure int
}

// outcome is the exit code a run answers with.
//
// A run where every merge failed used to exit 0. Failed merges are the work
// reporting failure; a rejected credential is a setup problem and outranks
// them, because every remaining row would fail the same way.
func (t tally) outcome() int {
	switch {
	case t.credentialFailure > 0:
		return workflow.Infrastructure
	case t.failed > 0:
		return workflow.Failed
	default:
		return workflow.OK
	}
}

func runMerge(cmd *cobra.Command, clients cli.GitlabClients, prompt cli.Prompt) (int, error) {
	if !cli.Switched(cmd, "fast-lane") {
		// Bad usage is an infrastructure outcome: no merge was attempted, so
		// there is no verdict about any merge request to report.
		return 0, errors.New(
			"the merge command only operates in fast-lane mode; re-run as `upkeep merge --fast-lane`. " +
				"It will still prompt per MR — the flag names the workflow, it never skips approval",
		)
	}

	where, err := cli.Cockpit(cmd)
	if err != nil {
		return 0, err
	}
	modules, err := cli.Modules(where)
	if err != nil {
		return 0, err
	}

	// Merging writes, so this is the strict path: without a credential nothing
	// can be classified, let alone merged.
	client, err := cli.WritingClient(cmd, clients)
	if err != nil {
		return 0, err
	}

	cache := results.NewCache(where.ResultsPath())
	rows := dashboard.NewAssembler(
		client, cache, gate.NewFastLane(config.BotPatternForCore),
	).Assemble(modules, "")

	// Resolved during the partition rather than inside the loop. A READY-AUTO
	// row always has a merge request and a project — that is what having a
	// verdict means — so asking again further down would be a refusal that
	// cannot happen, and an unreachable branch is one nothing can hold to
	// being right. Anything that somehow lacks them is simply not offered.
	var ready []offer
	var rest []dashboard.Row
	for _, row := range rows {
		candidate, eligible := offerFor(row)
		if !eligible {
			rest = append(rest, row)

			continue
		}
		ready = append(ready, candidate)
	}

	// Listed, never offered: seeing what the fast lane will not touch is how a
	// maintainer knows the lane is narrow rather than broken.
	if len(rest) > 0 {
		cli.Println(cmd, "Needs a human (never offered for merge)")
		for _, row := range rest {
			cli.Println(cmd, describeNonActionable(row))
		}
		cli.Println(cmd, "")
	}

	if len(ready) == 0 {
		cli.Println(cmd, "No READY-AUTO rows — nothing eligible for fast-lane merge.")

		return workflow.OK, nil
	}

	counts := tally{}

	if !prompt.Interactive() {
		// No terminal means no explicit per-merge-request affirmative is
		// possible, and without one nothing merges — ever. The rows are
		// reported as skipped rather than silently consuming defaults.
		cli.Println(cmd,
			"Approval requires an interactive terminal: every merge needs an explicit per-MR "+
				"\"merge\" answer, so a non-interactive run merges nothing.")
		for _, candidate := range ready {
			counts.skipped++
			cli.Printf(cmd, "Skipped %s !%d (no interactive approval possible).\n",
				candidate.row.Module, candidate.mergeRequest.IID)
		}
		renderTally(cmd, counts)

		return workflow.OK, nil
	}

	for _, candidate := range ready {
		renderMergeContext(cmd, candidate.row, candidate.mergeRequest)

		switch prompt.Choose(
			fmt.Sprintf("Fast-lane action for %s !%d",
				candidate.row.Module, candidate.mergeRequest.IID),
			mergeAnswers,
		) {
		case "quit":
			cli.Println(cmd, "Quit — leaving the remaining rows untouched.")
			renderTally(cmd, counts)

			return counts.outcome(), nil

		case "merge":
			mergeOne(cmd, client, cache, candidate, &counts)

		default:
			counts.skipped++
			cli.Printf(cmd, "Skipped %s !%d.\n",
				candidate.row.Module, candidate.mergeRequest.IID)
		}
	}

	renderTally(cmd, counts)

	return counts.outcome(), nil
}

// offer is a row the fast lane may present, with everything the prompt and the
// merge need already resolved.
type offer struct {
	row          dashboard.Row
	mergeRequest gitlab.MergeRequest
	project      gitlab.Project
}

// offerFor is the offer a row makes, or false when the lane must not present
// it.
func offerFor(row dashboard.Row) (offer, bool) {
	if !row.IsReadyAuto() {
		return offer{}, false
	}

	mergeRequest, err := row.RequireMergeRequest()
	if err != nil {
		return offer{}, false
	}
	project, err := row.RequireProject()
	if err != nil {
		return offer{}, false
	}

	return offer{row: row, mergeRequest: mergeRequest, project: project}, true
}

// mergeOne is one approved merge: the freshness re-check against a live
// re-fetch, then the single API call with the head-SHA guard.
func mergeOne(
	cmd *cobra.Command,
	client *gitlab.Client,
	cache *results.Cache,
	candidate offer,
	counts *tally,
) {
	row, mergeRequest, project := candidate.row, candidate.mergeRequest, candidate.project

	// Re-fetched with an unmemoised client so this is the merge request as it
	// is *now*, not as it was classified at row-assembly time.
	fresh, failure := client.Fresh().MergeRequest(project, mergeRequest.IID)
	if failure != nil {
		counts.demoted++
		cli.Printf(cmd, "Demoted %s !%d — freshness re-check failed (%s); not merging.\n",
			row.Module, mergeRequest.IID, failure.Message)

		return
	}

	if reasons := demotionReasons(client, cache, row, mergeRequest, *fresh, project); len(reasons) > 0 {
		counts.demoted++
		cli.Printf(cmd, "Demoted %s !%d to REVIEW (%s) — not merging.\n",
			row.Module, mergeRequest.IID, strings.Join(reasons, ", "))

		return
	}

	merged, failure := client.Merge(project, mergeRequest.IID, fresh.HeadSHA)
	if failure == nil {
		counts.merged++
		cli.Printf(cmd, "Merged %s !%d (state: %s).\n", row.Module, mergeRequest.IID, merged.State)

		return
	}

	reportMergeFailure(cmd, row, mergeRequest.IID, failure, counts)
}

// demotionReasons is why an approved merge must not go ahead after all.
//
// Re-classified against the fresh merge request and re-read local evidence,
// with the same conservative logic that admitted the row: this catches CI
// regression, a new draft marker, and evidence that has gone stale since the
// prompt was printed.
func demotionReasons(
	client *gitlab.Client,
	cache *results.Cache,
	row dashboard.Row,
	classified, fresh gitlab.MergeRequest,
	project gitlab.Project,
) []string {
	var reasons []string

	if fresh.State != "opened" {
		reasons = append(reasons, "state-changed:"+fresh.State)
	}
	if fresh.HeadSHA != classified.HeadSHA {
		reasons = append(reasons, "sha-drift")
	}

	cores := row.Local.Cores()
	key, err := results.MergeRequestKey(classified.IID)
	if err != nil {
		return append(reasons, "unusable-result-key")
	}

	byCore := map[string]*results.CachedResult{}
	for _, core := range cores {
		latest, err := cache.Latest(row.Module, key, core)
		if err != nil {
			// An unreadable results directory is not "never checked": it is a
			// question that could not be asked, and merging on the strength of
			// one is exactly what this re-check exists to stop.
			return append(reasons, "evidence-unreadable")
		}
		byCore[core] = latest
	}

	// The merge ref too, not just the merge request: the tree that was checked
	// is the branch merged into the target, so a commit landing on the
	// *target* between the check and this prompt makes the evidence about
	// something else — while the head SHA sits perfectly still.
	revision := gitlab.MergeRevision(client.Fresh().MergeRefSHA(project, classified.IID), fresh.HeadSHA)

	verdict := gate.NewFastLane(config.BotPatternForCore).
		Classify(fresh, cores, results.Evidence(byCore, revision))
	if verdict.Status != gate.ReadyAuto {
		reasons = append(reasons, verdict.Reasons...)
	}

	return unique(reasons)
}

// reportMergeFailure says what went wrong, and how to do it by hand.
func reportMergeFailure(
	cmd *cobra.Command, row dashboard.Row, iid int, failure *gitlab.Failure, counts *tally,
) {
	if failure.Kind == gitlab.EndpointClosed {
		// The documented degraded path: the instance refuses API merges — a
		// policy decision, not a broken credential — so the approved action
		// becomes handing the maintainer the exact browser URL to perform it
		// there.
		counts.handedToBrowser++
		cli.Println(cmd, "GitLab refuses API merges here (HTTP 403).")
		cli.Printf(cmd, "Merge in the browser: %s\n", failure.BrowserURL)
		cli.Printf(cmd, "Marked %s !%d handled-manually.\n", row.Module, iid)

		return
	}

	if failure.Kind == gitlab.Unauthorized {
		// A rejected credential is not a verdict about this merge request: it
		// is the operator's token, and every remaining row would fail the same
		// way.
		counts.credentialFailure++
	} else {
		counts.failed++
	}

	cli.Printf(cmd, "Merge failed for %s !%d [%s]: %s\n",
		row.Module, iid, failure.ShortCode(), failure.Message)
	// Every failure carries the browser URL where one is known, so the manual
	// fallback is offered whatever went wrong.
	if failure.BrowserURL != "" {
		cli.Printf(cmd, "Merge in the browser instead: %s\n", failure.BrowserURL)
	}
}

// renderMergeContext is what the maintainer is approving.
func renderMergeContext(cmd *cobra.Command, row dashboard.Row, mergeRequest gitlab.MergeRequest) {
	cores := row.Local.Cores()
	noun := "cores"
	if len(cores) == 1 {
		noun = "core"
	}
	listed := "none"
	if len(cores) > 0 {
		listed = strings.Join(cores, ",")
	}

	head := mergeRequest.HeadSHA
	if head == "" {
		head = "unknown"
	}

	cli.Printf(cmd, "\n%s !%d (%s %s) — READY-AUTO\n", row.Module, mergeRequest.IID, noun, listed)
	cli.Printf(cmd, "  Title:  %s\n", mergeRequest.Title)
	cli.Printf(cmd, "  Branch: %s -> %s\n", mergeRequest.SourceBranch, mergeRequest.TargetBranch)
	cli.Printf(cmd, "  Head:   %s\n", head)

	pipeline := ""
	if mergeRequest.HeadPipeline != nil && mergeRequest.HeadPipeline.WebURL != "" {
		pipeline = " — " + mergeRequest.HeadPipeline.WebURL
	}
	cli.Printf(cmd, "  CI:     %s%s\n", row.CICell(), pipeline)

	recorded := ""
	if at := row.Local.LatestRecordedAt(); !at.IsZero() {
		recorded = " (recorded " + at.Format(time.RFC3339) + ")"
	}
	cli.Printf(cmd, "  Local:  %s%s\n", row.LocalCell(), recorded)
	cli.Printf(cmd, "  URL:    %s\n", mergeRequest.WebURL)
}

// renderTally is what the run did.
func renderTally(cmd *cobra.Command, counts tally) {
	cli.Println(cmd, "")
	cli.Println(cmd, "Fast-lane summary")
	cli.Printf(cmd, "  Merged: %d\n", counts.merged)
	cli.Printf(cmd, "  Handed to browser: %d\n", counts.handedToBrowser)
	cli.Printf(cmd, "  Skipped: %d\n", counts.skipped)
	cli.Printf(cmd, "  Demoted: %d\n", counts.demoted)
	cli.Printf(cmd, "  Failed: %d\n", counts.failed+counts.credentialFailure)
}

// describeNonActionable is one row the fast lane will not offer.
func describeNonActionable(row dashboard.Row) string {
	if row.MergeRequest == nil {
		return fmt.Sprintf("  %s: merge requests unavailable — %s", row.Module, row.StatusCell())
	}

	return fmt.Sprintf("  %s !%d (%s): %s — %s",
		row.Module, row.MergeRequest.IID, row.Branch, row.StatusCell(),
		dashboard.Truncate(row.MergeRequest.Title, 0))
}

// unique keeps the first of each reason, so a row demoted for two overlapping
// reasons does not say the same thing twice.
func unique(values []string) []string {
	seen := map[string]bool{}
	kept := values[:0]
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			kept = append(kept, value)
		}
	}

	return kept
}

package patches

import (
	"fmt"
	"time"

	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
)

// Contribution is one drupal.org issue together with every merge request that
// claims authorship of it — the unit `upkeep patches` classifies and renders.
//
// Both halves are kept because an issue can hold both kinds of contribution at
// once, which is normal in the Drupal community: a patch posted in comment 4,
// a merge request opened in comment 9, and a re-roll posted in comment 14 that
// never made it onto the branch.
type Contribution struct {
	Module string
	Issue  drupal.Issue
	// MergeRequests is every merge request whose metadata asserts authorship
	// of this issue.
	MergeRequests []gitlab.MergeRequest
}

// Pair matches each issue with the merge requests that claim authorship of it.
//
// Shared by every consumer that has to decide what a contribution *is* — the
// patch report and the dashboard — so the two can never disagree about whether
// an issue is covered.
//
// Merge requests claiming an issue outside issues are dropped as they are
// grouped. That is not observable in the result — only the given issues get a
// contribution either way — it just keeps the grouping bounded by the issues
// asked about rather than by however many merge requests the project has.
//
// forkNids maps source-project id to issue nid. It is the authoritative
// pairing where it has an answer: drupal.org made that fork *for* that issue,
// which is a fact about how the repository exists rather than a string in a
// title.
func Pair(
	module string,
	issues []drupal.Issue,
	mergeRequests []gitlab.MergeRequest,
	forkNids map[int]int,
) []Contribution {
	wanted := make(map[int]bool, len(issues))
	for _, issue := range issues {
		wanted[issue.Nid] = true
	}

	byNid := map[int][]gitlab.MergeRequest{}
	for _, mr := range mergeRequests {
		// The fork wins. It is the only thing that pairs a Project Update Bot
		// merge request at all: those are titled "Automated Project Update Bot
		// fixes" on a branch called project-update-bot-only, and say only
		// "Relates to #NNN" — which ExtractOwningIssue rejects by design, so a
		// bot cannot suppress an issue's patches by mentioning it.
		nid, found := 0, false
		if mr.SourceProjectID != 0 {
			nid, found = forkNids[mr.SourceProjectID]
		}
		if !found {
			nid, found = drupal.ExtractOwningIssue(mr.Title, mr.SourceBranch, mr.Description)
		}
		if !found || !wanted[nid] {
			continue
		}
		byNid[nid] = append(byNid[nid], mr)
	}

	contributions := make([]Contribution, 0, len(issues))
	for _, issue := range issues {
		contributions = append(contributions, Contribution{
			Module: module, Issue: issue, MergeRequests: byNid[issue.Nid],
		})
	}

	return contributions
}

// Landed is the merge request whose work has landed, if one has.
//
// The question this whole surface exists to answer for a class of issue that
// cannot answer it itself: Project Update Bot compatibility issues are kept
// open on purpose, so the bot can post again as core moves, and an open one
// may already have had its work merged months ago. Two such issues look
// identical until you ask whether anything was merged.
func (c Contribution) Landed() *gitlab.MergeRequest {
	return gitlab.LatestMerged(c.MergeRequests)
}

// HasWorkNewerThanLanding reports whether anything on the issue is newer than
// the merge.
//
// The discriminator, and the reason this never says "resolved". A bot that
// posts again after its earlier work merged has raised new work; an issue
// where nothing has happened since is one whose open status is only the
// convention. Both are true statements about evidence, and which of them
// warrants closing the issue stays the maintainer's call.
func (c Contribution) HasWorkNewerThanLanding() bool {
	landed := c.Landed()
	if landed == nil || landed.MergedAt == "" {
		return false
	}
	mergedAt, ok := parseTimestamp(landed.MergedAt)
	if !ok {
		return false
	}

	for _, file := range c.Issue.Files {
		if file.Timestamp > mergedAt.Unix() {
			return true
		}
	}
	for _, mr := range c.MergeRequests {
		if mr.State == "merged" || mr.UpdatedAt == "" {
			continue
		}
		if updated, ok := parseTimestamp(mr.UpdatedAt); ok && updated.After(mergedAt) {
			return true
		}
	}

	return false
}

// parseTimestamp reads the shapes GitLab sends. PHP's strtotime takes
// essentially anything; Go needs the accepted forms listed, so the ones the
// API actually uses come first.
func parseTimestamp(raw string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}

	return time.Time{}, false
}

// SubstantiveMergeRequests is the merge requests that carry changes.
//
// One whose emptiness is *unknown* counts as substantive. Unknown is the
// reading a merge-request list payload gives (it omits diff_refs) and the
// reading a snapshot cached by an older upkeep gives, and in both cases the
// safe direction is the one that preserves the pre-existing behaviour — treat
// the merge request as real work — rather than one that invents empty merge
// requests out of missing data.
func (c Contribution) SubstantiveMergeRequests() []gitlab.MergeRequest {
	return Substantive(c.MergeRequests)
}

// Substantive filters out only the merge requests *known* to be empty.
func Substantive(mergeRequests []gitlab.MergeRequest) []gitlab.MergeRequest {
	substantive := []gitlab.MergeRequest{}
	for _, mr := range mergeRequests {
		if mr.CarriesChanges() != gitlab.No {
			substantive = append(substantive, mr)
		}
	}

	return substantive
}

// Kind is how this issue's work has been delivered.
func (c Contribution) Kind() Kind {
	hasPatches := c.Issue.PatchCount() > 0
	substantive := len(c.SubstantiveMergeRequests()) > 0

	if !hasPatches {
		if substantive {
			return MergeRequestOnly
		}

		return Nothing
	}
	if substantive {
		return PatchAndMergeRequest
	}
	if len(c.MergeRequests) == 0 {
		return PatchOnly
	}

	return PatchWithEmptyMergeRequest
}

// CurrentRevision is the revision a cached check result must name to be
// current for this issue: the newest patch on it.
//
// Empty when the issue carries no patch at all, in which case there is nothing
// a result could be about.
func (c Contribution) CurrentRevision() string {
	latest, found := c.Issue.LatestPatch()
	if !found {
		return ""
	}

	return Revision(latest.URL)
}

// DashboardStatus is the STATUS cell for a patch row: what arrived, and how
// much of it.
//
// Deliberately not a gate verdict — nothing here is mergeable, and a cell that
// looked like one would invite the wrong action.
func (c Contribution) DashboardStatus() string {
	files := fmt.Sprintf("%d patches", c.Issue.PatchCount())
	if c.Issue.PatchCount() == 1 {
		files = "1 patch"
	}

	switch c.Kind() {
	case PatchOnly:
		return "PATCH " + files
	case PatchAndMergeRequest, PatchWithEmptyMergeRequest:
		return "PATCH " + files + ", " + c.MergeRequestCell()
	case MergeRequestOnly:
		return "PATCH covered by " + c.MergeRequestCell()
	default:
		return "PATCH nothing attached"
	}
}

// MergeRequestCell is the MR column: the representative merge request, flagged
// when it carries nothing, with a count of any others.
//
// A substantive merge request represents the issue in preference to an empty
// one — an issue can carry both a real branch and a bot's empty draft, and the
// real branch is the answer to "is this already in git?".
func (c Contribution) MergeRequestCell() string {
	return RenderMergeRequestCell(c.MergeRequests, c.Landed(), c.HasWorkNewerThanLanding())
}

// RenderMergeRequestCell is the MR column, from merge requests alone.
//
// A free function because the dashboard row renders the same cell and only
// sometimes holds an issue: a merge request whose issue is closed, or outside
// the snapshot's queue, still has an MR column and no contribution to ask. One
// implementation, so the two views cannot describe the same merge requests
// differently.
func RenderMergeRequestCell(
	mergeRequests []gitlab.MergeRequest,
	landed *gitlab.MergeRequest,
	newerWorkSinceLanding bool,
) string {
	if len(mergeRequests) == 0 {
		return "–"
	}

	// A landing outranks everything else the cell could say. An open issue
	// whose work is already merged is the one case a maintainer cannot read
	// off the issue at all, and it is the commonest shape of a Project Update
	// Bot compatibility issue, which convention keeps open so the bot can post
	// again.
	if landed != nil {
		since := ""
		if newerWorkSinceLanding {
			since = ", newer work since"
		}
		mergedAt := landed.MergedAt
		if len(mergedAt) > 10 {
			mergedAt = mergedAt[:10]
		}

		return fmt.Sprintf("!%d merged %s%s", landed.IID, mergedAt, since)
	}

	representative := mergeRequests[0]
	if substantive := Substantive(mergeRequests); len(substantive) > 0 {
		representative = substantive[0]
	}

	cell := fmt.Sprintf("!%d", representative.IID)
	if representative.CarriesChanges() == gitlab.No {
		cell += " empty"
	}
	if others := len(mergeRequests) - 1; others > 0 {
		cell += fmt.Sprintf(" +%d", others)
	}

	return cell
}

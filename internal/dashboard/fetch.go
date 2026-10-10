package dashboard

import (
	"time"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
)

// SnapshotReader is the GitLab surface a refresh needs.
//
// Wider than the assembler's, because a snapshot answers more questions: which
// merge requests have already landed, which fork belongs to which issue, and
// what each branch declares about core. Still strictly read-only, and the type
// says so.
type SnapshotReader interface {
	Reader

	MergedMergeRequests(project gitlab.Project, limit int) ([]gitlab.MergeRequest, *gitlab.Failure)
	IssueForkNids(project gitlab.Project) (map[int]int, *gitlab.Failure)
	FileContents(project gitlab.Project, path, ref string) string
}

// IssueReader is the drupal.org surface a refresh needs.
type IssueReader interface {
	ProjectIssues(machineName string, statuses []drupal.IssueStatus) []drupal.Issue
}

// mergedLimit is how far back a landing search goes.
//
// Enough to answer "has this issue's work already landed?" for anything still
// open, without walking a decade of history on every refresh.
const mergedLimit = 100

// FetchModule is one module's snapshot, straight from the network.
//
// The expensive half of a refresh, and the reason the result is cached: a
// GitLab round trip per merge request plus a drupal.org scan whose attachment
// lookups are one request per file.
func FetchModule(
	client SnapshotReader, issues IssueReader, module cockpit.Module, now time.Time,
) (ModuleSnapshot, *gitlab.Failure) {
	project, failure := client.Project(module.Project)
	if failure != nil {
		return ModuleSnapshot{}, failure
	}

	listed, failure := client.OpenMergeRequests(*project)
	if failure != nil {
		return ModuleSnapshot{}, failure
	}

	branches := map[string]bool{}
	if project.DefaultBranch != "" {
		branches[project.DefaultBranch] = true
	}

	mrData := make([]map[string]any, 0, len(listed))
	mergeRefSHAs := map[int]string{}
	for _, one := range listed {
		// The list payload lacks the head pipeline; the single-merge-request
		// endpoint provides it. A failed detail fetch falls back to the listed
		// data, so the row still renders and the gate — seeing no pipeline —
		// conservatively denies READY-AUTO.
		mergeRequest := one
		if detail, failure := client.MergeRequest(*project, one.IID); failure == nil {
			mergeRequest = *detail
		}
		mrData = append(mrData, mergeRequest.ToAPIMap())
		branches[mergeRequest.TargetBranch] = true

		// What a check of this merge request is actually about: the branch
		// merged into the current tip of its target, which is the tree CI
		// analyses and the one upkeep checks out. Keyed on the head SHA alone,
		// evidence would stay "current" after the *target* moved.
		if sha := client.MergeRefSHA(*project, mergeRequest.IID); sha != "" {
			mergeRefSHAs[mergeRequest.IID] = sha
		}
	}

	// Merged ones too. Without them an issue whose work has already landed
	// reads exactly like one nobody has touched — and a promoted patch that
	// was fixed and merged leaves the bot's draft behind, looking like the
	// only contribution there is.
	mergedData := []map[string]any{}
	if merged, failure := client.MergedMergeRequests(*project, mergedLimit); failure == nil {
		for _, one := range merged {
			mergedData = append(mergedData, one.ToAPIMap())
			branches[one.TargetBranch] = true
		}
	}

	forkNids, failure := client.IssueForkNids(*project)
	if failure != nil {
		// The only thing that pairs a bot merge request to its issue, so
		// losing it costs the pairing and nothing else. A row still renders.
		forkNids = map[int]int{}
	}

	// Every *open* issue, not only the two statuses a contribution sits in.
	// One snapshot serves both questions a maintainer asks — "what is waiting
	// for me?" and "what could I work on?" — and each consumer narrows it.
	issueData := []map[string]any{}
	for _, issue := range issues.ProjectIssues(module.Name, drupal.OpenStatuses()) {
		issueData = append(issueData, issue.ToAPIMap())
	}

	return ModuleSnapshot{
		FetchedAt:       now,
		ProjectData:     project.ToAPIMap(),
		MRData:          mrData,
		PatchIssueData:  issueData,
		MergedMRData:    mergedData,
		ForkNids:        forkNids,
		CoreConstraints: coreConstraints(client, *project, module, branches),
		MergeRefSHAs:    mergeRefSHAs,
	}, nil
}

// coreConstraints is what each branch declares about core, read from its own
// info.yml.
//
// One request per branch a row could sit on, which on a real module is one or
// two — measured on pathauto, every open and merged merge request targets
// 8.x-1.x.
//
// Every failure is silent and falls back to the tracked cores: a branch with
// no info.yml at that path, a closed endpoint, a module whose machine name is
// not its project path. Missing evidence about a branch is not evidence that
// the branch supports nothing, and a module vanishing from the dashboard is
// the worst failure this tool has.
func coreConstraints(
	client SnapshotReader, project gitlab.Project, module cockpit.Module, branches map[string]bool,
) map[string]string {
	constraints := map[string]string{}
	for branch := range branches {
		info := client.FileContents(project, module.Name+".info.yml", branch)
		if constraint := drupal.ConstraintIn(info); constraint != "" {
			constraints[branch] = constraint
		}
	}

	return constraints
}

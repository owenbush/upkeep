package dashboard

import (
	"sort"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gate"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/patches"
	"github.com/owenbush/upkeep/internal/results"
)

// EvidenceStore is what the factory reads local check results through.
//
// An interface rather than the concrete cache, because the factory is pure
// classification and the only thing it needs from disk is "what was recorded
// for this subject on this core".
type EvidenceStore interface {
	Latest(module string, key results.Key, coreMajor string) (*results.CachedResult, error)
}

// RowFactory is the one classification pipeline: (module, project, merge
// requests, issues) to dashboard rows, one per (issue, branch), classified by
// the fast-lane gate.
//
// It takes merge requests it did not fetch, precisely so both consumers can
// use it: the assembler feeds it live client data, and the dashboard feeds it
// its cached per-module snapshot. Neither re-derives status — a drift here
// would misclassify merge eligibility, which is the one thing the gate says
// must not happen.
//
// **The tracked core versions are no longer a row multiplier.** They are the
// cores a row's evidence is gathered on, which is what they always meant: a
// module branch supports several at once, so a row per (subject x core) was
// describing one piece of work several times.
//
// And they are narrowed per row, to what the row's branch actually declares.
// Checking pathauto's 8.x-1.x on a core it does not declare produces a failure
// that says nothing about the module, and — since the fast lane requires every
// applicable core to be green — would deny a merge on the strength of a core
// the branch never claimed.
type RowFactory struct {
	cache EvidenceStore
	gate  *gate.FastLane
}

// NewRowFactory builds the pipeline. A nil gate takes the default.
func NewRowFactory(cache EvidenceStore, fastLane *gate.FastLane) *RowFactory {
	if fastLane == nil {
		fastLane = gate.NewFastLane(nil)
	}

	return &RowFactory{cache: cache, gate: fastLane}
}

// RowsInput is what one classification run is given.
type RowsInput struct {
	Module  cockpit.Module
	Project gitlab.Project
	// MergeRequests are the open ones, in any order.
	MergeRequests []gitlab.MergeRequest
	// VersionFilter gathers evidence for this core only, when given.
	VersionFilter string
	// CIFailures is a detail-fetch failure keyed by merge-request iid, for
	// rows that fell back to listed data.
	CIFailures map[int]*gitlab.Failure
	// Snapshot carries the module's issues and merged merge requests. Without
	// one there is nothing to group merge requests *by*, so every one of them
	// is its own row — which is the assembler's case, and why the fast lane
	// still prompts per merge request.
	Snapshot *ModuleSnapshot
	// MergeRefSHAs maps a merge-request iid to the SHA of its /merge ref, for
	// the caller that has no snapshot to take them from.
	//
	// Load-bearing, not an optimisation. `check` files its verdict under the
	// merge ref's SHA, because that is the tree it checked; evidence compared
	// against the *head* SHA instead reads as stale on every merge request
	// whose branch has fallen behind its target — which is most of them. The
	// snapshot supplies these on the dashboard path and this field on the
	// assembler's, and a caller supplying neither gets the head SHA and the
	// stale reading that comes with it.
	MergeRefSHAs map[int]string
}

// Rows classifies one module.
//
// The order is issue rows by nid then branch, then the merge requests
// belonging to no listed issue, by iid.
func (f *RowFactory) Rows(in RowsInput) []Row {
	cores := Cores(in.Module, in.VersionFilter)
	if len(cores) == 0 {
		return []Row{}
	}

	mergeRequests := make([]gitlab.MergeRequest, len(in.MergeRequests))
	copy(mergeRequests, in.MergeRequests)
	sort.SliceStable(mergeRequests, func(a, b int) bool {
		return mergeRequests[a].IID < mergeRequests[b].IID
	})

	var merged []gitlab.MergeRequest
	var contributions []patches.Contribution
	constraints := map[string]string{}
	mergeSHAs := in.MergeRefSHAs
	if mergeSHAs == nil {
		mergeSHAs = map[int]string{}
	}

	if in.Snapshot != nil {
		merged = in.Snapshot.MergedMergeRequests()
		contributions = patches.Pair(
			in.Module.Name,
			in.Snapshot.PatchIssues(),
			append(append([]gitlab.MergeRequest{}, mergeRequests...), merged...),
			in.Snapshot.ForkNids,
		)
		sort.SliceStable(contributions, func(a, b int) bool {
			return contributions[a].Issue.Nid < contributions[b].Issue.Nid
		})
		constraints = in.Snapshot.CoreConstraints
		mergeSHAs = in.Snapshot.MergeRefSHAs
	}

	branches := knownBranches(in.Project, append(append([]gitlab.MergeRequest{}, mergeRequests...), merged...))

	rows := []Row{}
	claimed := map[int]bool{}
	for _, contribution := range contributions {
		for _, mr := range contribution.MergeRequests {
			claimed[mr.IID] = true
		}
		rows = append(rows, f.issueRows(in, contribution, branches, cores, constraints, mergeSHAs)...)
	}

	for _, mergeRequest := range mergeRequests {
		if claimed[mergeRequest.IID] {
			continue
		}
		rows = append(rows, f.unlinkedRow(in, mergeRequest, cores, constraints, mergeSHAs))
	}

	return rows
}

// applicable is the cores worth gathering evidence on for one branch: the
// tracked ones, narrowed to what the branch declares.
//
// A branch with no recorded constraint keeps the tracked set whole. So does
// one whose constraint will not parse, and one that appears to declare none of
// the tracked cores — that is far likelier to be a constraint misread than a
// real state, and returning nothing would make the module vanish. Nothing here
// ever narrows to empty.
func applicable(cores []string, constraints map[string]string, branch string) []string {
	constraint, recorded := constraints[branch]
	if !recorded {
		return cores
	}

	compatibility, parsed := drupal.CompatibilityFrom(constraint, cores)
	if !parsed {
		return cores
	}

	return compatibility.ApplicableTo(cores)
}

// issueRows is one issue's rows: one per branch its open merge requests
// target, or — when nothing of its is open — one for the patches it carries.
//
// The branch multiplier is the only one left, and it is a real distinction: an
// issue with work on 1.0.x and on 2.0.x is a backport, which is two pieces of
// work rather than one seen twice.
//
// An issue with nothing open and no patch attached yields no row at all. That
// is the issue queue's subject — being unclaimed is the point there, where
// here it would be 29 of pathauto's 93 issues saying nothing.
func (f *RowFactory) issueRows(
	in RowsInput,
	contribution patches.Contribution,
	branches, cores []string,
	constraints map[string]string,
	mergeSHAs map[int]string,
) []Row {
	byBranch := map[string][]gitlab.MergeRequest{}
	for _, mr := range contribution.MergeRequests {
		byBranch[mr.TargetBranch] = append(byBranch[mr.TargetBranch], mr)
	}

	openBranches := []string{}
	for branch, onBranch := range byBranch {
		if len(openOnly(onBranch)) > 0 {
			openBranches = append(openBranches, branch)
		}
	}
	sort.Strings(openBranches)

	if len(openBranches) == 0 {
		branch := dormantBranch(contribution, in.Project, branches)
		if contribution.Issue.PatchCount() == 0 {
			return nil
		}

		key, err := results.PatchKey(contribution.Issue.Nid)
		if err != nil {
			return nil
		}

		return []Row{RowForIssue(
			in.Module.Name,
			branch,
			patches.Contribution{
				Module: in.Module.Name, Issue: contribution.Issue, MergeRequests: byBranch[branch],
			},
			&in.Project,
			nil,
			f.evidence(in.Module.Name, key, applicable(cores, constraints, branch), contribution.CurrentRevision()),
			nil,
			nil,
		)}
	}

	rows := make([]Row, 0, len(openBranches))
	for _, branch := range openBranches {
		onBranch := byBranch[branch]
		open := openOnly(onBranch)

		// A substantive merge request represents the branch in preference to
		// an empty one: an issue can carry both a real branch and the bot's
		// empty draft, and the real branch is what gets merged.
		representative := open[0]
		if substantive := patches.Substantive(open); len(substantive) > 0 {
			representative = substantive[0]
		}

		applicableCores := applicable(cores, constraints, branch)
		key, err := results.MergeRequestKey(representative.IID)
		if err != nil {
			continue
		}
		local := f.evidence(
			in.Module.Name, key, applicableCores,
			gitlab.MergeRevision(mergeSHAs[representative.IID], representative.HeadSHA),
		)
		verdict := f.gate.Classify(representative, applicableCores, local)

		rows = append(rows, RowForIssue(
			in.Module.Name,
			branch,
			patches.Contribution{Module: in.Module.Name, Issue: contribution.Issue, MergeRequests: onBranch},
			&in.Project,
			&representative,
			local,
			&verdict,
			in.CIFailures[representative.IID],
		))
	}

	return rows
}

// unlinkedRow is a merge request belonging to no listed issue.
//
// Not an edge case: 33 of pathauto's 162 merge requests claim no issue at all,
// and others claim one already marked fixed and so outside the open queue. The
// nid it does claim is still shown where there is one — an unpaired merge
// request is not an anonymous one.
func (f *RowFactory) unlinkedRow(
	in RowsInput,
	mergeRequest gitlab.MergeRequest,
	cores []string,
	constraints map[string]string,
	mergeSHAs map[int]string,
) Row {
	applicableCores := applicable(cores, constraints, mergeRequest.TargetBranch)

	var local results.LocalEvidence
	key, err := results.MergeRequestKey(mergeRequest.IID)
	if err == nil {
		local = f.evidence(
			in.Module.Name, key, applicableCores,
			gitlab.MergeRevision(mergeSHAs[mergeRequest.IID], mergeRequest.HeadSHA),
		)
	} else {
		local = results.NoEvidence()
	}

	claimed, _ := drupal.ExtractIssue(
		mergeRequest.Title, mergeRequest.SourceBranch, mergeRequest.Description,
	)

	return RowForUnlinkedMergeRequest(
		in.Module.Name,
		mergeRequest.TargetBranch,
		&in.Project,
		mergeRequest,
		local,
		f.gate.Classify(mergeRequest, applicableCores, local),
		in.CIFailures[mergeRequest.IID],
		claimed,
	)
}

// evidence is what is known locally about one subject, on every core that
// applies.
func (f *RowFactory) evidence(module string, key results.Key, cores []string, revision string) results.LocalEvidence {
	byCore := make(map[string]*results.CachedResult, len(cores))
	for _, core := range cores {
		// An unreadable results directory is reported by the cache and
		// deliberately not fatal here: a row with no evidence denies the fast
		// lane, which is the safe direction, and the cache's own complaint is
		// what tells the operator why.
		latest, _ := f.cache.Latest(module, key, core)
		byCore[core] = latest
	}

	return results.Evidence(byCore, revision)
}

// dormantBranch is the branch a row belongs to when no merge request of the
// issue's is open: the one that carried the landing, else the one the issue is
// filed against, else the project's default.
//
// Resolved rather than parsed. The version field holds whatever anyone has
// ever typed into it — 2.0.0, 8.0.x-dev, 5.1, and x.y.z 56 times in one sample
// — so candidates are proposed and only a name the project actually has is
// accepted.
func dormantBranch(contribution patches.Contribution, project gitlab.Project, branches []string) string {
	if landed := contribution.Landed(); landed != nil {
		return landed.TargetBranch
	}
	if branch := drupal.ResolveBranch(contribution.Issue.Version, branches); branch != "" {
		return branch
	}
	if project.DefaultBranch != "" {
		return project.DefaultBranch
	}

	return "-"
}

// knownBranches is every branch name this module's merge requests are known to
// target.
//
// The branch list without a request for one: an issue's version field is only
// usable when checked against branches that exist, and the merge requests
// already name them. Cheaper than asking, and never stale in a way the rest of
// the snapshot is not.
func knownBranches(project gitlab.Project, mergeRequests []gitlab.MergeRequest) []string {
	branches := []string{}
	seen := map[string]bool{}

	add := func(branch string) {
		if branch != "" && !seen[branch] {
			seen[branch] = true
			branches = append(branches, branch)
		}
	}

	add(project.DefaultBranch)
	for _, mr := range mergeRequests {
		add(mr.TargetBranch)
	}

	return branches
}

func openOnly(mergeRequests []gitlab.MergeRequest) []gitlab.MergeRequest {
	open := []gitlab.MergeRequest{}
	for _, mr := range mergeRequests {
		if mr.State != "merged" {
			open = append(open, mr)
		}
	}

	return open
}

// Cores is the core versions a run gathers evidence on for this module: all
// tracked ones, or the single filtered one when it is tracked.
func Cores(module cockpit.Module, versionFilter string) []string {
	if versionFilter == "" {
		return module.CoreVersions
	}

	filtered := []string{}
	for _, core := range module.CoreVersions {
		if core == versionFilter {
			filtered = append(filtered, core)
		}
	}

	return filtered
}

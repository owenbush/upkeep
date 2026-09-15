// Package dashboard assembles what a maintainer sees: one row per issue's work
// on one module branch, with the evidence and the next command for each.
package dashboard

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
)

var shaShaped = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// ModuleSnapshot is cached remote state for one module: the GitLab project,
// its open and merged merge requests, its open drupal.org issues, the
// fork-to-issue map that pairs the two, and what each branch declares about
// core.
//
// It stores raw API payloads so reading one back goes through the same
// narrowing a live fetch does — no separate serialisation contract to
// maintain.
//
// It used to carry a second issue collection as well, keyed by nid, fetched
// one request at a time for every issue any merge request mentioned. That
// existed to put a number in the dashboard's ISSUE cell. A row *is* an issue
// now and takes its issue from the open-issue scan already here, so the second
// collection — 155 requests per pathauto refresh, plus an attachment lookup
// per file on each — is read by nothing.
type ModuleSnapshot struct {
	FetchedAt time.Time

	// ProjectData is the raw GitLab project payload.
	ProjectData map[string]any
	// MRData is the raw GitLab merge-request detail payloads.
	MRData []map[string]any
	// PatchIssueData is the raw drupal.org payloads for the module's open
	// issues, with their attachments already dereferenced.
	PatchIssueData []map[string]any

	// MergedMRData is merged merge requests, kept apart from the open ones
	// because they make no row of their own — they answer "has this already
	// landed?" about an issue that is still open, which for a Project Update
	// Bot compatibility issue is the normal state and unknowable otherwise.
	MergedMRData []map[string]any

	// ForkNids maps source-project id to issue nid. The only thing that pairs
	// a bot merge request to its issue.
	ForkNids map[int]int

	// CoreConstraints maps a branch name to its core_version_requirement, read
	// from the branch's info.yml.
	//
	// Which cores a row's evidence is worth gathering on. A *branch* supports
	// several at once, and not always the ones the registry tracks — checking
	// a branch on a core it does not declare produces a failure that says
	// nothing about the module.
	//
	// A branch absent from this map is "cannot tell", never "supports
	// nothing": the file may not exist on that branch (token's
	// 691078-field-tokens has no info.yml at all), the fetch may have failed,
	// or the snapshot may predate this field. Every one of those falls back to
	// the tracked set whole.
	CoreConstraints map[string]string

	// MergeRefSHAs maps a merge-request iid to the SHA of its /merge ref.
	//
	// The revision each merge request's local evidence is about. upkeep checks
	// the branch merged into the current tip of its target, and that tree
	// moves when *either* side does — so a result keyed on the head SHA alone
	// would read as current after the target gained a commit. An iid missing
	// here has no merge ref (GitLab could not merge it, normally a conflict)
	// or comes from a snapshot written before this existed; both fall back to
	// the head SHA, which is what the adapter checks out in that case too.
	MergeRefSHAs map[int]string
}

// PatchIssues is the open issues this module had when the snapshot was taken —
// the dashboard's rows, and the issue queue.
//
// Empty for a snapshot written before they were stored, which reads as "this
// module contributed no rows" — the pre-existing dashboard, until the next
// refresh. Cheaper and less surprising than silently going to the network from
// a command whose whole promise is that a cached run costs nothing.
func (s ModuleSnapshot) PatchIssues() []drupal.Issue {
	issues := []drupal.Issue{}
	for _, data := range s.PatchIssueData {
		if issue, ok := drupal.IssueFrom(data); ok {
			issues = append(issues, issue)
		}
	}

	return issues
}

// Project is the GitLab project, as a model.
func (s ModuleSnapshot) Project() gitlab.Project {
	return gitlab.ProjectFrom(s.ProjectData)
}

// MergeRequests is the open merge requests, as models.
func (s ModuleSnapshot) MergeRequests() []gitlab.MergeRequest {
	return mergeRequestsFrom(s.MRData)
}

// MergedMergeRequests is the merged merge requests, as models.
func (s ModuleSnapshot) MergedMergeRequests() []gitlab.MergeRequest {
	return mergeRequestsFrom(s.MergedMRData)
}

func mergeRequestsFrom(payloads []map[string]any) []gitlab.MergeRequest {
	mergeRequests := make([]gitlab.MergeRequest, 0, len(payloads))
	for _, data := range payloads {
		mergeRequests = append(mergeRequests, gitlab.MergeRequestFrom(data))
	}

	return mergeRequests
}

// snapshotFile is the on-disk shape. Every field is read leniently because a
// cache file is untrusted input like any other: it is on disk, it outlives the
// format that wrote it, and a half-shaped one must read as "no cache" — the
// caller then refetches — rather than reach the models as anything else.
type snapshotFile struct {
	FetchedAt           string           `json:"fetched_at"`
	Project             map[string]any   `json:"project"`
	MergeRequests       []any            `json:"merge_requests"`
	PatchIssues         []any            `json:"patch_issues"`
	MergedMergeRequests []any            `json:"merged_merge_requests"`
	ForkNids            map[string]any   `json:"fork_nids"`
	CoreConstraints     map[string]any   `json:"core_constraints"`
	MergeRefSHAs        map[string]any   `json:"merge_ref_shas"`
}

// ToJSON renders the snapshot.
func (s ModuleSnapshot) ToJSON() (string, error) {
	// Written through plain maps so the payloads go out exactly as they came
	// in, and the two implementations produce interchangeable files.
	out := map[string]any{
		"fetched_at":            s.FetchedAt.Format(time.RFC3339),
		"project":               s.ProjectData,
		"merge_requests":        s.MRData,
		"patch_issues":          s.PatchIssueData,
		"merged_merge_requests": s.MergedMRData,
		"fork_nids":             forkNidsOut(s.ForkNids),
		"core_constraints":      s.CoreConstraints,
		"merge_ref_shas":        mergeRefSHAsOut(s.MergeRefSHAs),
	}

	encoded, err := json.MarshalIndent(out, "", "    ")
	if err != nil {
		return "", fmt.Errorf("cannot render the snapshot: %w", err)
	}

	return string(encoded), nil
}

// forkNidsOut and mergeRefSHAsOut key by the decimal id, because JSON object
// keys are strings and PHP writes them the same way.
func forkNidsOut(nids map[int]int) map[string]int {
	out := make(map[string]int, len(nids))
	for projectID, nid := range nids {
		out[strconv.Itoa(projectID)] = nid
	}

	return out
}

func mergeRefSHAsOut(shas map[int]string) map[string]string {
	out := make(map[string]string, len(shas))
	for iid, sha := range shas {
		out[strconv.Itoa(iid)] = sha
	}

	return out
}

// SnapshotFromJSON reads a cache file. It reports false for anything it cannot
// use, which the caller reads as "no cache".
func SnapshotFromJSON(raw string) (ModuleSnapshot, bool) {
	var file snapshotFile
	if err := json.Unmarshal([]byte(raw), &file); err != nil {
		return ModuleSnapshot{}, false
	}

	if file.FetchedAt == "" || file.Project == nil || file.MergeRequests == nil {
		return ModuleSnapshot{}, false
	}
	fetchedAt, ok := parseSnapshotTime(file.FetchedAt)
	if !ok {
		return ModuleSnapshot{}, false
	}

	// Absent in a snapshot written before landings were tracked: an older
	// cache reads as "nothing known to have merged", which is the previous
	// behaviour rather than a wrong claim.
	return ModuleSnapshot{
		FetchedAt:       fetchedAt,
		ProjectData:     file.Project,
		MRData:          payloadList(file.MergeRequests),
		PatchIssueData:  payloadList(file.PatchIssues),
		MergedMRData:    payloadList(file.MergedMergeRequests),
		ForkNids:        forkMap(file.ForkNids),
		CoreConstraints: constraintMap(file.CoreConstraints),
		MergeRefSHAs:    shaMap(file.MergeRefSHAs),
	}, true
}

func parseSnapshotTime(raw string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}

	return time.Time{}, false
}

func payloadList(raw []any) []map[string]any {
	payloads := []map[string]any{}
	for _, entry := range raw {
		if fields, ok := entry.(map[string]any); ok {
			payloads = append(payloads, fields)
		}
	}

	return payloads
}

// forkMap reads source-project id to issue nid from an untrusted cache file.
func forkMap(raw map[string]any) map[int]int {
	nids := map[int]int{}
	for projectID, nid := range raw {
		id, err := strconv.Atoi(projectID)
		if err != nil {
			continue
		}
		// Whole numbers only. JSON has one number type, so a nid written as
		// 3603341.5 would otherwise truncate into a plausible-looking issue
		// that does not exist.
		asFloat, isNumber := nid.(float64)
		if !isNumber || asFloat != float64(int(asFloat)) {
			continue
		}
		nids[id] = int(asFloat)
	}

	return nids
}

// constraintMap reads branch name to core constraint from an untrusted cache
// file.
func constraintMap(raw map[string]any) map[string]string {
	constraints := map[string]string{}
	for branch, constraint := range raw {
		if text, isString := constraint.(string); isString && text != "" {
			constraints[branch] = text
		}
	}

	return constraints
}

// shaMap reads merge-request iid to merge-ref SHA from an untrusted cache
// file.
func shaMap(raw map[string]any) map[int]string {
	shas := map[int]string{}
	for iid, sha := range raw {
		id, err := strconv.Atoi(iid)
		if err != nil {
			continue
		}
		if text, isString := sha.(string); isString && shaShaped.MatchString(text) {
			shas[id] = text
		}
	}

	return shas
}

// AgeLabel is how long ago this snapshot was taken, for the header line.
func (s ModuleSnapshot) AgeLabel(now time.Time) string {
	seconds := now.Unix() - s.FetchedAt.Unix()

	switch {
	case seconds < 60:
		return "just now"
	case seconds < 3600:
		return fmt.Sprintf("%dm ago", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh ago", seconds/3600)
	default:
		return fmt.Sprintf("%dd ago", seconds/86400)
	}
}

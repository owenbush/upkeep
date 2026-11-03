package drupalorgcli

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"

	"github.com/owenbush/upkeep/internal/gitlab"
)

// MergeRequestReader answers the four merge-request reads gitlab.Client
// answers natively — same signatures, same models, same *gitlab.Failure — by
// running drupalorg-cli's mr:list and mr:status instead. It does not replace
// the native methods; parity (task 3) decides whether it can.
//
// # What drupalorg-cli 0.10.3 cannot say
//
// mr:list rows carry iid, title, source_branch, target_branch, state,
// web_url, is_mergeable, author (a username) and updated_at. Everything else
// on gitlab.MergeRequest is left at its zero value, and these are the fields
// upkeep's callers actually read that the CLI therefore cannot fill:
//
//   - AuthorID: the bot-author match (config.BotPattern, notes) falls back to
//     the username alone.
//   - Description: drupal.ExtractOwningIssue loses its "Closes #N" and URL
//     signals (patches pairing, dashboard rows, issueloop, needs-work).
//   - HeadSHA: merge-revision keying of cached evidence, merge's moved-branch
//     guard and expected-SHA merge, the adapter's moved-head note.
//   - DiffBaseSHA, DiffHeadSHA: CarriesChanges is therefore always Unknown,
//     which callers treat as "carries changes" — never as empty.
//   - SourceProjectID: fork-nid pairing (patches.Contribution, checkout).
//   - MergedAt: gitlab.LatestMerged, the patch surface's landed-before check
//     and the dashboard's "landed" guidance all key on it.
//   - DetailedMergeStatus: only api:probe prints it. is_mergeable is the CLI's
//     reading of merge_status == can_be_merged, a different field, so it is
//     not translated into this one.
//   - Draft: the flag is absent, so it falls back to a "Draft: " title prefix
//     exactly as the native narrowing does when the key is missing.
//
// ToAPIMap round-trips whatever is set, so a snapshot written from these
// models carries the same gaps.
type MergeRequestReader struct {
	runner *Runner
	ctx    context.Context
}

// NewMergeRequestReader reads through runner. The native signatures take no
// context, so every call runs under context.Background().
func NewMergeRequestReader(runner *Runner) *MergeRequestReader {
	return &MergeRequestReader{runner: runner, ctx: context.Background()}
}

// mrList is mr:list's JSON.
type mrList struct {
	ProjectPath   string   `json:"project_path"`
	MergeRequests []mrItem `json:"merge_requests"`
}

type mrItem struct {
	IID          int    `json:"iid"`
	Title        string `json:"title"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	State        string `json:"state"`
	WebURL       string `json:"web_url"`
	IsMergeable  bool   `json:"is_mergeable"`
	Author       string `json:"author"`
	UpdatedAt    string `json:"updated_at"`
}

// mrStatus is mr:status's JSON. pipeline_id and pipeline_url are null when
// the merge request has no pipeline, and status is then "none".
type mrStatus struct {
	IID         int     `json:"iid"`
	PipelineID  *int    `json:"pipeline_id"`
	Status      string  `json:"status"`
	PipelineURL *string `json:"pipeline_url"`
}

// OpenMergeRequests is every open merge request on the project.
//
// `mr:list <project-path> --state=opened`. The CLI asks GitLab for one page of
// 100 (per_page=100, no pagination) where the native call also reads one page
// of 100, so the bound matches. Fields left zero: see MergeRequestReader.
func (m *MergeRequestReader) OpenMergeRequests(project gitlab.Project) ([]gitlab.MergeRequest, *gitlab.Failure) {
	return m.list(project, "opened", project.WebURL+"/-/merge_requests")
}

// MergedMergeRequests is recently merged merge requests, newest updated
// first, at most limit of them.
//
// `mr:list <project-path> --state=merged`. The CLI cannot order or bound the
// query: it takes GitLab's default order (created_at desc) and one page of
// 100. So the ordering and the limit are applied here, over that page — which
// matches the native updated_at-desc query whenever the project has at most
// 100 merged merge requests, and otherwise may miss an old merge request that
// was updated recently. Left for parity (task 3) to judge. MergedAt, the
// field these are most often read for, is among the fields left zero; see
// MergeRequestReader.
func (m *MergeRequestReader) MergedMergeRequests(
	project gitlab.Project,
	limit int,
) ([]gitlab.MergeRequest, *gitlab.Failure) {
	list, failure := m.list(project, "merged", project.WebURL+"/-/merge_requests?state=merged")
	if failure != nil {
		return nil, failure
	}

	// RFC 3339 in one zone (GitLab sends Z), so string order is time order.
	// Stable, so equal timestamps keep the CLI's newest-created-first order.
	slices.SortStableFunc(list, func(a, b gitlab.MergeRequest) int { return cmp.Compare(b.UpdatedAt, a.UpdatedAt) })
	if limit >= 0 && len(list) > limit {
		list = list[:limit]
	}

	return list, nil
}

// MergeRequest is one merge request with its head pipeline.
//
// There is no single-merge-request command: `mr:list <project-path>!<iid>`
// ignores the iid and lists the project. So this is `mr:list --state=all`
// filtered by iid, plus `mr:status <project-path>!<iid>` for the pipeline —
// two child processes where the native call makes one request. --state=all
// reads one page of 100, newest created first, so a merge request older than
// the project's 100 most recent is reported NotFound though it exists.
//
// Fields left zero: see MergeRequestReader. The detail endpoint's whole point
// natively is diff_refs, detailed_merge_status and head SHA, none of which
// the CLI carries. HeadPipeline is filled, minus its SHA (see HeadPipeline).
func (m *MergeRequestReader) MergeRequest(project gitlab.Project, iid int) (*gitlab.MergeRequest, *gitlab.Failure) {
	browserURL := fmt.Sprintf("%s/-/merge_requests/%d", project.WebURL, iid)

	list, failure := m.list(project, "all", browserURL)
	if failure != nil {
		return nil, failure
	}

	index := slices.IndexFunc(list, func(mr gitlab.MergeRequest) bool { return mr.IID == iid })
	if index < 0 {
		return nil, &gitlab.Failure{
			Kind: gitlab.NotFound,
			Message: fmt.Sprintf(
				"Merge request !%d is not among the merge requests drupalorg mr:list returned. "+
					"Check in the browser: %s", iid, browserURL),
			BrowserURL: browserURL,
		}
	}
	mr := list[index]

	pipeline, failure := m.pipeline(project, iid, browserURL)
	if failure != nil {
		return nil, failure
	}
	mr.HeadPipeline = pipeline

	return &mr, nil
}

// HeadPipeline is the latest pipeline of a merge request, or nil when it has
// none.
//
// `mr:status <project-path>!<iid>`, one child process; the native method
// derives this from MergeRequest, and this one does not need the list.
//
// Two differences from the native model. mr:status reads the newest entry of
// /merge_requests/:iid/pipelines rather than the MR's head_pipeline, which
// agree in the ordinary case. And the CLI flattens GitLab's statuses before
// printing them — success→passed, created/waiting_for_resource/preparing/
// scheduled/manual→pending, skipped→canceled — so passed maps back to
// success and the rest arrive as the coarser GitLab value: a manual or
// skipped pipeline reads as pending or canceled. Neither is green and
// neither is failed, so the gate's verdict is unchanged; only the raw status
// the dashboard prints is coarser. Pipeline.SHA is left zero (no caller
// reads it).
func (m *MergeRequestReader) HeadPipeline(project gitlab.Project, iid int) (*gitlab.Pipeline, *gitlab.Failure) {
	return m.pipeline(project, iid, fmt.Sprintf("%s/-/merge_requests/%d", project.WebURL, iid))
}

func (m *MergeRequestReader) list(
	project gitlab.Project,
	state, browserURL string,
) ([]gitlab.MergeRequest, *gitlab.Failure) {
	path, failure := projectPath(project)
	if failure != nil {
		return nil, withBrowserURL(failure, browserURL)
	}

	var out mrList
	if failure := m.runner.Run(m.ctx, []string{"mr:list", path, "--state=" + state}, &out); failure != nil {
		return nil, withBrowserURL(failure, browserURL)
	}

	list := make([]gitlab.MergeRequest, 0, len(out.MergeRequests))
	for _, item := range out.MergeRequests {
		list = append(list, mergeRequestFromItem(item))
	}

	return list, nil
}

func (m *MergeRequestReader) pipeline(
	project gitlab.Project,
	iid int,
	browserURL string,
) (*gitlab.Pipeline, *gitlab.Failure) {
	path, failure := projectPath(project)
	if failure != nil {
		return nil, withBrowserURL(failure, browserURL)
	}

	var out mrStatus
	if failure := m.runner.Run(m.ctx, []string{"mr:status", path + "!" + strconv.Itoa(iid)}, &out); failure != nil {
		return nil, withBrowserURL(failure, browserURL)
	}

	return pipelineFromStatus(out), nil
}

// mergeRequestFromItem goes through the native narrowing, so the CLI-backed
// model cannot drift from what a GitLab payload would produce — the
// "Draft: " title fallback in particular.
func mergeRequestFromItem(item mrItem) gitlab.MergeRequest {
	return gitlab.MergeRequestFrom(map[string]any{
		"iid":           item.IID,
		"title":         item.Title,
		"state":         item.State,
		"source_branch": item.SourceBranch,
		"target_branch": item.TargetBranch,
		"web_url":       item.WebURL,
		"updated_at":    item.UpdatedAt,
		"author":        map[string]any{"username": item.Author},
	})
}

// cliToGitlabStatus undoes the one rename in the CLI's status map. Every other
// value it prints is already GitLab's own word (see HeadPipeline).
var cliToGitlabStatus = map[string]string{"passed": string(gitlab.StatusSuccess)}

// pipelineFromStatus is nil for "no pipeline", which the CLI says with a null
// pipeline_id and status "none".
func pipelineFromStatus(status mrStatus) *gitlab.Pipeline {
	if status.PipelineID == nil || status.Status == "none" {
		return nil
	}

	raw := status.Status
	if mapped, ok := cliToGitlabStatus[raw]; ok {
		raw = mapped
	}

	pipeline := &gitlab.Pipeline{ID: *status.PipelineID, Status: gitlab.PipelineStatusFrom(raw), RawStatus: raw}
	if status.PipelineURL != nil {
		pipeline.WebURL = *status.PipelineURL
	}

	return pipeline
}

// machineName is what drupal.org accepts as a project's machine name, and
// what the CLI's own project-path parser accepts on either side of the slash.
var (
	machineName    = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	namespacedPath = regexp.MustCompile(`^[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+$`)
)

// projectPath is the namespaced path every call passes. Never a bare node id:
// `mr:list <nid>` resolves the issue's project and lists the whole project's
// merge requests, a silently different question.
func projectPath(project gitlab.Project) (string, *gitlab.Failure) {
	if path := project.PathWithNamespace; namespacedPath.MatchString(path) {
		return path, nil
	}
	if machineName.MatchString(project.Path) && !isDigits(project.Path) {
		return "project/" + project.Path, nil
	}

	return "", &gitlab.Failure{
		Kind: gitlab.ResourceMissing,
		Message: fmt.Sprintf(
			"No project path to give drupalorg-cli for project %d (path %q, path_with_namespace %q).",
			project.ID, project.Path, project.PathWithNamespace),
	}
}

func isDigits(s string) bool {
	_, err := strconv.Atoi(s)

	return err == nil
}

// withBrowserURL adds where to look, as the native client's failures do; the
// runner cannot know it. A copy, because the version gate hands every caller
// the same cached failure.
func withBrowserURL(failure *gitlab.Failure, browserURL string) *gitlab.Failure {
	copied := *failure
	if copied.BrowserURL == "" {
		copied.BrowserURL = browserURL
	}

	return &copied
}

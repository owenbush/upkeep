package gitlab

import (
	"strings"
	"time"
)

// Tristate answers a question the payload may not settle.
//
// It exists because two of these questions have an unknown that must never be
// read as "no": whether a merge request carries changes, and whether you may
// push to a project.
type Tristate int

const (
	// Unknown is "this payload cannot say" — never "no".
	Unknown Tristate = iota
	Yes
	No
)

// PipelineStatus is GitLab's pipeline status, mapped from the API's string
// values. An unrecognised (future) status maps to StatusUnknown rather than
// crashing the caller.
type PipelineStatus string

const (
	StatusCreated            PipelineStatus = "created"
	StatusWaitingForResource PipelineStatus = "waiting_for_resource"
	StatusPreparing          PipelineStatus = "preparing"
	StatusPending            PipelineStatus = "pending"
	StatusRunning            PipelineStatus = "running"
	StatusSuccess            PipelineStatus = "success"
	StatusFailed             PipelineStatus = "failed"
	StatusCanceled           PipelineStatus = "canceled"
	StatusSkipped            PipelineStatus = "skipped"
	StatusManual             PipelineStatus = "manual"
	StatusScheduled          PipelineStatus = "scheduled"
	StatusUnknown            PipelineStatus = "unknown"
)

var knownStatuses = []PipelineStatus{
	StatusCreated, StatusWaitingForResource, StatusPreparing, StatusPending,
	StatusRunning, StatusSuccess, StatusFailed, StatusCanceled, StatusSkipped,
	StatusManual, StatusScheduled, StatusUnknown,
}

// PipelineStatusFrom maps an API status string, or StatusUnknown.
func PipelineStatusFrom(status string) PipelineStatus {
	for _, known := range knownStatuses {
		if string(known) == status {
			return known
		}
	}

	return StatusUnknown
}

// IsGreen reports whether the pipeline finished with a green result — the only
// value the fast-lane gate treats as CI-passing.
func (s PipelineStatus) IsGreen() bool { return s == StatusSuccess }

// Pipeline is a CI run against a revision.
type Pipeline struct {
	ID     int
	Status PipelineStatus
	// RawStatus is what GitLab actually said, kept so an unrecognised status
	// can be reported rather than flattened to "unknown".
	RawStatus string
	SHA       string
	WebURL    string
}

func pipelineFrom(p payload) Pipeline {
	raw := p.str("status", "")

	return Pipeline{
		ID:        p.intOr("id", 0),
		Status:    PipelineStatusFrom(raw),
		RawStatus: raw,
		SHA:       p.str("sha", ""),
		WebURL:    p.str("web_url", ""),
	}
}

// ToAPIMap renders the pipeline back into the shape it was read from, so a
// cached snapshot round-trips.
func (p Pipeline) ToAPIMap() map[string]any {
	return map[string]any{"id": p.ID, "status": p.RawStatus, "sha": p.SHA, "web_url": p.WebURL}
}

// developerAccess is GitLab's Developer role: the level at which pushing
// becomes possible.
const developerAccess = 30

// Project is a GitLab project.
type Project struct {
	ID                int
	Path              string
	PathWithNamespace string
	Name              string
	WebURL            string
	// SSHURL is the push URL as GitLab itself advertises it. Never
	// constructed: git.drupalcode.org serves HTTPS but advertises SSH on
	// git.drupal.org, and assembling the URL from the host you fetched from
	// produces one that does not answer.
	SSHURL string
	// DefaultBranch is the branch a merge request targets when nothing else
	// says.
	DefaultBranch string
	// AccessLevel is your GitLab access level here, or nil when the payload
	// cannot say — an unauthenticated read omits permissions entirely. Nil is
	// genuinely unknown and must never be read as "no".
	AccessLevel *int
}

// CanPush reports whether you may push here.
//
// On drupal.org this is the question a fresh issue fork answers "no" to:
// creating a fork does not grant push access to it, and the grant is a
// separate button on the issue page. Asking before pushing turns a post-hoc
// rejection into something that can be said up front.
//
// Unknown when it cannot be told, and callers must proceed on unknown rather
// than refuse: an absent permissions key means the request was
// unauthenticated or the shape changed, and neither is evidence about access.
func (p Project) CanPush() Tristate {
	if p.AccessLevel == nil {
		return Unknown
	}
	if *p.AccessLevel >= developerAccess {
		return Yes
	}

	return No
}

func projectFrom(p payload) Project {
	return Project{
		ID:                p.intOr("id", 0),
		Path:              p.str("path", ""),
		PathWithNamespace: p.str("path_with_namespace", ""),
		Name:              p.str("name", ""),
		WebURL:            p.str("web_url", ""),
		SSHURL:            p.str("ssh_url_to_repo", ""),
		DefaultBranch:     p.str("default_branch", ""),
		AccessLevel:       accessLevelFrom(p),
	}
}

// accessLevelFrom is the highest of the direct and inherited grants, since
// either is enough to push and GitLab reports them separately.
func accessLevelFrom(p payload) *int {
	permissions, ok := p.child("permissions")
	if !ok {
		return nil
	}

	var highest *int
	for _, key := range []string{"project_access", "group_access"} {
		grant, ok := permissions.child(key)
		if !ok {
			continue
		}
		level, ok := scalarInt(grant.data["access_level"])
		if !ok {
			continue
		}
		if highest == nil || level > *highest {
			value := level
			highest = &value
		}
	}

	return highest
}

// ToAPIMap renders the project back into the shape it was read from.
func (p Project) ToAPIMap() map[string]any {
	var permissions any
	if p.AccessLevel != nil {
		permissions = map[string]any{"project_access": map[string]any{"access_level": *p.AccessLevel}}
	}

	return map[string]any{
		"id":                  p.ID,
		"path":                p.Path,
		"path_with_namespace": p.PathWithNamespace,
		"name":                p.Name,
		"web_url":             p.WebURL,
		"ssh_url_to_repo":     p.SSHURL,
		"default_branch":      p.DefaultBranch,
		"permissions":         permissions,
	}
}

// Tag is a repository tag.
type Tag struct {
	Name      string
	CommitSHA string
	// CreatedAt is the zero time when the tag carries no readable commit date.
	CreatedAt time.Time
}

// tagTimeLayouts are what GitLab actually sends, newest form first. Go has no
// tolerant date constructor, so the accepted shapes are listed rather than
// guessed at.
var tagTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.000-07:00",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

func tagFrom(p payload) Tag {
	commit, hasCommit := p.child("commit")

	tag := Tag{Name: p.str("name", "")}
	if hasCommit {
		tag.CommitSHA = commit.str("id", "")
		raw := commit.str("created_at", "")
		if raw == "" {
			raw = commit.str("committed_date", "")
		}
		for _, layout := range tagTimeLayouts {
			if parsed, err := time.Parse(layout, raw); err == nil {
				tag.CreatedAt = parsed

				break
			}
		}
	}
	if tag.CommitSHA == "" {
		tag.CommitSHA = p.str("target", "")
	}

	return tag
}

// MergeRequest is a merge request as the dashboard and the gate consume it.
//
// It exposes exactly the fields the fast-lane gate keys on for bot-MR
// classification — author username and id, source branch, title, draft state,
// detailed_merge_status, head SHA — plus the diff refs that say whether the
// merge request carries any changes at all.
type MergeRequest struct {
	IID                 int
	Title               string
	State               string
	AuthorUsername      string
	AuthorID            int
	SourceBranch        string
	TargetBranch        string
	Draft               bool
	DetailedMergeStatus string
	HeadSHA             string
	WebURL              string
	Description         string
	// HeadPipeline is nil when the merge request has none.
	HeadPipeline *Pipeline
	UpdatedAt    string
	DiffBaseSHA  string
	DiffHeadSHA  string
	// SourceProjectID is the project holding the source branch, which on
	// drupal.org is almost never the project the merge request targets — it is
	// the issue fork. Zero when the payload does not carry it.
	SourceProjectID int
	// MergedAt is when it merged, for a merged merge request. The evidence
	// that an open issue's work has already landed, which a Project Update Bot
	// compatibility issue, kept open by convention, cannot tell you itself.
	MergedAt string
}

// IsDraft reports whether GitLab considers this a draft. Signalled two ways,
// and either alone counts.
func (m MergeRequest) IsDraft() bool {
	return m.Draft || m.DetailedMergeStatus == "draft_status"
}

// CarriesChanges reports whether this merge request carries any changes.
//
// A merge request whose branch holds no commits the target does not already
// have is an *empty* one: it exists, it can be linked from an issue, and it
// covers nothing. The Project Update Bot opens exactly such a merge request on
// a great many contrib projects when it finds nothing to fix.
//
// The signal is diff_refs.base_sha != head_sha, deliberately, because the two
// more obvious fields both lie: changes_count is null on an empty merge
// request, indistinguishable from "the diff has not been generated yet"; and
// detailed_merge_status reports draft_status for a draft, masking the
// emptiness underneath — while a draft carrying real commits is not empty at
// all.
//
// Unknown means "not known from this payload" rather than "not empty": the
// merge-request *list* endpoint omits diff_refs entirely, so only the
// single-merge-request endpoint can settle it. Callers decide what an unknown
// means for them; none may read it as No.
func (m MergeRequest) CarriesChanges() Tristate {
	if m.DiffBaseSHA == "" || m.DiffHeadSHA == "" {
		return Unknown
	}
	if m.DiffBaseSHA != m.DiffHeadSHA {
		return Yes
	}

	return No
}

// LatestMerged is the most recently merged of a set of merge requests, or nil.
//
// The question a still-open drupal.org issue cannot answer about itself.
// Project Update Bot compatibility issues are kept open on purpose so the bot
// can post again as core moves, so an open one may have had its real work
// merged months ago — and a maintainer staring at the bot's leftover draft has
// no way to tell.
func LatestMerged(mergeRequests []MergeRequest) *MergeRequest {
	var latest *MergeRequest
	for i := range mergeRequests {
		mr := &mergeRequests[i]
		if mr.State != "merged" {
			continue
		}
		if latest == nil || mr.MergedAt > latest.MergedAt {
			latest = mr
		}
	}

	return latest
}

func mergeRequestFrom(p payload) MergeRequest {
	title := p.str("title", "")
	mr := MergeRequest{
		IID:                 p.intOr("iid", 0),
		Title:               title,
		State:               p.str("state", ""),
		SourceBranch:        p.str("source_branch", ""),
		TargetBranch:        p.str("target_branch", ""),
		Draft:               p.boolOr("draft", strings.HasPrefix(title, "Draft: ")),
		DetailedMergeStatus: p.str("detailed_merge_status", ""),
		HeadSHA:             p.str("sha", ""),
		WebURL:              p.str("web_url", ""),
		Description:         p.str("description", ""),
		UpdatedAt:           p.str("updated_at", ""),
		SourceProjectID:     p.intOr("source_project_id", 0),
		MergedAt:            p.str("merged_at", ""),
	}

	if author, ok := p.child("author"); ok {
		mr.AuthorUsername = author.str("username", "")
		mr.AuthorID = author.intOr("id", 0)
	}
	if pipeline, ok := p.child("head_pipeline"); ok {
		built := pipelineFrom(pipeline)
		mr.HeadPipeline = &built
	}
	if diffRefs, ok := p.child("diff_refs"); ok {
		mr.DiffBaseSHA = diffRefs.str("base_sha", "")
		mr.DiffHeadSHA = diffRefs.str("head_sha", "")
	}

	return mr
}

// ToAPIMap renders the merge request back into the shape it was read from.
func (m MergeRequest) ToAPIMap() map[string]any {
	var pipeline any
	if m.HeadPipeline != nil {
		pipeline = m.HeadPipeline.ToAPIMap()
	}

	// Round-tripped so a cached snapshot answers CarriesChanges without
	// refetching. Absent when unknown, which reads back as unknown rather than
	// as "not empty".
	var diffRefs any
	if m.DiffBaseSHA != "" || m.DiffHeadSHA != "" {
		diffRefs = map[string]any{"base_sha": m.DiffBaseSHA, "head_sha": m.DiffHeadSHA}
	}

	return map[string]any{
		"iid":                   m.IID,
		"title":                 m.Title,
		"state":                 m.State,
		"author":                map[string]any{"username": m.AuthorUsername, "id": m.AuthorID},
		"source_branch":         m.SourceBranch,
		"target_branch":         m.TargetBranch,
		"draft":                 m.Draft,
		"detailed_merge_status": m.DetailedMergeStatus,
		"sha":                   m.HeadSHA,
		"web_url":               m.WebURL,
		"description":           m.Description,
		"head_pipeline":         pipeline,
		"updated_at":            m.UpdatedAt,
		"source_project_id":     m.SourceProjectID,
		"merged_at":             m.MergedAt,
		"diff_refs":             diffRefs,
	}
}

// MergeRevision is the revision a merge request's local evidence is about.
//
// The merge ref when there is one, the head otherwise. GitLab publishes two
// refs per merge request: /head is the contributor's branch, /merge is that
// branch merged into the current tip of the target, and CI analyses /merge.
// Evidence keyed on the head SHA would still read as current after the
// *target* gained a commit, because the merge tree depends on both sides and
// the head does not change when only one of them does.
//
// Empty when neither is known.
func MergeRevision(mergeRefSHA, headSHA string) string {
	for _, candidate := range []string{mergeRefSHA, headSHA} {
		if candidate != "" {
			return candidate
		}
	}

	return ""
}

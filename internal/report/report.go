// Package report is upkeep's machine-readable output.
//
// **These types are a published contract and the internal models are not.**
// That is the whole reason the package exists. `dashboard.Row` holds pointers
// into gitlab, gate, results and patches, so marshalling it would publish the
// entire internal object graph and freeze it: every field rename inside those
// packages would become a breaking change for somebody's script. Declaring the
// payload separately costs a mapping function and buys the freedom to refactor
// everything behind it.
//
// It is also not the table serialised. The tables truncate titles to fit a
// column and print an en dash for an empty cell, both of which are right for
// reading and useless to a program. Where a *judgement* is what the dashboard
// exists to give — the status phrase and the command to run next — the payload
// carries it verbatim from the same `dashboard.GuidanceFor`, so the two
// surfaces cannot disagree about what to do with a row.
//
// Nothing imports this but the command layer, so it stays a leaf.
package report

import (
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
)

// Schema is the payload version.
//
// An integer rather than the binary's version, because they answer different
// questions: a consumer cares whether the *shape* it knows is the shape it is
// being handed, and upkeep's own version changes for reasons that have nothing
// to do with these structs. Bumped only when a field changes meaning or goes
// away; adding one is not a bump.
const Schema = 1

// Envelope is the one document shape every command emits.
//
// The command name is in the document rather than left to the caller's memory
// of what it ran, so a payload read from a log or a pipe still says what it
// describes.
type Envelope struct {
	Upkeep   string   `json:"upkeep"`
	Schema   int      `json:"schema"`
	Command  string   `json:"command"`
	Warnings []string `json:"warnings"`
	Data     any      `json:"data"`
}

// NewEnvelope wraps a payload.
//
// Warnings are always an array, never null: a consumer should be able to
// iterate them without a nil check, and "no warnings" is a real answer that
// deserves a real empty list.
func NewEnvelope(version, command string, data any, warnings []string) Envelope {
	if warnings == nil {
		warnings = []string{}
	}

	return Envelope{
		Upkeep:   version,
		Schema:   Schema,
		Command:  command,
		Warnings: warnings,
		Data:     data,
	}
}

// IssueRef is an issue as every payload carries it.
//
// Untruncated title, and the status as drupal.org's own word rather than the
// integer: a consumer deciding what to do with "needs review" should not have
// to carry a table of drupal.org's status ids.
type IssueRef struct {
	Nid      int    `json:"nid"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
	Version  string `json:"version,omitempty"`
	URL      string `json:"url,omitempty"`
	Patches  int    `json:"patches"`
}

// MergeRequestRef is a merge request as every payload carries it.
type MergeRequestRef struct {
	IID          int    `json:"iid"`
	Title        string `json:"title"`
	Draft        bool   `json:"draft"`
	SourceBranch string `json:"source_branch,omitempty"`
	WebURL       string `json:"web_url,omitempty"`
	HeadSHA      string `json:"head_sha,omitempty"`
}

// PipelineRef is the head pipeline's verdict.
//
// Nil when the merge request has no pipeline, which is different from a
// pipeline that has not finished and different again from upkeep being unable
// to ask — see Row.CIUnavailable.
type PipelineRef struct {
	Status string `json:"status"`
	Green  bool   `json:"green"`
}

// VerdictRef is the fast-lane classification and why.
type VerdictRef struct {
	Status  string   `json:"status"`
	Reasons []string `json:"reasons"`
}

// issueRef narrows a drupal.Issue to what a payload carries.
//
// Untruncated title, the status and priority as words rather than drupal.org's
// integers, and the patch count — which is the number a consumer deciding
// whether to look at patches actually wants.
func issueRef(issue drupal.Issue) *IssueRef {
	return &IssueRef{
		Nid:      issue.Nid,
		Title:    issue.Title,
		Status:   issue.Status.ShortLabel(),
		Priority: issue.PriorityLabel(),
		Version:  issue.Version,
		URL:      issue.URL,
		Patches:  issue.PatchCount(),
	}
}

// mergeRequestRef narrows a gitlab.MergeRequest to what a payload carries.
func mergeRequestRef(mr gitlab.MergeRequest) *MergeRequestRef {
	return &MergeRequestRef{
		IID:          mr.IID,
		Title:        mr.Title,
		Draft:        mr.Draft,
		SourceBranch: mr.SourceBranch,
		WebURL:       mr.WebURL,
		HeadSHA:      mr.HeadSHA,
	}
}

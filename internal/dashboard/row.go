package dashboard

import (
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/owenbush/upkeep/internal/gate"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/patches"
	"github.com/owenbush/upkeep/internal/results"
)

// Row is one assembled dashboard row.
//
// **A row is one issue's work on one module branch.** That is the whole change
// from what came before, and it removes two multipliers that added rows
// without adding information:
//
//   - An issue used to appear once per merge request *and* again as a patch
//     row, which is why the row factory carried a filter whose only job was to
//     suppress the duplicate it had just created.
//   - Every row was multiplied by the module's tracked core versions. But a
//     module *branch* supports several cores at once — pathauto's single
//     8.x-1.x declares ^10.2 || ^11 || ^12 — so that produced rows describing
//     the same branch and the same work, differing only in a column. Core is
//     **evidence**, and lives in LocalEvidence.
//
// The multiplier that remains is real: an issue with work on two branches is a
// backport, which is two pieces of work rather than one seen twice.
//
// A merge request claiming no issue keeps a row of its own — not an edge case,
// since 33 of pathauto's 162 merge requests claim none — and a module whose
// merge requests cannot be listed gets a visible failure row.
type Row struct {
	Module string
	Branch string
	// IssueNid is 0 when the row carries no issue.
	IssueNid int
	// Contribution is nil for a row with no issue behind it.
	Contribution *patches.Contribution

	// MergeRequests is every merge request this row covers — the issue's,
	// narrowed to this branch.
	MergeRequests []gitlab.MergeRequest
	// MergeRequest is the open one the row acts on: what gets checked, gated
	// and merged. Nil for a row carrying only patches or only a landing.
	MergeRequest *gitlab.MergeRequest

	Project *gitlab.Project
	Local   results.LocalEvidence
	// Verdict is nil when there is no open merge request, which is what makes
	// IsReadyAuto false by construction for a patch-only row rather than by a
	// check someone has to remember.
	Verdict *gate.Verdict

	CIFailure     *gitlab.Failure
	ModuleFailure *gitlab.Failure

	// Landed is a merge request on this row's issue whose work has already
	// landed.
	//
	// The fact a row cannot otherwise carry. Project Update Bot compatibility
	// issues are kept open on purpose so the bot can post again, so an open
	// issue with an open draft on it may nonetheless have had its real work
	// merged — as happens the moment a maintainer promotes a patch, fixes it
	// and merges the result, leaving the bot's draft sitting there looking
	// like the only contribution.
	Landed *gitlab.MergeRequest
	// NewerWorkSinceLanding is whether anything on the issue is newer than
	// that landing.
	NewerWorkSinceLanding bool

	PatchCount int
	// PatchNewerThanMergeRequest is whether the newest patch on the issue
	// postdates the merge request's last update — the old patch↑ flag, which
	// used to be a cross-reference between two rows and is now a statement
	// about one.
	PatchNewerThanMergeRequest bool
}

// RowForIssue is an issue's work on one branch: its merge requests, its
// patches, or both.
//
// Both used to be separate row kinds, and an issue carrying both produced one
// of each — the duplication the covered-by-a-merge-request filter existed to
// hide. Here they are columns of the same row, which is what they always were:
// an issue with a patch in comment 4 and a merge request in comment 9 is one
// piece of work that arrived twice.
//
// open is the open merge request to act on, if any; verdict is nil when there
// is none.
func RowForIssue(
	module, branch string,
	contribution patches.Contribution,
	project *gitlab.Project,
	open *gitlab.MergeRequest,
	local results.LocalEvidence,
	verdict *gate.Verdict,
	ciFailure *gitlab.Failure,
) Row {
	landed := contribution.Landed()

	return Row{
		Module:                     module,
		Branch:                     branch,
		IssueNid:                   contribution.Issue.Nid,
		Contribution:               &contribution,
		MergeRequests:              contribution.MergeRequests,
		MergeRequest:               open,
		Project:                    project,
		Local:                      local,
		Verdict:                    verdict,
		CIFailure:                  ciFailure,
		Landed:                     landed,
		NewerWorkSinceLanding:      landed != nil && contribution.HasWorkNewerThanLanding(),
		PatchCount:                 contribution.Issue.PatchCount(),
		PatchNewerThanMergeRequest: patchIsNewer(contribution, open),
	}
}

// RowForUnlinkedMergeRequest is a merge request with no issue behind it on
// this dashboard.
//
// Either it claims none — 20% of pathauto's merge requests do — or it claims
// one outside the module's open queue, typically an issue already marked
// fixed. Both keep a row: the merge request is open, and an open merge request
// is a contribution whatever its issue says.
//
// issueNid is the nid its metadata claims, when there is one. It is shown, but
// not paired: without the issue there is no status, no patch count and nothing
// to group by. Zero means none.
func RowForUnlinkedMergeRequest(
	module, branch string,
	project *gitlab.Project,
	mergeRequest gitlab.MergeRequest,
	local results.LocalEvidence,
	verdict gate.Verdict,
	ciFailure *gitlab.Failure,
	issueNid int,
) Row {
	return Row{
		Module:        module,
		Branch:        branch,
		IssueNid:      issueNid,
		MergeRequests: []gitlab.MergeRequest{mergeRequest},
		MergeRequest:  &mergeRequest,
		Project:       project,
		Local:         local,
		Verdict:       &verdict,
		CIFailure:     ciFailure,
	}
}

// RowForModuleFailure keeps a visible row for a module whose merge requests
// cannot be listed.
func RowForModuleFailure(module string, failure *gitlab.Failure) Row {
	return Row{
		Module:        module,
		Branch:        "-",
		Local:         results.NoEvidence(),
		ModuleFailure: failure,
	}
}

// IsReadyAuto reports whether the fast lane may take this row.
func (r Row) IsReadyAuto() bool {
	return r.Verdict != nil && r.Verdict.Status == gate.ReadyAuto
}

// RequireMergeRequest is the merge request this row describes.
//
// A real guard rather than an assertion: this invariant is load-bearing — the
// merge command feeds the result straight into a merge call.
func (r Row) RequireMergeRequest() (gitlab.MergeRequest, error) {
	if r.MergeRequest == nil {
		return gitlab.MergeRequest{}, notAMergeRequestRow("merge request")
	}

	return *r.MergeRequest, nil
}

// RequireProject is the project this row's merge request belongs to.
func (r Row) RequireProject() (gitlab.Project, error) {
	if r.Project == nil {
		return gitlab.Project{}, notAMergeRequestRow("project")
	}

	return *r.Project, nil
}

// RequireVerdict is the gate verdict for this row.
func (r Row) RequireVerdict() (gate.Verdict, error) {
	if r.Verdict == nil {
		return gate.Verdict{}, notAMergeRequestRow("gate verdict")
	}

	return *r.Verdict, nil
}

func notAMergeRequestRow(what string) error {
	return fmt.Errorf("dashboard row has no %s: it carries no open merge request", what)
}

// TableCells is the row exactly as the dashboard table renders it: MODULE,
// ISSUE, VERSION, TITLE, MR, PATCH, CI, LOCAL, STATUS, NEXT.
//
// STATUS is a phrase and NEXT is a command, because a row that says only what
// it *is* leaves a maintainer with a hundred of them and no idea which to
// touch. The gate's own reason tokens are still available — StatusCell renders
// them, and the dashboard prints them under -v.
//
// verbose swaps the phrase for the gate's reason tokens, and the LOCAL cell
// for every core's own state.
func (r Row) TableCells(verbose bool) []string {
	guidance := GuidanceFor(r)

	if r.ModuleFailure != nil {
		cell := FailureCell(r.ModuleFailure)

		return []string{
			r.Module, "–", "–", r.TitleCell(), "–", "–", cell, "–", cell, guidance.Command,
		}
	}

	local := r.Local.Cell()
	status := guidance.Status
	if verbose {
		local = r.Local.Describe()
		status = r.StatusCell()
	}

	return []string{
		r.Module,
		r.IssueCell(),
		r.Branch,
		Truncate(r.TitleCell(), 0),
		r.MergeRequestCell(),
		r.PatchCell(),
		r.CICell(),
		local,
		status,
		guidance.Command,
	}
}

// IssueCell is the nid and drupal.org's own status word.
//
// The status is the half that was missing. "#3598272 review" and
// "#3598272 RTBC" call for different things from a maintainer, and the
// dashboard used to print the same cell for both.
func (r Row) IssueCell() string {
	if r.Contribution != nil {
		return strconv.Itoa(r.Contribution.Issue.Nid) + " " + r.Contribution.Issue.Status.ShortLabel()
	}
	if r.IssueNid == 0 {
		return "–"
	}

	return strconv.Itoa(r.IssueNid)
}

// TitleCell is the issue's title, or the merge request's when there is no
// issue.
func (r Row) TitleCell() string {
	if r.Contribution != nil {
		return r.Contribution.Issue.Title
	}
	if r.MergeRequest != nil {
		return r.MergeRequest.Title
	}

	return "(merge requests unavailable)"
}

// MergeRequestCell is the representative merge request, or the landing that
// outranks it.
func (r Row) MergeRequestCell() string {
	return patches.RenderMergeRequestCell(r.MergeRequests, r.Landed, r.NewerWorkSinceLanding)
}

// PatchCell is how many patch files the issue carries, flagged when the newest
// of them postdates the merge request.
//
// patch↑ used to live in the ISSUE column of a merge-request row and mean "the
// issue this merge request mentions has a newer patch on it" — a
// cross-reference between two rows that no glossary explained and nobody could
// read. Here the patch and the merge request are the same row, so the flag is
// a statement about one thing.
func (r Row) PatchCell() string {
	if r.PatchCount == 0 {
		return "–"
	}
	if r.PatchNewerThanMergeRequest {
		return strconv.Itoa(r.PatchCount) + " ↑"
	}

	return strconv.Itoa(r.PatchCount)
}

// CICell is the pipeline state, or the explicit typed-failure state.
func (r Row) CICell() string {
	if r.CIFailure != nil {
		return FailureCell(r.CIFailure)
	}
	if r.MergeRequest == nil || r.MergeRequest.HeadPipeline == nil {
		return "–"
	}

	pipeline := r.MergeRequest.HeadPipeline
	switch {
	case pipeline.Status.IsGreen():
		return "pass"
	case pipeline.Status == gitlab.StatusFailed:
		return "fail"
	case pipeline.RawStatus != "":
		return pipeline.RawStatus
	default:
		return string(pipeline.Status)
	}
}

// LocalCell is the worst state across every applicable core, naming the core
// it came from.
func (r Row) LocalCell() string { return r.Local.Cell() }

// StatusCell is the gate verdict, the contribution kind, or the failure.
func (r Row) StatusCell() string {
	if r.ModuleFailure != nil {
		return FailureCell(r.ModuleFailure)
	}
	if r.Verdict != nil {
		return r.Verdict.Describe()
	}
	if r.Contribution != nil {
		return r.Contribution.DashboardStatus()
	}

	return "–"
}

// FailureCell is the compact, explicit cell state for a typed client failure,
// e.g. "n/a (403)".
//
// The discriminator lives on the failure itself, so this cannot drift out of
// step with the taxonomy.
func FailureCell(failure *gitlab.Failure) string {
	return "n/a (" + failure.ShortCode() + ")"
}

// Truncate shortens a title to fit the table. A max of 0 or less takes the
// default.
//
// Counted in runes rather than bytes, which is what mb_substr does on the PHP
// side: a title with an accented character must not lose a byte and become
// invalid UTF-8 in the middle of a table.
// DefaultTitleWidth is how wide a title cell is when a caller has no reason to
// choose: wide enough that most drupal.org issue titles survive whole, narrow
// enough that the columns after it stay on one line.
const DefaultTitleWidth = 44

func Truncate(title string, max int) string {
	if max <= 0 {
		max = DefaultTitleWidth
	}
	if utf8.RuneCountInString(title) <= max {
		return title
	}

	return string([]rune(title)[:max-1]) + "…"
}

// patchIsNewer reports whether the issue's newest patch postdates the merge
// request's last update — somebody posted a re-roll the branch does not carry.
func patchIsNewer(contribution patches.Contribution, open *gitlab.MergeRequest) bool {
	latest, found := contribution.Issue.LatestPatch()
	if !found || latest.Timestamp <= 0 || open == nil || open.UpdatedAt == "" {
		return false
	}

	updated, ok := parseSnapshotTime(open.UpdatedAt)
	if !ok {
		return false
	}

	return latest.Timestamp > updated.Unix()
}

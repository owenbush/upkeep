package report

import (
	"time"

	"github.com/owenbush/upkeep/internal/dashboard"
	"github.com/owenbush/upkeep/internal/gitlab"
)

// OverviewPayload is `dashboard` with no module named.
type OverviewPayload struct {
	Modules []ModuleLine `json:"modules"`
}

// ModuleLine is one module's summary.
//
// Including ReadyAuto, Review and Blocked, which the table does not show: it
// prints READY and CI FAILED and deliberately has no REVIEW column, because
// that counted everything neither ready nor failed, which is nearly every row.
// A column that says nothing is noise on a terminal; a field a consumer can
// ignore costs it nothing.
type ModuleLine struct {
	Module        string   `json:"module"`
	Branches      []string `json:"branches"`
	MergeRequests int      `json:"merge_requests"`
	PatchIssues   int      `json:"patch_issues"`
	ReadyAuto     int      `json:"ready_auto"`
	Review        int      `json:"review"`
	Blocked       int      `json:"blocked"`
	Unchecked     int      `json:"unchecked"`
	// Failed is whether reading this module fell short, in which case the
	// counts above are lower bounds.
	Failed bool `json:"failed"`
	// CachedAt is when the snapshot was taken, or null when there is none.
	CachedAt *time.Time `json:"cached_at"`
}

// Overview builds the per-module payload.
//
// Aggregated from exactly the rows a drill-down would print, through the same
// dashboard.SummaryFromRows the table uses. Two counts of the same thing that
// can disagree are worse than one, and that applies across output formats as
// much as within one.
func Overview(
	rows []dashboard.Row, snapshots map[string]dashboard.ModuleSnapshot,
) OverviewPayload {
	byModule := map[string][]dashboard.Row{}
	order := []string{}
	for _, row := range rows {
		if _, seen := byModule[row.Module]; !seen {
			order = append(order, row.Module)
		}
		byModule[row.Module] = append(byModule[row.Module], row)
	}

	lines := make([]ModuleLine, 0, len(order))
	for _, module := range order {
		summary := dashboard.SummaryFromRows(module, byModule[module])

		var cachedAt *time.Time
		if snapshot, cached := snapshots[module]; cached && !snapshot.FetchedAt.IsZero() {
			at := snapshot.FetchedAt
			cachedAt = &at
		}

		branches := summary.Branches
		if branches == nil {
			branches = []string{}
		}

		lines = append(lines, ModuleLine{
			Module:        summary.Module,
			Branches:      branches,
			MergeRequests: summary.MergeRequests,
			PatchIssues:   summary.PatchIssues,
			ReadyAuto:     summary.ReadyAuto,
			Review:        summary.Review,
			Blocked:       summary.Blocked,
			Unchecked:     summary.Unchecked,
			Failed:        summary.Failed,
			CachedAt:      cachedAt,
		})
	}

	return OverviewPayload{Modules: lines}
}

// RowsPayload is `dashboard <module>`, the drill-down.
type RowsPayload struct {
	Rows []RowLine `json:"rows"`
}

// RowLine is one piece of work: an issue's contribution on one module branch.
type RowLine struct {
	Module string `json:"module"`
	Branch string `json:"branch"`
	Title  string `json:"title"`

	// Issue is null for a merge request claiming no listed issue, which 33 of
	// pathauto's 162 rows do.
	Issue *IssueRef `json:"issue"`
	// MergeRequest is the open one the row acts on, null for a row carrying
	// only patches or only a landing.
	MergeRequest *MergeRequestRef `json:"merge_request"`
	// OpenMergeRequests is how many this row covers, which can exceed one.
	OpenMergeRequests int `json:"open_merge_requests"`

	// CI is the head pipeline, null when there is none.
	CI *PipelineRef `json:"ci"`
	// CIUnavailable is why the pipeline could not be read, empty when it
	// could. Distinct from a null CI, which means there is no pipeline:
	// collapsing the two would report "no CI" for a rate limit.
	CIUnavailable string `json:"ci_unavailable,omitempty"`
	// ModuleUnavailable is why the whole module could not be read. When set,
	// everything else on this row is unknown rather than absent.
	ModuleUnavailable string `json:"module_unavailable,omitempty"`

	// Local is each applicable core's local check state: pass, fail, stale or
	// unchecked, the same four words the table prints.
	Local map[string]string `json:"local"`
	// Verdict is the fast-lane classification, null for a row with no open
	// merge request — which is what makes ReadyAuto false by construction
	// there rather than by a check somebody has to remember.
	//
	// Its reasons are already machine-readable tokens ("ci-red",
	// "local-failed:phpunit"), which behind -v is all the text surface does
	// with them. Here they are the useful half.
	Verdict   *VerdictRef `json:"verdict"`
	ReadyAuto bool        `json:"ready_auto"`

	// Landed names work on this issue that has already merged, which an open
	// row can carry: bot compatibility issues are kept open by convention, so
	// an open issue may have had its real work land months ago.
	Landed *Landing `json:"landed"`

	// Status and Next are the dashboard's product rather than its formatting:
	// the plain-English state and the literal command to run. Taken from the
	// same dashboard.GuidanceFor the table uses, so the two cannot disagree
	// about what to do with this row.
	Status string `json:"status"`
	Next   string `json:"next"`
}

// Landing is a merge request on this row's issue whose work already merged.
type Landing struct {
	IID      int    `json:"iid"`
	MergedAt string `json:"merged_at,omitempty"`
	WebURL   string `json:"web_url,omitempty"`
	// NewerWork is whether anything on the issue postdates the merge, which is
	// what separates "this is done" from "this landed and then moved on".
	NewerWork bool `json:"newer_work"`
}

// Rows builds the drill-down payload.
func Rows(rows []dashboard.Row) RowsPayload {
	lines := make([]RowLine, 0, len(rows))
	for _, row := range rows {
		guidance := dashboard.GuidanceFor(row)

		line := RowLine{
			Module:            row.Module,
			Branch:            row.Branch,
			Title:             row.TitleCell(),
			OpenMergeRequests: len(row.MergeRequests),
			Local:             row.Local.States(),
			ReadyAuto:         row.IsReadyAuto(),
			Status:            guidance.Status,
			Next:              guidance.Command,
		}
		if line.Local == nil {
			line.Local = map[string]string{}
		}

		if row.Contribution != nil {
			line.Issue = issueRef(row.Contribution.Issue)
		} else if row.IssueNid != 0 {
			line.Issue = &IssueRef{Nid: row.IssueNid}
		}

		if row.MergeRequest != nil {
			line.MergeRequest = mergeRequestRef(*row.MergeRequest)
			line.CI = pipelineRef(row.MergeRequest.HeadPipeline)
		}
		if row.CIFailure != nil {
			line.CIUnavailable = row.CIFailure.ShortCode()
		}
		if row.ModuleFailure != nil {
			line.ModuleUnavailable = row.ModuleFailure.ShortCode()
		}

		if row.Verdict != nil {
			reasons := row.Verdict.Reasons
			if reasons == nil {
				reasons = []string{}
			}
			line.Verdict = &VerdictRef{
				Status:  string(row.Verdict.Status),
				Reasons: reasons,
			}
		}

		if row.Landed != nil {
			line.Landed = &Landing{
				IID:       row.Landed.IID,
				MergedAt:  row.Landed.MergedAt,
				WebURL:    row.Landed.WebURL,
				NewerWork: row.NewerWorkSinceLanding,
			}
		}

		lines = append(lines, line)
	}

	return RowsPayload{Rows: lines}
}

func pipelineRef(pipeline *gitlab.Pipeline) *PipelineRef {
	if pipeline == nil {
		return nil
	}

	status := pipeline.RawStatus
	if status == "" {
		status = string(pipeline.Status)
	}

	return &PipelineRef{Status: status, Green: pipeline.Status.IsGreen()}
}

package dashboard

import (
	"strconv"
	"strings"

	"github.com/owenbush/upkeep/internal/gate"
)

// ModuleSummary is one module's row on the overview dashboard.
//
// Derived from exactly the rows the detailed view would render, never
// recounted from the underlying data — so a module whose overview says three
// READY-AUTO shows three READY-AUTO when you drill into it. Two counts of the
// same thing that can disagree are worse than one count.
//
// The overview exists because per-module volume is real: a mature contrib
// project can carry a hundred open merge requests and forty open issues, and a
// cockpit-wide detailed table stopped being readable some time before it
// stopped being printable.
type ModuleSummary struct {
	Module string
	// Branches are the module branches this run's rows sit on.
	Branches      []string
	MergeRequests int
	ReadyAuto     int
	Review        int
	Blocked       int
	PatchIssues   int
	Unchecked     int
	Failed        bool
}

// SummaryFromRows aggregates every row for one module.
func SummaryFromRows(module string, rows []Row) ModuleSummary {
	summary := ModuleSummary{Module: module, Branches: []string{}}

	seenBranch := map[string]bool{}
	seenMR := map[int]bool{}
	seenPatchIssue := map[int]bool{}

	for _, row := range rows {
		if row.ModuleFailure != nil {
			summary.Failed = true

			continue
		}

		if row.Branch != "-" && !seenBranch[row.Branch] {
			seenBranch[row.Branch] = true
			summary.Branches = append(summary.Branches, row.Branch)
		}

		// Counted per distinct subject, not per row. Rows are no longer
		// multiplied by core, but an issue backported to two branches is still
		// two rows over one set of merge requests.
		for _, mergeRequest := range row.MergeRequests {
			if mergeRequest.State != "merged" {
				seenMR[mergeRequest.IID] = true
			}
		}
		if row.IssueNid != 0 && row.PatchCount > 0 {
			seenPatchIssue[row.IssueNid] = true
		}

		if row.Verdict != nil {
			switch row.Verdict.Status {
			case gate.ReadyAuto:
				summary.ReadyAuto++
			case gate.Review:
				summary.Review++
			case gate.Blocked:
				summary.Blocked++
			}
		}

		// A row counts as unchecked when *any* applicable core lacks fresh
		// evidence. Stricter than the cell it summarises, and deliberately:
		// the number exists to say how much work stands between the queue and
		// a verdict.
		if row.Local.AnyUnchecked() || row.Local.AnyStale() {
			summary.Unchecked++
		}
	}

	summary.MergeRequests = len(seenMR)
	summary.PatchIssues = len(seenPatchIssue)

	return summary
}

// TableCells is the overview row: MODULE, BRANCHES, MRS, PATCH ISSUES, READY,
// CI FAILED, UNCHECKED, CACHED.
//
// The cache age is the caller's — it comes from the snapshot, not from the
// rows.
//
// Three deliberate departures from the gate's own vocabulary, so the overview
// reads the same way as the rows it summarises:
//
//   - "PATCH ISSUES", not "PATCHES", because it counts *issues* carrying
//     patches while a row's own patch count is *files*. One word for two
//     things is how a summary comes to disagree with its detail.
//   - "CI FAILED", not "BLOCKED", because that is what the gate's Blocked
//     verdict is set by and the only thing it is set by.
//   - No REVIEW column. It counted everything neither ready nor CI-failed,
//     which on a real module is every row — a number that is always the total
//     tells a maintainer nothing. What is actionable is UNCHECKED, which is
//     beside it. The count is still on the struct for anything that wants it.
func (s ModuleSummary) TableCells(cacheAge string) []string {
	if s.Failed {
		return []string{s.Module, "–", "–", "–", "–", "–", "–", cacheAge}
	}

	branches := "–"
	if len(s.Branches) > 0 {
		branches = strings.Join(s.Branches, ",")
	}

	return []string{
		s.Module,
		branches,
		strconv.Itoa(s.MergeRequests),
		strconv.Itoa(s.PatchIssues),
		dashIfZero(s.ReadyAuto),
		dashIfZero(s.Blocked),
		dashIfZero(s.Unchecked),
		cacheAge,
	}
}

// dashIfZero reads better than a nought: the eye should catch the non-zero
// cells.
func dashIfZero(value int) string {
	if value == 0 {
		return "–"
	}

	return strconv.Itoa(value)
}

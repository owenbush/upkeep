package dashboard

import (
	"sort"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gate"
	"github.com/owenbush/upkeep/internal/gitlab"
)

// Reader is the GitLab surface the assembler needs.
//
// An interface so the assembler can be exercised without a network, and so it
// cannot reach a write: the whole thing is strictly read-only, and the type
// says so.
type Reader interface {
	Project(moduleOrPath string) (*gitlab.Project, *gitlab.Failure)
	OpenMergeRequests(project gitlab.Project) ([]gitlab.MergeRequest, *gitlab.Failure)
	MergeRequest(project gitlab.Project, iid int) (*gitlab.MergeRequest, *gitlab.Failure)
}

// Assembler fetches each registered module's project and open merge requests
// from GitLab, then hands them to the row factory — the one canonical
// classification pipeline the dashboard also uses.
//
// Strictly read-only: registry data, GET-backed client calls, and the results
// cache. Typed client failures become explicit row states, never crashes.
type Assembler struct {
	client  Reader
	factory *RowFactory
}

// NewAssembler wires a reader to the classification pipeline.
func NewAssembler(client Reader, cache EvidenceStore, fastLane *gate.FastLane) *Assembler {
	return &Assembler{client: client, factory: NewRowFactory(cache, fastLane)}
}

// Assemble is every module's rows, ordered by module name, then merge-request
// iid.
//
// versionFilter gathers evidence for one core only, when given.
func (a *Assembler) Assemble(modules map[string]cockpit.Module, versionFilter string) []Row {
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := []Row{}
	for _, name := range names {
		rows = append(rows, a.moduleRows(modules[name], versionFilter)...)
	}

	return rows
}

// moduleRows is one module's rows: one per open merge request.
//
// No snapshot, so no issues and nothing to group by — every merge request is
// its own row here, where the dashboard would gather an issue's onto one. The
// *classification* is identical (same gate, same evidence, same cores), which
// is the property that matters: the fast lane prompts per merge request, so a
// row per merge request is exactly what it wants.
func (a *Assembler) moduleRows(module cockpit.Module, versionFilter string) []Row {
	if len(Cores(module, versionFilter)) == 0 {
		return nil
	}

	project, failure := a.client.Project(module.Project)
	if failure != nil {
		return []Row{RowForModuleFailure(module.Name, failure)}
	}

	listed, failure := a.client.OpenMergeRequests(*project)
	if failure != nil {
		return []Row{RowForModuleFailure(module.Name, failure)}
	}

	mergeRequests := make([]gitlab.MergeRequest, 0, len(listed))
	ciFailures := map[int]*gitlab.Failure{}
	for _, one := range listed {
		// The list payload lacks head_pipeline; the single-merge-request
		// endpoint provides it, memoized by the client. If that fetch fails,
		// fall back to the listed data: the row still renders, the CI cell
		// carries the failure state, and the gate — seeing no pipeline —
		// conservatively denies READY-AUTO.
		detail, failure := a.client.MergeRequest(*project, one.IID)
		if failure != nil {
			ciFailures[one.IID] = failure
			mergeRequests = append(mergeRequests, one)

			continue
		}
		mergeRequests = append(mergeRequests, *detail)
	}

	return a.factory.Rows(RowsInput{
		Module:        module,
		Project:       *project,
		MergeRequests: mergeRequests,
		VersionFilter: versionFilter,
		CIFailures:    ciFailures,
	})
}

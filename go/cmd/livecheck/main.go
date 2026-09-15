// Command livecheck exercises the ported domain core against the real
// drupal.org and git.drupalcode.org APIs.
//
// Not a test: it needs a network, it reads a project that changes under it,
// and it asserts shapes rather than values. What it is for is the thing the
// unit suite structurally cannot do — prove that the narrowing, the pairing
// and the row model survive contact with data nobody wrote down.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/dashboard"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/patches"
	"github.com/owenbush/upkeep/internal/results"
)

func main() {
	module := "pathauto"
	if len(os.Args) > 1 {
		module = os.Args[1]
	}

	fmt.Printf("=== live check: %s ===\n\n", module)

	gl := gitlab.NewClient(nil, "", "", "")
	dl := drupal.NewClient(nil, "")

	project, failure := gl.Project(module)
	if failure != nil {
		fmt.Printf("FAILED: project: %s\n", failure.Message)
		os.Exit(1)
	}
	fmt.Printf("project      %s (id %d, default %s)\n", project.PathWithNamespace, project.ID, project.DefaultBranch)
	fmt.Printf("ssh url      %s\n", project.SSHURL)
	fmt.Printf("can push     %v (unknown is correct when anonymous)\n\n", project.CanPush())

	open, failure := gl.OpenMergeRequests(*project)
	if failure != nil {
		fmt.Printf("FAILED: open merge requests: %s\n", failure.Message)
		os.Exit(1)
	}
	merged, failure := gl.MergedMergeRequests(*project, 100)
	if failure != nil {
		fmt.Printf("FAILED: merged merge requests: %s\n", failure.Message)
		os.Exit(1)
	}
	forks, failure := gl.IssueForkNids(*project)
	if failure != nil {
		fmt.Printf("FAILED: forks: %s\n", failure.Message)
		os.Exit(1)
	}
	fmt.Printf("merge requests  %d open, %d merged\n", len(open), len(merged))
	fmt.Printf("issue forks     %d\n", len(forks))

	branches, failure := gl.BranchNames(*project)
	if failure != nil {
		fmt.Printf("FAILED: branches: %s\n", failure.Message)
		os.Exit(1)
	}
	fmt.Printf("branches        %d: %s\n\n", len(branches), strings.Join(first(branches, 6), ", "))

	// The info.yml read that decides which cores a branch's evidence is worth
	// gathering on — the thing a regex over majors gets wrong.
	constraints := map[string]string{}
	for _, branch := range branches {
		info := gl.FileContents(*project, module+".info.yml", branch)
		if constraint := drupal.ConstraintIn(info); constraint != "" {
			constraints[branch] = constraint
		}
	}
	fmt.Println("core_version_requirement, per branch:")
	for _, branch := range sorted(constraints) {
		applicable := "everything tracked"
		if compat, ok := drupal.CompatibilityFrom(constraints[branch], []string{"9", "10", "11", "12"}); ok {
			applicable = strings.Join(compat.Cores, ",")
		}
		fmt.Printf("  %-12s %-28s -> cores %s\n", branch, constraints[branch], applicable)
	}
	fmt.Println()

	issues := dl.ProjectIssues(module, drupal.OpenStatuses())
	fmt.Printf("open issues     %d\n", len(issues))
	withPatches := 0
	attachments := 0
	for _, issue := range issues {
		attachments += len(issue.Files)
		if issue.PatchCount() > 0 {
			withPatches++
		}
	}
	fmt.Printf("attachments     %d resolved across them (%d issues carry a patch)\n", attachments, withPatches)
	for _, warning := range dl.Warnings() {
		fmt.Printf("  warning: %s\n", warning)
	}
	fmt.Println()

	// The pairing, which is the piece with the most judgement in it.
	all := append(append([]gitlab.MergeRequest{}, open...), merged...)
	contributions := patches.Pair(module, issues, all, forks)

	byKind := map[patches.Kind]int{}
	pairedByFork, pairedByMetadata := 0, 0
	for _, contribution := range contributions {
		byKind[contribution.Kind()]++
		for _, mr := range contribution.MergeRequests {
			if _, viaFork := forks[mr.SourceProjectID]; viaFork {
				pairedByFork++
			} else {
				pairedByMetadata++
			}
		}
	}
	fmt.Printf("paired          %d by fork, %d by metadata\n", pairedByFork, pairedByMetadata)
	for kind, label := range map[patches.Kind]string{
		patches.PatchOnly:                  "patch-only",
		patches.PatchAndMergeRequest:       "patch + MR",
		patches.PatchWithEmptyMergeRequest: "patch, empty MR",
		patches.MergeRequestOnly:           "MR-only",
		patches.Nothing:                    "nothing attached",
	} {
		fmt.Printf("  %-18s %d\n", label, byKind[kind])
	}
	fmt.Println()

	// And the row model, assembled from a snapshot exactly as the dashboard
	// builds one.
	snapshot := buildSnapshot(*project, open, merged, issues, forks, constraints, gl)
	factory := dashboard.NewRowFactory(emptyStore{}, nil)
	rows := factory.Rows(dashboard.RowsInput{
		Module:        cockpit.Module{Name: module, Project: project.PathWithNamespace, CoreVersions: []string{"10", "11", "12"}},
		Project:       *project,
		MergeRequests: open,
		Snapshot:      &snapshot,
	})

	fmt.Printf("rows            %d (from %d open merge requests and %d open issues)\n", len(rows), len(open), len(issues))

	landed, withGuidance, emptyMRs := 0, 0, 0
	for _, row := range rows {
		if row.Landed != nil {
			landed++
		}
		if dashboard.GuidanceFor(row).Command != "" {
			withGuidance++
		}
		if row.MergeRequest != nil && row.MergeRequest.CarriesChanges() == gitlab.No {
			emptyMRs++
		}
	}
	fmt.Printf("  with a landing        %d\n", landed)
	fmt.Printf("  empty merge requests  %d\n", emptyMRs)
	fmt.Printf("  yielding a command    %d / %d\n\n", withGuidance, len(rows))

	if withGuidance != len(rows) {
		fmt.Println("FAILED: not every row yielded a command")
		os.Exit(1)
	}

	fmt.Println("first ten rows, as the dashboard renders them:")
	for i, row := range rows {
		if i >= 10 {
			break
		}
		cells := row.TableCells(false)
		fmt.Printf("  %-9s %-18s %-8s %-30s %-14s %-4s %-8s %-14s %-26s %s\n",
			cells[0], cells[1], cells[2], truncate(cells[3], 30), cells[4], cells[5], cells[6], cells[7],
			truncate(cells[8], 26), cells[9])
	}

	fmt.Printf("\nsummary: %v\n", dashboard.SummaryFromRows(module, rows).TableCells("just now"))
	fmt.Println("\n=== ok ===")
}

func buildSnapshot(
	project gitlab.Project,
	open, merged []gitlab.MergeRequest,
	issues []drupal.Issue,
	forks map[int]int,
	constraints map[string]string,
	gl *gitlab.Client,
) dashboard.ModuleSnapshot {
	issueData := []map[string]any{}
	for _, issue := range issues {
		issueData = append(issueData, issue.ToAPIMap())
	}
	mergedData := []map[string]any{}
	for _, mr := range merged {
		mergedData = append(mergedData, mr.ToAPIMap())
	}
	openData := []map[string]any{}
	mergeRefs := map[int]string{}
	for _, mr := range open {
		openData = append(openData, mr.ToAPIMap())
		if sha := gl.MergeRefSHA(project, mr.IID); sha != "" {
			mergeRefs[mr.IID] = sha
		}
	}

	return dashboard.ModuleSnapshot{
		FetchedAt:       time.Now(),
		ProjectData:     project.ToAPIMap(),
		MRData:          openData,
		PatchIssueData:  issueData,
		MergedMRData:    mergedData,
		ForkNids:        forks,
		CoreConstraints: constraints,
		MergeRefSHAs:    mergeRefs,
	}
}

// emptyStore has no local results, which is the honest state for a machine
// that has never run a check.
type emptyStore struct{}

func (emptyStore) Latest(string, results.Key, string) (*results.CachedResult, error) { return nil, nil }

func first(values []string, n int) []string {
	if len(values) <= n {
		return values
	}

	return values[:n]
}

func sorted(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

func truncate(value string, max int) string {
	if len([]rune(value)) <= max {
		return value
	}

	return string([]rune(value)[:max-1]) + "…"
}

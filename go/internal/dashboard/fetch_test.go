package dashboard

import (
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
)

// fakeSnapshotReader answers a refresh, and records what it was asked.
type fakeSnapshotReader struct {
	fakeReader

	merged       []gitlab.MergeRequest
	mergedFail   *gitlab.Failure
	forkNids     map[int]int
	forkFail     *gitlab.Failure
	infoByBranch map[string]string
	branchesRead []string
}

func (r *fakeSnapshotReader) MergedMergeRequests(
	gitlab.Project, int,
) ([]gitlab.MergeRequest, *gitlab.Failure) {
	return r.merged, r.mergedFail
}

func (r *fakeSnapshotReader) IssueForkNids(gitlab.Project) (map[int]int, *gitlab.Failure) {
	return r.forkNids, r.forkFail
}

func (r *fakeSnapshotReader) FileContents(_ gitlab.Project, _, ref string) string {
	r.branchesRead = append(r.branchesRead, ref)

	return r.infoByBranch[ref]
}

// fakeIssues answers a drupal.org scan.
type fakeIssues struct{ issues []drupal.Issue }

func (f fakeIssues) ProjectIssues(string, []drupal.IssueStatus) []drupal.Issue { return f.issues }

func aModule() cockpit.Module {
	return cockpit.Module{
		Name: "pathauto", Project: "project/pathauto",
		CoreVersions: []string{"11"}, Watched: true,
	}
}

// A snapshot carries everything a row needs, so the dashboard can render from
// it without going back to the network.
func TestASnapshotCarriesEverythingARowNeeds(t *testing.T) {
	reader := &fakeSnapshotReader{
		fakeReader: fakeReader{
			project: &gitlab.Project{
				ID: 1, Path: "pathauto", PathWithNamespace: "project/pathauto",
				DefaultBranch: "2.0.x",
			},
			listed: []gitlab.MergeRequest{
				{IID: 12, State: "opened", TargetBranch: "2.0.x", HeadSHA: "aaaa"},
			},
			mergeRefSHAs: map[int]string{12: "bbbb"},
		},
		merged: []gitlab.MergeRequest{
			{IID: 9, State: "merged", TargetBranch: "1.0.x"},
		},
		forkNids:     map[int]int{77: 3601234},
		infoByBranch: map[string]string{"2.0.x": "core_version_requirement: ^10.2 || ^11\n"},
	}

	snapshot, failure := FetchModule(
		reader, fakeIssues{issues: []drupal.Issue{{Nid: 3601234, Title: "Fix it", Status: drupal.StatusNeedsReview}}},
		aModule(), time.Now(),
	)
	if failure != nil {
		t.Fatalf("fetch: %s", failure.Message)
	}

	if len(snapshot.MergeRequests()) != 1 {
		t.Errorf("open merge requests: %+v", snapshot.MergeRequests())
	}
	// Merged ones too: without them an issue whose work has already landed
	// reads exactly like one nobody has touched.
	if len(snapshot.MergedMergeRequests()) != 1 {
		t.Errorf("merged merge requests: %+v", snapshot.MergedMergeRequests())
	}
	// The only thing that pairs a bot merge request to its issue.
	if snapshot.ForkNids[77] != 3601234 {
		t.Errorf("fork nids: %v", snapshot.ForkNids)
	}
	if len(snapshot.PatchIssues()) != 1 {
		t.Errorf("issues: %+v", snapshot.PatchIssues())
	}
	// The merge ref, which is what a check of this merge request is about.
	if snapshot.MergeRefSHAs[12] != "bbbb" {
		t.Errorf("merge refs: %v", snapshot.MergeRefSHAs)
	}
	if snapshot.CoreConstraints["2.0.x"] != "^10.2 || ^11" {
		t.Errorf("constraints: %v", snapshot.CoreConstraints)
	}
}

// Every branch a row could sit on is asked about, and nothing else: the
// default branch plus the targets of the open and merged merge requests.
func TestOnlyTheBranchesARowCouldSitOnAreRead(t *testing.T) {
	reader := &fakeSnapshotReader{
		fakeReader: fakeReader{
			project: &gitlab.Project{ID: 1, PathWithNamespace: "project/pathauto", DefaultBranch: "2.0.x"},
			listed: []gitlab.MergeRequest{
				{IID: 12, State: "opened", TargetBranch: "2.0.x"},
				{IID: 13, State: "opened", TargetBranch: "3.0.x"},
			},
		},
		merged: []gitlab.MergeRequest{{IID: 9, State: "merged", TargetBranch: "1.0.x"}},
	}

	if _, failure := FetchModule(reader, fakeIssues{}, aModule(), time.Now()); failure != nil {
		t.Fatalf("fetch: %s", failure.Message)
	}

	read := map[string]bool{}
	for _, branch := range reader.branchesRead {
		if read[branch] {
			t.Errorf("%s was read twice", branch)
		}
		read[branch] = true
	}
	for _, branch := range []string{"2.0.x", "3.0.x", "1.0.x"} {
		if !read[branch] {
			t.Errorf("%s was never asked about", branch)
		}
	}
	if len(read) != 3 {
		t.Errorf("read %v", reader.branchesRead)
	}
}

// Every optional half degrades on its own: losing the merged listing, the fork
// map or a branch's info.yml costs exactly what it carries, and never the
// module.
//
// A module vanishing from the dashboard is the worst failure this tool has.
func TestEveryOptionalHalfDegradesOnItsOwn(t *testing.T) {
	base := func() *fakeSnapshotReader {
		return &fakeSnapshotReader{
			fakeReader: fakeReader{
				project: &gitlab.Project{ID: 1, PathWithNamespace: "project/pathauto"},
				listed:  []gitlab.MergeRequest{{IID: 12, State: "opened", TargetBranch: "2.0.x"}},
			},
			merged:       []gitlab.MergeRequest{{IID: 9, State: "merged", TargetBranch: "1.0.x"}},
			forkNids:     map[int]int{77: 3601234},
			infoByBranch: map[string]string{"2.0.x": "core_version_requirement: ^11\n"},
		}
	}

	refused := &gitlab.Failure{Kind: gitlab.EndpointClosed, Message: "403"}

	for name, breaks := range map[string]func(*fakeSnapshotReader){
		"the merged listing": func(r *fakeSnapshotReader) { r.mergedFail = refused; r.merged = nil },
		"the fork map":       func(r *fakeSnapshotReader) { r.forkFail = refused; r.forkNids = nil },
		"every info.yml":     func(r *fakeSnapshotReader) { r.infoByBranch = nil },
	} {
		reader := base()
		breaks(reader)

		snapshot, failure := FetchModule(reader, fakeIssues{}, aModule(), time.Now())
		if failure != nil {
			t.Errorf("%s failing lost the whole module: %s", name, failure.Message)

			continue
		}
		// The open merge requests — the thing a row is made of — survive
		// whatever else went missing.
		if len(snapshot.MergeRequests()) != 1 {
			t.Errorf("%s failing cost the merge requests too", name)
		}
	}
}

// The two halves that *are* the module refuse: without a project or its open
// merge requests there is no row to render, and a row invented from nothing
// would be worse than a failure row saying so.
func TestTheTwoEssentialHalvesRefuse(t *testing.T) {
	refused := &gitlab.Failure{Kind: gitlab.NotFound, Message: "404"}

	noProject := &fakeSnapshotReader{fakeReader: fakeReader{projectFailure: refused}}
	if _, failure := FetchModule(noProject, fakeIssues{}, aModule(), time.Now()); failure == nil {
		t.Error("a module with no project fetched a snapshot")
	}

	noList := &fakeSnapshotReader{fakeReader: fakeReader{
		project:     &gitlab.Project{ID: 1, PathWithNamespace: "project/pathauto"},
		listFailure: refused,
	}}
	if _, failure := FetchModule(noList, fakeIssues{}, aModule(), time.Now()); failure == nil {
		t.Error("a module whose merge requests could not be listed fetched a snapshot")
	}
}

// A merge request with no merge ref contributes no entry, so the revision
// falls back to its head SHA — GitLab publishes none for one that conflicts
// with its target, and the adapter checks the branch alone there.
func TestAMergeRequestWithNoMergeRefContributesNoEntry(t *testing.T) {
	reader := &fakeSnapshotReader{fakeReader: fakeReader{
		project: &gitlab.Project{ID: 1, PathWithNamespace: "project/pathauto"},
		listed: []gitlab.MergeRequest{
			{IID: 12, State: "opened", TargetBranch: "2.0.x"},
			{IID: 13, State: "opened", TargetBranch: "2.0.x"},
		},
		mergeRefSHAs: map[int]string{12: "bbbb"},
	}}

	snapshot, failure := FetchModule(reader, fakeIssues{}, aModule(), time.Now())
	if failure != nil {
		t.Fatalf("fetch: %s", failure.Message)
	}

	if len(snapshot.MergeRefSHAs) != 1 || snapshot.MergeRefSHAs[12] != "bbbb" {
		t.Errorf("merge refs: %v", snapshot.MergeRefSHAs)
	}
}

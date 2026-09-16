package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/gitlab"
)

const snapshotFixtureDir = "../../testdata/snapshots"

type expectedSnapshot struct {
	Read             bool              `json:"read"`
	FetchedAt        string            `json:"fetched_at"`
	ProjectID        int               `json:"project_id"`
	ProjectPath      string            `json:"project_path"`
	MergeRequestIIDs []int             `json:"merge_request_iids"`
	MergedIIDs       []int             `json:"merged_iids"`
	IssueNids        []int             `json:"issue_nids"`
	IssuePatchCounts []int             `json:"issue_patch_counts"`
	ForkNids         map[string]int    `json:"fork_nids"`
	CoreConstraints  map[string]string `json:"core_constraints"`
	MergeRefSHAs     map[string]string `json:"merge_ref_shas"`
	AgeLabels        map[string]string `json:"age_labels"`
}

// The snapshot is a cache file that outlives whichever binary wrote it, so
// both implementations have to read each other's — and it is untrusted input
// by the same argument, which is why the malformed fixtures matter as much as
// the good one.
//
// Regenerate with: php snapshot_expect.php.
func TestSnapshotReadingMatchesPhp(t *testing.T) {
	expected := loadExpectedSnapshots(t)

	fixtures, err := filepath.Glob(filepath.Join(snapshotFixtureDir, "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	checked := 0
	for _, fixture := range fixtures {
		name := filepath.Base(fixture)
		if name == "expected.json" {
			continue
		}
		want, recorded := expected[name]
		if !recorded {
			t.Errorf("%s: no recorded answer — regenerate", name)

			continue
		}
		checked++

		raw, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		snapshot, read := SnapshotFromJSON(string(raw))
		if read != want.Read {
			t.Errorf("%s: read=%v, PHP says %v", name, read, want.Read)

			continue
		}
		if !read {
			continue
		}

		wantFetchedAt, err := time.Parse(time.RFC3339, want.FetchedAt)
		if err != nil {
			t.Fatalf("%s: recorded fetched_at is unparseable: %v", name, err)
		}
		if !snapshot.FetchedAt.Equal(wantFetchedAt) {
			t.Errorf("%s: fetched at %v, PHP read %v", name, snapshot.FetchedAt, wantFetchedAt)
		}

		project := snapshot.Project()
		if project.ID != want.ProjectID || project.PathWithNamespace != want.ProjectPath {
			t.Errorf("%s: project %d %q, PHP read %d %q",
				name, project.ID, project.PathWithNamespace, want.ProjectID, want.ProjectPath)
		}

		compareInts(t, name, "open merge requests", iids(snapshot.MergeRequests()), want.MergeRequestIIDs)
		compareInts(t, name, "merged merge requests", iids(snapshot.MergedMergeRequests()), want.MergedIIDs)

		issues := snapshot.PatchIssues()
		nids := []int{}
		counts := []int{}
		for _, issue := range issues {
			nids = append(nids, issue.Nid)
			counts = append(counts, issue.PatchCount())
		}
		compareInts(t, name, "issues", nids, want.IssueNids)
		compareInts(t, name, "patch counts", counts, want.IssuePatchCounts)

		compareIntMap(t, name, "fork nids", snapshot.ForkNids, want.ForkNids)
		compareStringMap(t, name, "merge ref shas", snapshot.MergeRefSHAs, want.MergeRefSHAs)

		if len(snapshot.CoreConstraints) != len(want.CoreConstraints) {
			t.Errorf("%s: %d core constraints, PHP read %d",
				name, len(snapshot.CoreConstraints), len(want.CoreConstraints))
		}
		for branch, constraint := range want.CoreConstraints {
			if snapshot.CoreConstraints[branch] != constraint {
				t.Errorf("%s: constraint for %q is %q, PHP read %q",
					name, branch, snapshot.CoreConstraints[branch], constraint)
			}
		}

		for label, want := range want.AgeLabels {
			offset := map[string]time.Duration{
				"just now": 30 * time.Second,
				"minutes":  45 * time.Minute,
				"hours":    5 * time.Hour,
				"days":     9 * 24 * time.Hour,
			}[label]
			if got := snapshot.AgeLabel(snapshot.FetchedAt.Add(offset)); got != want {
				t.Errorf("%s: age at %v is %q, PHP says %q", name, offset, got, want)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no fixtures were checked")
	}
}

// What one side writes the other must read. go-written.json is in the set
// snapshot_expect.php loads, so its being in the answers file at all is PHP
// proving it can read this.
func TestThisSnapshotRenderingIsWhatPhpReads(t *testing.T) {
	expected := loadExpectedSnapshots(t)

	want, present := expected["go-written.json"]
	if !present {
		t.Fatal("go-written.json is not in the answers — regenerate with php snapshot_expect.php")
	}
	if !want.Read {
		t.Fatal("PHP could not read the snapshot this implementation writes")
	}

	raw, err := os.ReadFile(filepath.Join(snapshotFixtureDir, "go-written.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	snapshot, read := SnapshotFromJSON(string(raw))
	if !read {
		t.Fatal("this implementation cannot read what it writes")
	}

	// And the values PHP read out of it are the ones that went in.
	compareInts(t, "go-written.json", "open merge requests", iids(snapshot.MergeRequests()), want.MergeRequestIIDs)
	compareInts(t, "go-written.json", "merged merge requests", iids(snapshot.MergedMergeRequests()), want.MergedIIDs)
	compareIntMap(t, "go-written.json", "fork nids", snapshot.ForkNids, want.ForkNids)
	compareStringMap(t, "go-written.json", "merge ref shas", snapshot.MergeRefSHAs, want.MergeRefSHAs)
	if len(snapshot.PatchIssues()) != len(want.IssueNids) {
		t.Errorf("issues: %d here, %d in PHP", len(snapshot.PatchIssues()), len(want.IssueNids))
	}
}

// The committed go-written.json is the file PHP was shown and read
// identically to its own. Pinning the writer against those exact bytes is what
// makes that verification mean anything later: without it, the writer can
// drift — a different timestamp format, a different key — and the fixture goes
// on passing because it is a fixture.
func TestTheWriterStillProducesTheFixturePhpVerified(t *testing.T) {
	rendered, err := fixtureSnapshot(t).ToJSON()
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	committed, err := os.ReadFile(filepath.Join(snapshotFixtureDir, "go-written.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if rendered+"\n" != string(committed) {
		t.Errorf("the writer no longer produces the verified fixture.\n--- committed ---\n%s\n--- now ---\n%s",
			committed, rendered)
	}
}

// fixtureSnapshot is the snapshot go-written.json was rendered from.
func fixtureSnapshot(t *testing.T) ModuleSnapshot {
	t.Helper()

	return ModuleSnapshot{
		FetchedAt:      time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC),
		ProjectData:    loadPayload(t, "../../testdata/project.json"),
		MRData:         []map[string]any{loadPayload(t, "../../testdata/mr.json")},
		PatchIssueData: []map[string]any{loadPayload(t, "../../testdata/issues/resolved-attachments.json")},
		MergedMRData:   []map[string]any{loadPayload(t, "../../testdata/merged-mr.json")},
		ForkNids:       map[int]int{501: 3603341, 502: 3597808},
		CoreConstraints: map[string]string{
			"2.0.x": "^10.2 || ^11 || ^12", "1.0.x": "^10",
		},
		MergeRefSHAs: map[int]string{12: "deadbeefcafe", 9: "abc1234"},
	}
}

func loadPayload(t *testing.T, path string) map[string]any {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	return data
}

// A half-shaped cache must read as "no cache" — the caller then refetches —
// rather than reach the models as something else.
func TestAHalfShapedCacheIsNoCache(t *testing.T) {
	for _, body := range []string{
		"",
		"   ",
		"null",
		`{"fetched_at":"2026-06-12T10:00:00+00:00"}`,
		`{"fetched_at":null,"project":{},"merge_requests":[]}`,
		`{"fetched_at":"2026-06-12T10:00:00+00:00","project":"nope","merge_requests":[]}`,
	} {
		if _, read := SnapshotFromJSON(body); read {
			t.Errorf("%q read as a usable snapshot", body)
		}
	}
}

func iids(mergeRequests []gitlab.MergeRequest) []int {
	out := []int{}
	for _, mr := range mergeRequests {
		out = append(out, mr.IID)
	}

	return out
}

func compareInts(t *testing.T, fixture, what string, got, want []int) {
	t.Helper()

	if want == nil {
		want = []int{}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: %s are %v, PHP read %v", fixture, what, got, want)
	}
}

func compareIntMap(t *testing.T, fixture, what string, got map[int]int, want map[string]int) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("%s: %d %s, PHP read %d", fixture, len(got), what, len(want))
	}
	for key, value := range want {
		id := atoi(t, key)
		if got[id] != value {
			t.Errorf("%s: %s[%d] is %d, PHP read %d", fixture, what, id, got[id], value)
		}
	}
}

func compareStringMap(t *testing.T, fixture, what string, got map[int]string, want map[string]string) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("%s: %d %s, PHP read %d", fixture, len(got), what, len(want))
	}
	for key, value := range want {
		id := atoi(t, key)
		if got[id] != value {
			t.Errorf("%s: %s[%d] is %q, PHP read %q", fixture, what, id, got[id], value)
		}
	}
}

func atoi(t *testing.T, value string) int {
	t.Helper()

	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("recorded key %q is not a number: %v", value, err)
	}

	return n
}

func loadExpectedSnapshots(t *testing.T) map[string]expectedSnapshot {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(snapshotFixtureDir, "expected.json"))
	if err != nil {
		t.Fatalf("answers: %v (regenerate with: php snapshot_expect.php)", err)
	}

	var expected map[string]expectedSnapshot
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatalf("answers: %v", err)
	}
	if len(expected) == 0 {
		t.Fatal("no answers recorded")
	}

	return expected
}

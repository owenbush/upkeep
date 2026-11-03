package drupalorgcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/gitlab"
)

var contentSync = gitlab.Project{
	ID:                12345,
	Path:              "content_sync",
	PathWithNamespace: "project/content_sync",
	WebURL:            "https://git.drupalcode.org/project/content_sync",
}

// fakeCLI installs a stub drupalorg that answers each argv prefix with a
// recorded fixture from testdata, records every argv it saw, and fails with
// GitLab's 404 wording for anything unrouted.
func fakeCLI(t *testing.T, routes map[string]string) (*MergeRequestReader, func() []string) {
	t.Helper()

	var body strings.Builder
	body.WriteString("echo \"$*\" >> \"$(dirname \"$0\")/argv\"\ncase \"$*\" in\n")
	for prefix, fixture := range routes {
		abs, err := filepath.Abs(filepath.Join("testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		body.WriteString("  \"" + prefix + " \"*) cat '" + abs + "' ;;\n")
	}
	body.WriteString("  *) echo 'Client error: `GET x` resulted in a `404 Not Found` response' >&2; exit 1 ;;\nesac\n")

	path := stub(t, "0.10.3", body.String())

	argv := func() []string {
		data, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "argv"))

		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}

	return NewMergeRequestReader(New(nil, 0)), argv
}

func TestOpenMergeRequestsMapCLIRowsOntoTheModel(t *testing.T) {
	reader, argv := fakeCLI(t, map[string]string{
		"mr:list project/content_sync --state=opened": "mr-list-opened.json",
	})

	list, failure := reader.OpenMergeRequests(contentSync)
	if failure != nil {
		t.Fatalf("failure: %v", failure)
	}
	if got := argv(); len(got) != 1 || got[0] != "mr:list project/content_sync --state=opened --format=json --no-interaction" {
		t.Fatalf("argv = %q", got)
	}
	if len(list) != 3 {
		t.Fatalf("got %d merge requests", len(list))
	}

	bot := list[0]
	want := gitlab.MergeRequest{
		IID:            22,
		Title:          "Automated Project Update Bot fixes",
		State:          "opened",
		AuthorUsername: "Project-Update-Bot",
		SourceBranch:   "project-update-bot-only",
		TargetBranch:   "5.0.x",
		WebURL:         "https://git.drupalcode.org/project/content_sync/-/merge_requests/22",
		UpdatedAt:      "2026-10-23T23:06:50.425Z",
	}
	if bot.HeadPipeline != nil || bot != want {
		t.Fatalf("mapped\n %+v\nwant\n %+v", bot, want)
	}
	// What the list cannot say stays unknown, never "no changes".
	if bot.CarriesChanges() != gitlab.Unknown {
		t.Fatalf("CarriesChanges = %v, want Unknown", bot.CarriesChanges())
	}
}

func TestMergedMergeRequestsAreNewestUpdatedFirstAndBounded(t *testing.T) {
	reader, argv := fakeCLI(t, map[string]string{
		"mr:list project/content_sync --state=merged": "mr-list-merged.json",
	})

	// The CLI returns 14, 10, 8 (creation order); by updated_at it is 14, 8, 10.
	list, failure := reader.MergedMergeRequests(contentSync, 2)
	if failure != nil {
		t.Fatalf("failure: %v", failure)
	}
	if len(list) != 2 || list[0].IID != 14 || list[1].IID != 8 {
		t.Fatalf("got %+v", list)
	}
	if list[0].MergedAt != "" {
		t.Fatalf("MergedAt = %q; the CLI does not provide it", list[0].MergedAt)
	}
	if got := argv(); got[0] != "mr:list project/content_sync --state=merged --format=json --no-interaction" {
		t.Fatalf("argv = %q", got)
	}
}

func TestMergeRequestFindsTheIIDAndAttachesItsPipeline(t *testing.T) {
	reader, argv := fakeCLI(t, map[string]string{
		"mr:list project/content_sync --state=all": "mr-list-all.json",
		"mr:status project/content_sync!22":        "mr-status-passed.json",
		"mr:status project/content_sync!15":        "mr-status-none.json",
	})

	mr, failure := reader.MergeRequest(contentSync, 22)
	if failure != nil {
		t.Fatalf("failure: %v", failure)
	}
	if mr.IID != 22 || mr.AuthorUsername != "Project-Update-Bot" {
		t.Fatalf("got %+v", mr)
	}
	want := gitlab.Pipeline{
		ID:        1013844,
		Status:    gitlab.StatusSuccess,
		RawStatus: "success",
		WebURL:    "https://git.drupalcode.org/issue/content_sync-3632781/-/pipelines/1013844",
	}
	if mr.HeadPipeline == nil || *mr.HeadPipeline != want {
		t.Fatalf("pipeline = %+v, want %+v", mr.HeadPipeline, want)
	}
	if got := argv(); len(got) != 2 || got[1] != "mr:status project/content_sync!22 --format=json --no-interaction" {
		t.Fatalf("argv = %q", got)
	}

	// A "Draft: " title is a draft even though the CLI drops the flag.
	draft, failure := reader.MergeRequest(contentSync, 15)
	if failure != nil {
		t.Fatalf("failure: %v", failure)
	}
	if !draft.IsDraft() || draft.State != "closed" || draft.HeadPipeline != nil {
		t.Fatalf("got %+v", draft)
	}
}

func TestAMergeRequestTheListDoesNotHoldIsNotFound(t *testing.T) {
	reader, _ := fakeCLI(t, map[string]string{
		"mr:list project/content_sync --state=all": "mr-list-all.json",
	})

	_, failure := reader.MergeRequest(contentSync, 999)
	if failure == nil || failure.Kind != gitlab.NotFound {
		t.Fatalf("failure = %+v, want NotFound", failure)
	}
	if failure.BrowserURL != contentSync.WebURL+"/-/merge_requests/999" {
		t.Fatalf("BrowserURL = %q", failure.BrowserURL)
	}

	// Listed, but its pipeline cannot be read: the merge request is not
	// returned without one, as the native single fetch would not be.
	if mr, failure := reader.MergeRequest(contentSync, 22); failure == nil || mr != nil {
		t.Fatalf("mr = %+v, failure = %+v; want the mr:status failure", mr, failure)
	}
}

func TestHeadPipelineMapsCLIStatusesBack(t *testing.T) {
	reader, _ := fakeCLI(t, map[string]string{
		"mr:list project/content_sync --state=all": "mr-list-all.json",
		"mr:status project/content_sync!14":        "mr-status-canceled.json",
		"mr:status project/content_sync!5":         "mr-status-none.json",
	})

	pipeline, failure := reader.HeadPipeline(contentSync, 14)
	if failure != nil || pipeline == nil || pipeline.Status != gitlab.StatusCanceled || pipeline.ID != 291956 {
		t.Fatalf("pipeline = %+v, failure = %v", pipeline, failure)
	}

	none, failure := reader.HeadPipeline(contentSync, 5)
	if failure != nil || none != nil {
		t.Fatalf("pipeline = %+v, failure = %v; want nil, nil", none, failure)
	}

	cases := map[string]gitlab.PipelineStatus{
		"passed":   gitlab.StatusSuccess,
		"failed":   gitlab.StatusFailed,
		"running":  gitlab.StatusRunning,
		"pending":  gitlab.StatusPending,
		"canceled": gitlab.StatusCanceled,
		"weird":    gitlab.StatusUnknown,
	}
	for raw, want := range cases {
		got := pipelineFromStatus(mrStatus{PipelineID: new(1), Status: raw})
		if got == nil || got.Status != want {
			t.Errorf("%s -> %+v, want %s", raw, got, want)
		}
	}
	if got := pipelineFromStatus(mrStatus{PipelineID: new(1), Status: "weird"}); got.RawStatus != "weird" {
		t.Errorf("an unrecognised status keeps its raw value, got %q", got.RawStatus)
	}
}

func TestFailuresCarryTheBrowserURLAndNoBareNidIsSent(t *testing.T) {
	reader, argv := fakeCLI(t, nil)

	_, failure := reader.OpenMergeRequests(contentSync)
	if failure == nil || failure.Kind != gitlab.NotFound || failure.BrowserURL != contentSync.WebURL+"/-/merge_requests" {
		t.Fatalf("failure = %+v", failure)
	}

	// Without a namespaced path the reader refuses rather than guess.
	_, failure = reader.OpenMergeRequests(gitlab.Project{ID: 1})
	if failure == nil || failure.Kind != gitlab.ResourceMissing {
		t.Fatalf("failure = %+v, want ResourceMissing", failure)
	}
	if got := argv(); len(got) != 1 {
		t.Fatalf("the CLI was run for a project with no path: %q", got)
	}

	if _, failure := reader.MergeRequest(contentSync, 5); failure == nil || failure.Kind != gitlab.NotFound {
		t.Fatalf("detail failure = %+v", failure)
	}
	if _, failure := reader.MergedMergeRequests(contentSync, 5); failure == nil || failure.Kind != gitlab.NotFound {
		t.Fatalf("merged failure = %+v", failure)
	}
	if _, failure := reader.HeadPipeline(gitlab.Project{ID: 1}, 5); failure == nil || failure.Kind != gitlab.ResourceMissing {
		t.Fatalf("pipeline failure = %+v, want ResourceMissing", failure)
	}

	// A bare machine name is namespaced, never passed as-is.
	path, failure := projectPath(gitlab.Project{Path: "content_sync"})
	if failure != nil || path != "project/content_sync" {
		t.Fatalf("path = %q, failure = %v", path, failure)
	}
}

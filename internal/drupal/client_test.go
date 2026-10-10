package drupal

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A stand-in for api-d7, including the two shapes that each cost an extra
// request: only project nodes carry field_project_machine_name, and every
// attachment is a bare reference with no name.
type fakeAPI struct {
	mu        sync.Mutex
	requests  []string
	inFlight  atomic.Int32
	peak      atomic.Int32
	fileDelay time.Duration
	fileFails map[int]int // fid -> status to answer with
	throttle  map[string]int
}

func (f *fakeAPI) serve(t *testing.T) *Client {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.RequestURI())
		f.mu.Unlock()

		if status, ok := f.throttle[r.URL.Path]; ok && status != 0 {
			delete(f.throttle, r.URL.Path)
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}

		switch {
		case strings.HasPrefix(r.URL.Path, "/api-d7/file/"):
			f.serveFile(w, r)
		case r.URL.Query().Get("field_project_machine_name") != "":
			fmt.Fprint(w, `{"list":[{"nid":3060,"type":"project_module"}]}`)
		case r.URL.Query().Get("type") == "project_issue":
			f.serveListing(w, r)
		default:
			f.serveNode(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.Client(), server.URL+"/api-d7")
	client.sleep = func(time.Duration) {} // no real waiting in tests

	return client
}

func (f *fakeAPI) serveFile(w http.ResponseWriter, r *http.Request) {
	current := f.inFlight.Add(1)
	for {
		peak := f.peak.Load()
		if current <= peak || f.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	defer f.inFlight.Add(-1)

	if f.fileDelay > 0 {
		time.Sleep(f.fileDelay)
	}

	var fid int
	fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/api-d7/file/"), "%d.json", &fid)
	if status, ok := f.fileFails[fid]; ok {
		w.WriteHeader(status)

		return
	}

	fmt.Fprintf(w, `{"name":"3597808-%d.patch","url":"https://x.test/%d.patch","filesize":120,
		"timestamp":1700000000,"owner":{"id":77}}`, fid, fid)
}

func (f *fakeAPI) serveNode(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(w, `{"nid":3597808,"title":"Fix the widget","field_issue_status":"8",
		"field_issue_priority":"300","field_issue_version":"2.0.x-dev",
		"field_project":{"machine_name":"widget"},
		"field_issue_files":[{"file":{"id":11}},{"file":{"id":12}}]}`)
}

func (f *fakeAPI) serveListing(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(w, `{"list":[{"nid":1,"title":"One","field_issue_status":"8",
		"field_issue_files":[{"file":{"id":21}}]},
		{"nid":2,"title":"Two","field_issue_status":"8","field_issue_files":[]}]}`)
}

func TestAnIssueCarriesItsAttachmentsAfterTheyAreDereferenced(t *testing.T) {
	api := &fakeAPI{}
	client := api.serve(t)

	issue, ok := client.Issue(3597808)
	if !ok {
		t.Fatalf("issue not fetched; warnings: %v", client.Warnings())
	}

	if issue.Title != "Fix the widget" || issue.Status != StatusNeedsReview {
		t.Errorf("issue = %+v", issue)
	}
	// Without the per-file lookup every issue has zero attachments, which is
	// the bug this shape exists to avoid.
	if issue.PatchCount() != 2 {
		t.Errorf("PatchCount = %d, want 2", issue.PatchCount())
	}
	if latest, _ := issue.LatestPatch(); latest.OwnerUID != 77 {
		t.Errorf("owner = %d, want the uid that posted it", latest.OwnerUID)
	}
	if issue.Project != "widget" {
		t.Errorf("project = %q", issue.Project)
	}
}

func TestAnIssueIsFetchedOnceAndRemembered(t *testing.T) {
	api := &fakeAPI{}
	client := api.serve(t)

	client.Issue(3597808)
	before := len(api.requests)
	client.Issue(3597808)

	if len(api.requests) != before {
		t.Errorf("the second read cost %d more requests", len(api.requests)-before)
	}
}

// Serialised, a busy module's attachment lookups took ~52s against a cold
// cache. Bounded rather than unbounded because api-d7 publishes no quota.
func TestAttachmentLookupsRunConcurrentlyButBounded(t *testing.T) {
	api := &fakeAPI{fileDelay: 20 * time.Millisecond}
	client := api.serve(t)

	var files []any
	for id := 100; id < 130; id++ {
		files = append(files, map[string]any{"file": map[string]any{"id": float64(id)}})
	}
	client.prefetchAttachments([]map[string]any{{"field_issue_files": files}})

	if peak := api.peak.Load(); peak < 2 {
		t.Errorf("peak concurrency %d — the lookups were serialised", peak)
	}
	if peak := api.peak.Load(); peak > MaxConcurrent {
		t.Errorf("peak concurrency %d exceeds the %d limit", peak, MaxConcurrent)
	}
}

// Returning less data is indistinguishable from there being less data, so a
// dropped attachment is always said out loud.
func TestADroppedAttachmentIsWarnedAboutRatherThanSilentlyMissing(t *testing.T) {
	api := &fakeAPI{fileFails: map[int]int{12: http.StatusNotFound}}
	client := api.serve(t)

	issue, ok := client.Issue(3597808)
	if !ok {
		t.Fatal("issue not fetched")
	}

	if issue.PatchCount() != 1 {
		t.Errorf("PatchCount = %d, want the one that resolved", issue.PatchCount())
	}
	warnings := strings.Join(client.Warnings(), "\n")
	if !strings.Contains(warnings, "attachment 12") {
		t.Errorf("warnings = %q, want the missing attachment named", warnings)
	}
}

// A machine name filters nothing on an issue listing, so the project's node id
// is resolved once and the listing filtered by that.
func TestTheProjectNidIsResolvedOnceAndUsedToFilterTheListing(t *testing.T) {
	api := &fakeAPI{}
	client := api.serve(t)

	issues := client.ProjectIssues("widget", []IssueStatus{StatusNeedsReview})

	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2; warnings: %v", len(issues), client.Warnings())
	}
	var nidLookups, listings int
	for _, request := range api.requests {
		switch {
		case strings.Contains(request, "field_project_machine_name"):
			nidLookups++
		case strings.Contains(request, "field_project=3060"):
			listings++
		}
	}
	if nidLookups != 1 {
		t.Errorf("resolved the project nid %d times, want once", nidLookups)
	}
	if listings == 0 {
		t.Error("the listing was not filtered by the project nid")
	}
}

// A CLI that appears to hang is worse than one that says it was throttled.
func TestA429IsRetriedOnceAndThenReported(t *testing.T) {
	api := &fakeAPI{throttle: map[string]int{"/api-d7/node/3597808.json": http.StatusTooManyRequests}}
	client := api.serve(t)

	if _, ok := client.Issue(3597808); !ok {
		t.Fatalf("the retry did not succeed; warnings: %v", client.Warnings())
	}

	var attempts int
	for _, request := range api.requests {
		if strings.Contains(request, "node/3597808.json") {
			attempts++
		}
	}
	if attempts != 2 {
		t.Errorf("made %d attempts, want the original and one retry", attempts)
	}
}

func TestARetryAfterLongerThanWeWillWaitIsReportedNotSleptThrough(t *testing.T) {
	if _, ok := retryAfter("600"); ok {
		t.Error("a ten-minute wait was accepted")
	}
	if wait, ok := retryAfter("2"); !ok || wait != 2*time.Second {
		t.Errorf("wait = %v ok = %v", wait, ok)
	}
	if wait, ok := retryAfter("nonsense"); !ok || wait != time.Second {
		t.Errorf("an unreadable header should wait the default once: %v %v", wait, ok)
	}
}

func TestAnUnreachableEndpointIsAWarningNotACrash(t *testing.T) {
	client := NewClient(&http.Client{Timeout: time.Second}, "http://127.0.0.1:1/api-d7")

	if _, ok := client.Issue(1); ok {
		t.Error("an unreachable endpoint reported success")
	}
	if len(client.Warnings()) == 0 {
		t.Error("nothing was warned about")
	}
}

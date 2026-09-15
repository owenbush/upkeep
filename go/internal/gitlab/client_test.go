package gitlab

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// reply is one canned response.
type reply struct {
	status  int
	body    string
	headers map[string]string
}

// recorder is the HTTP seam: it records every request that leaves the client
// and answers from a queue, so "how many requests did that cost" is testable.
type recorder struct {
	replies []reply
	// fallback answers once the queue is empty, for tests about pagination
	// where the count matters more than the sequence.
	fallback *reply
	requests []*http.Request
	err      error
}

func (r *recorder) Do(request *http.Request) (*http.Response, error) {
	r.requests = append(r.requests, request)
	if r.err != nil {
		return nil, r.err
	}

	var next reply
	switch {
	case len(r.replies) > 0:
		next, r.replies = r.replies[0], r.replies[1:]
	case r.fallback != nil:
		next = *r.fallback
	default:
		return nil, fmt.Errorf("unexpected request %d: %s", len(r.requests), request.URL)
	}

	response := &http.Response{
		StatusCode: next.status,
		Body:       io.NopCloser(strings.NewReader(next.body)),
		Header:     http.Header{},
	}
	for name, value := range next.headers {
		response.Header.Set(name, value)
	}

	return response, nil
}

func (r *recorder) urls() []string {
	seen := make([]string, 0, len(r.requests))
	for _, request := range r.requests {
		seen = append(seen, request.URL.String())
	}

	return seen
}

func ok(body string) reply { return reply{status: 200, body: body} }

func clientWith(token string, replies ...reply) (*Client, *recorder) {
	transport := &recorder{replies: replies}

	return NewClient(transport, token, "https://api.test/api/v4", "https://web.test"), transport
}

const pathautoJSON = `{"id":11,"path":"pathauto","path_with_namespace":"project/pathauto",
	"name":"Pathauto","web_url":"https://web.test/project/pathauto"}`

func pathauto(t *testing.T) Project {
	t.Helper()

	return decodeProject(t, pathautoJSON)
}

// GitLab addresses a project by its namespaced path with the slash encoded; a
// bare slash addresses a path that does not exist.
func TestTheProjectPathTravelsAsOneEncodedSegment(t *testing.T) {
	client, transport := clientWith("", ok(pathautoJSON))

	project, failure := client.Project("pathauto")
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if project.ID != 11 {
		t.Errorf("id %d", project.ID)
	}

	want := "https://api.test/api/v4/projects/project%2Fpathauto"
	if got := transport.urls()[0]; got != want {
		t.Errorf("requested %q,\n    want %q", got, want)
	}
}

// A full namespaced path is taken as given rather than prefixed again.
func TestAFullPathIsNotPrefixed(t *testing.T) {
	client, transport := clientWith("", ok(pathautoJSON))

	if _, failure := client.Project("project/pathauto"); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if strings.Contains(transport.urls()[0], "project%2Fproject") {
		t.Errorf("prefixed a path that already had a namespace: %s", transport.urls()[0])
	}
}

// Reading needs no credential, and an empty PRIVATE-TOKEN is a *bad* one:
// GitLab answers 401 to it where no header at all is an ordinary public read.
func TestAnAnonymousClientSendsNoTokenHeaderAtAll(t *testing.T) {
	client, transport := clientWith("", ok(pathautoJSON))

	if _, failure := client.Project("pathauto"); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}

	if _, present := transport.requests[0].Header["Private-Token"]; present {
		t.Error("an anonymous read sent a PRIVATE-TOKEN header")
	}
}

func TestATokenTravelsInTheHeader(t *testing.T) {
	client, transport := clientWith("glpat-secret", ok(pathautoJSON))

	if _, failure := client.Project("pathauto"); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}

	if got := transport.requests[0].Header.Get("PRIVATE-TOKEN"); got != "glpat-secret" {
		t.Errorf("header %q", got)
	}
}

// Writes refuse *in the client*, before any request, so no command can reach
// one anonymously by forgetting to check.
func TestWritesRefuseAnonymouslyBeforeAnyRequest(t *testing.T) {
	project := pathauto(t)

	for _, tc := range []struct {
		name string
		call func(*Client) *Failure
	}{
		{"merge", func(c *Client) *Failure { _, f := c.Merge(project, 1, ""); return f }},
		{"note", func(c *Client) *Failure { return c.PostNote(project, 1, "hello") }},
		{"open a merge request", func(c *Client) *Failure {
			_, f := c.CreateMergeRequest(project, "src", "dst", "t", "", nil)

			return f
		}},
	} {
		client, transport := clientWith("")

		failure := tc.call(client)
		if failure == nil {
			t.Errorf("%s: went ahead anonymously", tc.name)

			continue
		}
		if failure.Kind != Unauthorized {
			t.Errorf("%s: kind %v, want unauthorized", tc.name, failure.Kind)
		}
		if len(transport.requests) != 0 {
			t.Errorf("%s: made %d request(s) before refusing", tc.name, len(transport.requests))
		}
		if strings.Contains(failure.Message, "glpat") {
			t.Errorf("%s: message quotes token material", tc.name)
		}
	}
}

// Rate-limit friendliness: the same resource is never fetched twice in one run.
func TestAGetIsMemoizedForTheRunAndFreshClearsIt(t *testing.T) {
	client, transport := clientWith("", ok(pathautoJSON), ok(pathautoJSON))

	for range 3 {
		if _, failure := client.Project("pathauto"); failure != nil {
			t.Fatalf("unexpected failure: %v", failure)
		}
	}
	if len(transport.requests) != 1 {
		t.Fatalf("made %d requests, want 1", len(transport.requests))
	}

	// The pre-merge freshness re-check must observe the world as it is NOW.
	if _, failure := client.Fresh().Project("pathauto"); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if len(transport.requests) != 2 {
		t.Errorf("a fresh copy reused the cache (%d requests)", len(transport.requests))
	}

	// And the original's cache is left untouched.
	if _, failure := client.Project("pathauto"); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if len(transport.requests) != 2 {
		t.Errorf("Fresh() emptied the original's cache (%d requests)", len(transport.requests))
	}
}

// One blip must not turn into a permanent verdict for that resource.
func TestATransientFailureIsNotMemoized(t *testing.T) {
	client, transport := clientWith("",
		reply{status: 429, headers: map[string]string{"Retry-After": "7"}},
		ok(pathautoJSON),
	)

	_, failure := client.Project("pathauto")
	if failure == nil || failure.Kind != RateLimited {
		t.Fatalf("first call: %v", failure)
	}
	if failure.RetryAfter != 7 {
		t.Errorf("Retry-After %d, want 7", failure.RetryAfter)
	}
	if !strings.Contains(failure.Message, "retry in 7 seconds") {
		t.Errorf("message does not carry the wait: %q", failure.Message)
	}

	project, failure := client.Project("pathauto")
	if failure != nil {
		t.Fatalf("the rate limit was cached and poisoned the resource: %v", failure)
	}
	if project.ID != 11 {
		t.Error("retry returned the wrong project")
	}
	if len(transport.requests) != 2 {
		t.Errorf("made %d requests, want 2", len(transport.requests))
	}
}

// A stable failure IS memoized — a 404 stays a 404 for the run.
func TestAStableFailureIsMemoized(t *testing.T) {
	client, transport := clientWith("", reply{status: 404, body: `{}`}, ok(pathautoJSON))

	for range 2 {
		if _, failure := client.Project("nothing_here"); failure == nil || failure.Kind != NotFound {
			t.Fatalf("expected a 404, got %v", failure)
		}
	}
	if len(transport.requests) != 1 {
		t.Errorf("made %d requests, want 1", len(transport.requests))
	}
}

// A rejected credential is 401 and must not be told as "use the browser",
// which is what a 403 on this block-by-default instance means.
func TestTheStatusesMapToDistinctConditions(t *testing.T) {
	for _, tc := range []struct {
		status int
		kind   FailureKind
		says   string
	}{
		{401, Unauthorized, "rejected the credential"},
		{403, EndpointClosed, "Use the browser instead"},
		{404, NotFound, "not found"},
		{400, RequestRejected, "rejected the request"},
		{500, RequestRejected, "rejected the request"},
	} {
		client, _ := clientWith("t", reply{status: tc.status, body: `{}`})

		_, failure := client.Project("pathauto")
		if failure == nil {
			t.Errorf("HTTP %d: no failure", tc.status)

			continue
		}
		if failure.Kind != tc.kind {
			t.Errorf("HTTP %d: kind %v, want %v", tc.status, failure.Kind, tc.kind)
		}
		if !strings.Contains(failure.Message, tc.says) {
			t.Errorf("HTTP %d: message %q does not say %q", tc.status, failure.Message, tc.says)
		}
		if failure.Status != tc.status {
			t.Errorf("HTTP %d: carried status %d", tc.status, failure.Status)
		}
	}
}

// GitLab returns either a string or a list of complaints; only the scalar
// entries are quotable, and nothing from the request headers is ever quoted.
func TestTheServersOwnComplaintIsQuoted(t *testing.T) {
	client, _ := clientWith("glpat-secret", reply{
		status: 400,
		body:   `{"message":["branch is missing","title can't be blank",{"nested":1}]}`,
	})

	_, failure := client.Project("pathauto")
	if failure == nil {
		t.Fatal("no failure")
	}
	if !strings.Contains(failure.Message, "branch is missing; title can't be blank") {
		t.Errorf("message %q", failure.Message)
	}
	if strings.Contains(failure.Message, "nested") {
		t.Error("a nested structure was rendered into the message")
	}
	if strings.Contains(failure.Message, "glpat-secret") {
		t.Fatal("the token reached an error message")
	}
}

// A gateway interstitial served with HTTP 200 is not a success.
func TestAnUnusableBodyIsMalformedRatherThanDecodedAsNothing(t *testing.T) {
	client, _ := clientWith("", ok(`<html>Checking your browser…</html>`))

	_, failure := client.Project("pathauto")
	if failure == nil || failure.Kind != MalformedResponse {
		t.Fatalf("got %v, want a malformed response", failure)
	}
	if !failure.Transient() {
		t.Error("an interstitial should be worth another attempt")
	}
}

// A bare JSON scalar decodes fine and is still not a resource.
func TestAScalarBodyIsMalformed(t *testing.T) {
	client, _ := clientWith("", ok(`"just a string"`))

	_, failure := client.Project("pathauto")
	if failure == nil || failure.Kind != MalformedResponse {
		t.Fatalf("got %v, want a malformed response", failure)
	}
	if !strings.Contains(failure.Message, "got string") {
		t.Errorf("message %q does not name what arrived", failure.Message)
	}
}

// No response at all is a transport error, and it carries no status because
// there was none to take.
func TestNoResponseAtAllIsATransportError(t *testing.T) {
	transport := &recorder{err: fmt.Errorf("dial tcp: connection refused")}
	client := NewClient(transport, "", "https://api.test/api/v4", "https://web.test")

	_, failure := client.Project("pathauto")
	if failure == nil || failure.Kind != TransportError {
		t.Fatalf("got %v, want a transport error", failure)
	}
	if failure.Status != 0 {
		t.Errorf("carried status %d, want none", failure.Status)
	}
	if !failure.Transient() {
		t.Error("a network condition should be worth another attempt")
	}
}

// The shape that used to make pagination loop forever.
func TestAnObjectWhereAListWasPromisedIsMalformed(t *testing.T) {
	client, _ := clientWith("", ok(`{"message":"401 Unauthorized"}`))

	_, failure := client.BranchNames(pathauto(t))
	if failure == nil || failure.Kind != MalformedResponse {
		t.Fatalf("got %v, want a malformed response", failure)
	}
	if !strings.Contains(failure.Message, "Expected a list of branches") {
		t.Errorf("message %q", failure.Message)
	}
}

// A scalar entry used to reach a model and fatal.
func TestAListEntryThatIsNotAnObjectIsMalformed(t *testing.T) {
	client, _ := clientWith("", ok(`[{"name":"2.0.x"}, "1.0.x"]`))

	_, failure := client.BranchNames(pathauto(t))
	if failure == nil || failure.Kind != MalformedResponse {
		t.Fatalf("got %v, want a malformed response", failure)
	}
	if !strings.Contains(failure.Message, "entry 1 is string") {
		t.Errorf("message %q does not name the offending entry", failure.Message)
	}
}

// A server that never stops handing back full pages must end as a protocol
// fault, not as an unbounded request loop.
func TestMembershipPaginationIsBounded(t *testing.T) {
	full := strings.Repeat(`{"id":1},`, 99) + `{"id":1}`
	transport := &recorder{fallback: &reply{status: 200, body: "[" + full + "]"}}
	client := NewClient(transport, "t", "https://api.test/api/v4", "https://web.test")

	_, failure := client.MembershipProjects()
	if failure == nil || failure.Kind != MalformedResponse {
		t.Fatalf("got %v, want a malformed response", failure)
	}
	if len(transport.requests) != MaxPages {
		t.Errorf("made %d requests, want the %d-page guard", len(transport.requests), MaxPages)
	}
}

func TestMembershipPaginationStopsOnAnEmptyPage(t *testing.T) {
	client, transport := clientWith("t", ok(`[{"id":1},{"id":2}]`), ok(`[]`))

	projects, failure := client.MembershipProjects()
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if len(projects) != 2 {
		t.Errorf("got %d projects, want 2", len(projects))
	}
	if len(transport.requests) != 2 {
		t.Errorf("made %d requests, want 2", len(transport.requests))
	}
}

// An unstarted issue has no fork, and that is a state, not an error.
func TestAMissingIssueForkIsNeitherAProjectNorAFailure(t *testing.T) {
	client, _ := clientWith("", reply{status: 404, body: `{}`})

	fork, failure := client.IssueFork(pathauto(t), 3603341)
	if failure != nil {
		t.Fatalf("a missing fork was reported as a failure: %v", failure)
	}
	if fork != nil {
		t.Error("invented a fork")
	}
}

// Anything that is not a 404 still is one.
func TestAnIssueForkLookupStillReportsRealFailures(t *testing.T) {
	client, _ := clientWith("t", reply{status: 403, body: `{}`})

	_, failure := client.IssueFork(pathauto(t), 3603341)
	if failure == nil || failure.Kind != EndpointClosed {
		t.Fatalf("got %v, want the 403", failure)
	}
}

func TestIssueForkNidsMapsSourceProjectsToIssues(t *testing.T) {
	client, _ := clientWith("", ok(`[
		{"id":501,"path_with_namespace":"issue/pathauto-3603341"},
		{"id":502,"path_with_namespace":"project/somebody-elses-fork"},
		{"id":503,"path_with_namespace":"issue/pathauto-3596502"}
	]`))

	nids, failure := client.IssueForkNids(pathauto(t))
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	want := map[int]int{501: 3603341, 503: 3596502}
	if len(nids) != len(want) {
		t.Fatalf("got %v, want %v", nids, want)
	}
	for id, nid := range want {
		if nids[id] != nid {
			t.Errorf("fork %d mapped to %d, want %d", id, nids[id], nid)
		}
	}
}

// One request per project, not one per merge request — a short page ends it.
func TestForkPaginationStopsOnAShortPage(t *testing.T) {
	client, transport := clientWith("", ok(`[{"id":1,"path_with_namespace":"issue/pathauto-1"}]`))

	if _, failure := client.IssueForkNids(pathauto(t)); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if len(transport.requests) != 1 {
		t.Errorf("made %d requests for one short page", len(transport.requests))
	}
}

// The merge ref is absent for a merge request that conflicts with its target,
// which is a fallback condition rather than a failure.
func TestNoMergeRefReadsAsEmptyRatherThanFailing(t *testing.T) {
	client, _ := clientWith("", reply{status: 404, body: `{}`})

	if got := client.MergeRefSHA(pathauto(t), 12); got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}

func TestTheMergeRefShaIsRead(t *testing.T) {
	client, _ := clientWith("", ok(`{"commit_id":"deadbeef"}`))

	if got := client.MergeRefSHA(pathauto(t), 12); got != "deadbeef" {
		t.Errorf("got %q", got)
	}
}

// The single-merge-request endpoint is the only one that can settle emptiness,
// and asking for the pipeline must not cost a second request.
func TestTheMergeRequestAndItsPipelineCostOneRequest(t *testing.T) {
	client, transport := clientWith("", ok(`{"iid":12,"title":"t","state":"opened",
		"head_pipeline":{"id":9,"status":"success","web_url":"u"},
		"diff_refs":{"base_sha":"aaa","head_sha":"aaa"}}`))

	project := pathauto(t)
	mr, failure := client.MergeRequest(project, 12)
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if mr.CarriesChanges() != No {
		t.Error("an empty merge request was not settled by the single endpoint")
	}

	pipeline, failure := client.HeadPipeline(project, 12)
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if pipeline == nil || !pipeline.Status.IsGreen() {
		t.Errorf("pipeline %+v", pipeline)
	}
	if len(transport.requests) != 1 {
		t.Errorf("made %d requests, want 1", len(transport.requests))
	}
}

// Branch names on drupal.org are the issue nid and a slug, so the same name
// exists on every fork of an issue.
func TestABranchLookupIsNarrowedToYourOwnFork(t *testing.T) {
	body := `[{"iid":1,"source_project_id":999},{"iid":2,"source_project_id":501}]`
	mine := Project{ID: 501}

	client, _ := clientWith("t", ok(body))
	mr, failure := client.MergeRequestForBranch(pathauto(t), "3603341-fix", &mine)
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if mr == nil || mr.IID != 2 {
		t.Errorf("got %+v, want !2 — somebody else's fork was answered as mine", mr)
	}

	// Without narrowing, the first one wins, as before.
	client, _ = clientWith("t", ok(body))
	mr, failure = client.MergeRequestForBranch(pathauto(t), "3603341-fix", nil)
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if mr == nil || mr.IID != 1 {
		t.Errorf("got %+v, want !1", mr)
	}
}

func TestNoOpenMergeRequestForABranchIsNeitherResultNorFailure(t *testing.T) {
	client, _ := clientWith("t", ok(`[]`))

	mr, failure := client.MergeRequestForBranch(pathauto(t), "3603341-fix", nil)
	if failure != nil || mr != nil {
		t.Errorf("got (%v, %v), want nothing at all", mr, failure)
	}
}

// The branch lives on an issue fork and the merge request goes into the
// canonical project, which GitLab wants POSTed to the *source*.
func TestOpeningAcrossProjectsNamesTheTarget(t *testing.T) {
	client, transport := clientWith("t", ok(`{"iid":7,"state":"opened"}`))
	fork := Project{ID: 501, WebURL: "https://web.test/issue/pathauto-1"}
	canonical := pathauto(t)

	mr, failure := client.CreateMergeRequest(fork, "1-fix", "2.0.x", "Issue #1 by x: Fix", "body", &canonical)
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if mr.IID != 7 {
		t.Errorf("iid %d", mr.IID)
	}

	request := transport.requests[0]
	if !strings.Contains(request.URL.String(), "/projects/501/merge_requests") {
		t.Errorf("posted to %q, want the source project", request.URL)
	}
	body := bodyOf(t, transport, 0)
	if !strings.Contains(body, `"target_project_id":11`) {
		t.Errorf("body %q does not name the target project", body)
	}
	// Nothing here deletes what it did not create.
	if !strings.Contains(body, `"remove_source_branch":false`) {
		t.Errorf("body %q does not keep the contributor's branch", body)
	}
}

// Same project both sides: no target_project_id, which GitLab rejects.
func TestOpeningWithinOneProjectNamesNoTarget(t *testing.T) {
	client, transport := clientWith("t", ok(`{"iid":7}`))
	project := pathauto(t)

	if _, failure := client.CreateMergeRequest(project, "a", "b", "t", "", &project); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if body := bodyOf(t, transport, 0); strings.Contains(body, "target_project_id") {
		t.Errorf("body %q names a target project it is already in", body)
	}
}

// The sha guard is what refuses a merge if the branch moved since the human
// reviewed it.
func TestMergePassesTheExpectedHeadAsAGuard(t *testing.T) {
	client, transport := clientWith("t", ok(`{"iid":12,"state":"merged"}`))

	if _, failure := client.Merge(pathauto(t), 12, "abc123"); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if transport.requests[0].Method != http.MethodPut {
		t.Errorf("method %s", transport.requests[0].Method)
	}
	if body := bodyOf(t, transport, 0); !strings.Contains(body, `"sha":"abc123"`) {
		t.Errorf("body %q carries no guard", body)
	}
}

func TestMergeWithoutAGuardSendsAnEmptyObject(t *testing.T) {
	client, transport := clientWith("t", ok(`{"iid":12,"state":"merged"}`))

	if _, failure := client.Merge(pathauto(t), 12, ""); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if body := bodyOf(t, transport, 0); body != "{}" {
		t.Errorf("body %q, want an empty object", body)
	}
}

// A 403 here is the documented degraded path, not a bug.
func TestAClosedMergeEndpointCarriesTheBrowserFallback(t *testing.T) {
	client, _ := clientWith("t", reply{status: 403, body: `{}`})

	_, failure := client.Merge(pathauto(t), 12, "")
	if failure == nil || failure.Kind != EndpointClosed {
		t.Fatalf("got %v", failure)
	}
	want := "https://web.test/project/pathauto/-/merge_requests/12"
	if failure.BrowserURL != want {
		t.Errorf("browser URL %q, want %q", failure.BrowserURL, want)
	}
	if !strings.Contains(failure.Message, want) {
		t.Error("the message does not carry the fallback")
	}
}

func TestPostNoteSendsTheBody(t *testing.T) {
	client, transport := clientWith("t", ok(`{"id":1}`))

	if failure := client.PostNote(pathauto(t), 12, "looks good"); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if body := bodyOf(t, transport, 0); !strings.Contains(body, `"body":"looks good"`) {
		t.Errorf("body %q", body)
	}
}

// Neither a missing file nor a missing branch is worth stopping for.
func TestFileContentsIsEmptyRatherThanFailing(t *testing.T) {
	for _, status := range []int{404, 403, 500} {
		client, _ := clientWith("", reply{status: status, body: `nope`})

		if got := client.FileContents(pathauto(t), "pathauto.info.yml", "2.0.x"); got != "" {
			t.Errorf("HTTP %d: got %q, want nothing", status, got)
		}
	}
}

// The file path travels as one segment too, and the raw body comes back as-is
// rather than through the JSON boundary.
func TestFileContentsReturnsTheRawBody(t *testing.T) {
	client, transport := clientWith("", ok("name: Pathauto\ncore_version_requirement: ^10.2 || ^11\n"))

	got := client.FileContents(pathauto(t), "pathauto.info.yml", "2.0.x")
	if !strings.Contains(got, "core_version_requirement") {
		t.Errorf("got %q", got)
	}
	if url := transport.urls()[0]; !strings.Contains(url, "pathauto.info.yml/raw?ref=2.0.x") {
		t.Errorf("requested %q", url)
	}
}

// An unknown tag is a domain-level miss: the tag list came back fine. Claiming
// an HTTP 404 that did not happen would send somebody looking for the wrong
// thing.
func TestAnUnknownTagIsMissingRatherThanNotFound(t *testing.T) {
	client, _ := clientWith("", ok(`[{"name":"1.0.0","commit":{"id":"a","created_at":"2026-01-01T00:00:00Z"}}]`))

	_, failure := client.MergedSinceTag(pathauto(t), "9.9.9")
	if failure == nil || failure.Kind != ResourceMissing {
		t.Fatalf("got %v, want a resource-missing failure", failure)
	}
	if failure.Status != 0 {
		t.Errorf("claimed HTTP %d for a call that succeeded", failure.Status)
	}
	if !strings.Contains(failure.Message, `tag "9.9.9"`) {
		t.Errorf("message %q", failure.Message)
	}
}

// A tag with no readable date cannot answer "merged since that tag", and
// guessing one would date the release notes wrongly.
func TestAnUndatedTagIsMalformedRatherThanSilentlyEpoch(t *testing.T) {
	client, _ := clientWith("", ok(`[{"name":"1.0.0"}]`))

	_, failure := client.MergedSinceTag(pathauto(t), "1.0.0")
	if failure == nil || failure.Kind != MalformedResponse {
		t.Fatalf("got %v, want a malformed response", failure)
	}
}

func TestMergedSinceTagFiltersFromTheTagsDate(t *testing.T) {
	client, transport := clientWith("",
		ok(`[{"name":"1.0.0","commit":{"id":"a","created_at":"2026-06-12T10:00:00Z"}}]`),
		ok(`[{"iid":3,"state":"merged"}]`),
	)

	merged, failure := client.MergedSinceTag(pathauto(t), "1.0.0")
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if len(merged) != 1 || merged[0].IID != 3 {
		t.Errorf("got %+v", merged)
	}
	if url := transport.urls()[1]; !strings.Contains(url, "updated_after=2026-06-12T10%3A00%3A00Z") {
		t.Errorf("requested %q, want the tag's date", url)
	}
}

func TestMergedSinceFiltersOnMergedState(t *testing.T) {
	client, transport := clientWith("", ok(`[]`))

	since := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, failure := client.MergedSince(pathauto(t), since); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	url := transport.urls()[0]
	for _, want := range []string{"state=merged", "scope=all", "updated_after=2026-01-02T03%3A04%3A05Z"} {
		if !strings.Contains(url, want) {
			t.Errorf("requested %q, missing %q", url, want)
		}
	}
}

func TestOpenAndMergedListingsAskForWhatTheyName(t *testing.T) {
	client, transport := clientWith("", ok(`[]`), ok(`[]`))
	project := pathauto(t)

	if _, failure := client.OpenMergeRequests(project); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if !strings.Contains(transport.urls()[0], "state=opened") {
		t.Errorf("open listing asked for %q", transport.urls()[0])
	}

	if _, failure := client.MergedMergeRequests(project, 25); failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	url := transport.urls()[1]
	for _, want := range []string{"state=merged", "order_by=updated_at", "sort=desc", "per_page=25"} {
		if !strings.Contains(url, want) {
			t.Errorf("merged listing %q missing %q", url, want)
		}
	}
}

func TestBranchNamesSkipsUnnamedRows(t *testing.T) {
	client, _ := clientWith("", ok(`[{"name":"2.0.x"},{"name":""},{"default":true},{"name":"1.0.x"}]`))

	names, failure := client.BranchNames(pathauto(t))
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if len(names) != 2 || names[0] != "2.0.x" || names[1] != "1.0.x" {
		t.Errorf("got %v", names)
	}
}

func TestTagsAreRead(t *testing.T) {
	client, _ := clientWith("", ok(`[
		{"name":"2.0.1","commit":{"id":"bbb","created_at":"2026-07-01T00:00:00Z"}},
		{"name":"2.0.0","commit":{"id":"aaa","created_at":"2026-01-01T00:00:00Z"}}
	]`))

	tags, failure := client.Tags(pathauto(t))
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if len(tags) != 2 || tags[0].Name != "2.0.1" {
		t.Errorf("got %+v, want GitLab's own ordering preserved", tags)
	}
}

// The short code is what the dashboard prints, so no consumer re-derives the
// taxonomy from a class name.
func TestShortCodesAreStable(t *testing.T) {
	for _, tc := range []struct {
		failure *Failure
		want    string
	}{
		{unauthorized("u"), "401"},
		{endpointClosed("u"), "403"},
		{notFound("u"), "404"},
		{rateLimited(0, "u"), "rate-limited"},
		{requestRejected(422, "", "u"), "422"},
		{malformedResponse("m", 200, "u"), "malformed"},
		{transportError("t", "u"), "transport"},
		{resourceMissing("tag", "project", "u"), "missing"},
	} {
		if got := tc.failure.ShortCode(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

// A failure is an error, so it can travel where one is expected.
func TestAFailureIsAnError(t *testing.T) {
	var err error = notFound("https://web.test/x")
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("Error() is %q", err.Error())
	}
}

func bodyOf(t *testing.T, transport *recorder, index int) string {
	t.Helper()

	request := transport.requests[index]
	if request.Body == nil {
		return ""
	}
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return string(raw)
}

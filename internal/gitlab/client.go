package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/owenbush/upkeep/internal/drupal"
)

const (
	// maxForkPages bounds fork pagination: 100 per page, so 20 pages is 2000
	// forks.
	maxForkPages = 20

	// MaxPages is the pagination guard. MembershipProjects trusts the server
	// to eventually hand back an empty page; this bounds what "eventually" may
	// mean so a misbehaving endpoint cannot spin forever issuing requests.
	MaxPages = 50

	// idleTimeout is the longest gap tolerated while waiting for a response to
	// begin.
	idleTimeout = 15 * time.Second

	// maxDuration is the total per-request cap. Every call here is a small
	// JSON document or a single merge; none has a legitimate reason to take a
	// minute, and an unbounded one is indistinguishable from a hang.
	maxDuration = 60 * time.Second

	// DefaultAPIBase and DefaultBrowserBase are drupal.org's GitLab.
	DefaultAPIBase     = "https://git.drupalcode.org/api/v4"
	DefaultBrowserBase = "https://git.drupalcode.org"
)

// doer is the HTTP seam, so tests need no network.
type doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client is a read/merge client for the git.drupalcode.org GitLab REST API
// (v4).
//
//   - Authenticates with the maintainer's Git-access PAT via the PRIVATE-TOKEN
//     header. The token is held privately and is never echoed into any result,
//     message, or log output produced here.
//   - Built for a block-by-default instance: every failure mode — HTTP status,
//     unusable body, or no response at all — is returned as a *Failure.
//     Nothing escapes as a transport error; see FailureKind for the full
//     condition table.
//   - Rate-limit friendly: GET responses are memoized per client instance
//     (= per command invocation), so the same resource is never fetched twice
//     within one run.
type Client struct {
	http  doer
	token string

	apiBase     string
	browserBase string

	// mu guards cache. The PHP client runs one request at a time; this one is
	// safe to share across goroutines, though it does not collapse two
	// concurrent reads of the same URL into one request.
	mu sync.Mutex
	// cache is the per-invocation GET memoization, keyed by full request URL.
	// The client lives for a single command run, so entries are never stale
	// within the consistency window the CLI cares about.
	//
	// Successes and STABLE failures only: a transient failure (rate limit,
	// network blip, gateway interstitial) must not fail that resource for the
	// rest of the run — see Failure.Transient.
	cache map[string]cached
}

type cached struct {
	data    any
	failure *Failure
}

// NewClient builds a client. An empty token reads anonymously; empty bases
// take drupal.org's.
//
// git.drupalcode.org serves every public project's merge requests, refs, forks
// and raw files without a credential — measured across this project's whole
// read surface — so a credential is a requirement of *writing*, not of
// looking. An empty token omits the header entirely rather than sending an
// empty one, which GitLab answers with a 401.
func NewClient(httpClient doer, token, apiBase, browserBase string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: maxDuration,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				ResponseHeaderTimeout: idleTimeout,
			},
		}
	}
	if apiBase == "" {
		apiBase = DefaultAPIBase
	}
	if browserBase == "" {
		browserBase = DefaultBrowserBase
	}

	return &Client{
		http:        httpClient,
		token:       token,
		apiBase:     apiBase,
		browserBase: browserBase,
		cache:       map[string]cached{},
	}
}

// Fresh is a copy of this client with an empty GET-memoization cache.
//
// Within one run, GETs are intentionally memoized (rate-limit friendliness) —
// but the freshness re-check immediately before a fast-lane merge must observe
// the merge request as it is NOW, not as memoized at row-assembly time. That
// re-check goes through a Fresh copy; the original's cache is left untouched.
func (c *Client) Fresh() *Client {
	return &Client{
		http:        c.http,
		token:       c.token,
		apiBase:     c.apiBase,
		browserBase: c.browserBase,
		cache:       map[string]cached{},
	}
}

// IsAnonymous reports whether this client is reading without a credential.
func (c *Client) IsAnonymous() bool { return c.token == "" }

// Project looks up a project by module name ("conditions_helper") or full
// namespaced path ("project/conditions_helper").
func (c *Client) Project(moduleOrPath string) (*Project, *Failure) {
	path := moduleOrPath
	if !strings.Contains(path, "/") {
		path = "project/" + path
	}

	// PathEscape, not a bare interpolation: GitLab addresses a project by its
	// namespaced path with the slash encoded ("project%2Fpathauto"), and the
	// unescaped form addresses a path that does not exist.
	data, failure := c.get(
		c.apiBase+"/projects/"+url.PathEscape(path),
		c.browserBase+"/"+path,
	)
	if failure != nil {
		return nil, failure
	}

	return c.projectFromBody(data, c.browserBase+"/"+path)
}

// IssueFork is the drupal.org issue fork for an issue, or nil when there is
// none yet.
//
// This is how contributing to Drupal actually works: branches do not go on the
// canonical project. drupal.org's issue page mints a fork at
// issue/<machine-name>-<nid>, the branch is pushed there, and the merge request
// is opened *across* projects into the canonical one. Measured on pathauto,
// 100 of 100 open merge requests come from a fork and none from the project.
//
// A missing fork is **nil with no failure**, not an error — it is an ordinary
// state of an issue nobody has started, and the caller's job is to say how to
// make one rather than to report a problem.
func (c *Client) IssueFork(project Project, issueNid int) (*Project, *Failure) {
	path := "issue/" + project.Path + "-" + strconv.Itoa(issueNid)
	browserURL := c.browserBase + "/" + path

	data, failure := c.get(c.apiBase+"/projects/"+url.PathEscape(path), browserURL)
	if failure != nil {
		if failure.Kind == NotFound {
			return nil, nil
		}

		return nil, failure
	}

	return c.projectFromBody(data, browserURL)
}

// FileContents is one file's contents at a ref, or "" when it is not there.
//
// Used for a branch's .info.yml, which is the only place a module says which
// Drupal cores it supports. Empty covers both "no such file" and "no such
// branch"; neither is an error worth stopping for, and the caller falls back
// to the tracked core set whole.
func (c *Client) FileContents(project Project, path, ref string) string {
	requestURL := fmt.Sprintf(
		"%s/projects/%d/repository/files/%s/raw?ref=%s",
		c.apiBase, project.ID, url.PathEscape(path), url.QueryEscape(ref),
	)

	response, body, err := c.send(http.MethodGet, requestURL, nil)
	if err != nil || response.StatusCode != http.StatusOK {
		return ""
	}

	return string(body)
}

// BranchNames is the project's branch names.
//
// Read so that an issue's "Version" field can be turned into a branch by
// *checking* rather than by parsing: the field holds whatever anyone has
// typed, and only the project can say which of the readings is real.
func (c *Client) BranchNames(project Project) ([]string, *Failure) {
	requestURL := fmt.Sprintf("%s/projects/%d/repository/branches?per_page=100", c.apiBase, project.ID)
	browserURL := project.WebURL + "/-/branches"

	rows, failure := c.rows(requestURL, browserURL, "branches")
	if failure != nil {
		return nil, failure
	}

	names := []string{}
	for _, row := range rows {
		if name := (payload{data: row}).str("name", ""); name != "" {
			names = append(names, name)
		}
	}

	return names, nil
}

// IssueForkNids is every issue fork of a project, as source-project id to
// issue node id.
//
// One request per project (paginated) rather than one per merge request: a
// busy project has a fork per issue, and resolving each merge request's source
// project on its own would be a request per row.
func (c *Client) IssueForkNids(project Project) (map[int]int, *Failure) {
	nids := map[int]int{}
	browserURL := project.WebURL + "/-/forks"

	for page := 1; page <= maxForkPages; page++ {
		requestURL := fmt.Sprintf("%s/projects/%d/forks?per_page=100&page=%d", c.apiBase, project.ID, page)

		rows, failure := c.rows(requestURL, browserURL, "forks")
		if failure != nil {
			return nil, failure
		}
		if len(rows) == 0 {
			break
		}

		for _, row := range rows {
			p := payload{data: row}
			id, hasID := scalarInt(row["id"])
			nid, hasNid := drupal.IssueFromForkPath(p.str("path_with_namespace", ""))
			if hasID && hasNid {
				nids[id] = nid
			}
		}

		if len(rows) < 100 {
			break
		}
	}

	return nids, nil
}

// OpenMergeRequests lists a project's open merge requests.
func (c *Client) OpenMergeRequests(project Project) ([]MergeRequest, *Failure) {
	requestURL := fmt.Sprintf(
		"%s/projects/%d/merge_requests?state=opened&scope=all&per_page=100",
		c.apiBase, project.ID,
	)

	return c.mergeRequests(requestURL, project.WebURL+"/-/merge_requests", "merge requests")
}

// MergedMergeRequests is recently merged merge requests, newest first.
//
// Bounded rather than exhaustive: the question is whether an *open* issue
// already has work merged, and a merge old enough to fall off this list is old
// enough that the issue's staying open is a deliberate choice rather than an
// oversight.
func (c *Client) MergedMergeRequests(project Project, limit int) ([]MergeRequest, *Failure) {
	requestURL := fmt.Sprintf(
		"%s/projects/%d/merge_requests?state=merged&order_by=updated_at&sort=desc&per_page=%d",
		c.apiBase, project.ID, limit,
	)

	return c.mergeRequests(requestURL, project.WebURL+"/-/merge_requests?state=merged", "merged merge requests")
}

// MergeRefSHA is the SHA of refs/merge-requests/<iid>/merge — the branch
// merged into the current tip of its target.
//
// The revision a merge request's local evidence is actually about, now that
// upkeep checks the merge rather than the branch. Keying a cached result on
// the head SHA alone would leave it reading as current after the *target*
// moved, because the merge tree depends on both sides and the head does not
// change when only one of them does.
//
// Empty when GitLab publishes no merge ref — the merge request conflicts with
// its target — which is the same condition the adapter falls back to the
// branch on, so both sides fall back together.
func (c *Client) MergeRefSHA(project Project, iid int) string {
	data, failure := c.get(
		fmt.Sprintf("%s/projects/%d/merge_requests/%d/merge_ref", c.apiBase, project.ID, iid),
		mergeRequestBrowserURL(project, iid),
	)
	if failure != nil {
		return ""
	}

	object, ok := data.(map[string]any)
	if !ok {
		return ""
	}

	return (payload{data: object}).str("commit_id", "")
}

// MergeRequest fetches a single merge request; the payload includes
// head_pipeline, detailed_merge_status and the diff refs.
func (c *Client) MergeRequest(project Project, iid int) (*MergeRequest, *Failure) {
	browserURL := mergeRequestBrowserURL(project, iid)
	data, failure := c.get(
		fmt.Sprintf("%s/projects/%d/merge_requests/%d", c.apiBase, project.ID, iid),
		browserURL,
	)
	if failure != nil {
		return nil, failure
	}

	return c.mergeRequestFromBody(data, browserURL)
}

// HeadPipeline is the head pipeline of a merge request, or nil when it has
// none.
//
// Sourced from the single-merge-request endpoint, memoized together with
// MergeRequest, so combined use costs one request.
func (c *Client) HeadPipeline(project Project, iid int) (*Pipeline, *Failure) {
	mr, failure := c.MergeRequest(project, iid)
	if failure != nil {
		return nil, failure
	}

	return mr.HeadPipeline, nil
}

// MembershipProjects is every project the token holder is a member of — for a
// Drupal.org maintainer, their maintained projects. It paginates until an
// empty page; callers filter namespaces (contrib modules live under project/).
//
// Only two things end the loop legitimately: a failure, or an empty page.
// Anything else — a JSON object where a list was promised, or a server that
// never stops handing back full pages — is a protocol fault and ends as a
// malformed response rather than as an unbounded request loop.
func (c *Client) MembershipProjects() ([]Project, *Failure) {
	projects := []Project{}
	browserURL := c.browserBase + "/dashboard/projects"

	for page := 1; page <= MaxPages; page++ {
		requestURL := fmt.Sprintf("%s/projects?membership=true&simple=true&per_page=100&page=%d", c.apiBase, page)

		rows, failure := c.rows(requestURL, browserURL, "projects")
		if failure != nil {
			return nil, failure
		}
		if len(rows) == 0 {
			return projects, nil
		}
		for _, row := range rows {
			projects = append(projects, projectFrom(payload{data: row}))
		}
	}

	return nil, malformedResponse(
		fmt.Sprintf(
			"Project membership pagination did not reach an empty page within %d pages; giving up.",
			MaxPages,
		),
		0,
		browserURL,
	)
}

// Tags is the repository's tags, newest first (GitLab's default ordering).
func (c *Client) Tags(project Project) ([]Tag, *Failure) {
	requestURL := fmt.Sprintf("%s/projects/%d/repository/tags", c.apiBase, project.ID)

	rows, failure := c.rows(requestURL, project.WebURL+"/-/tags", "tags")
	if failure != nil {
		return nil, failure
	}

	tags := make([]Tag, 0, len(rows))
	for _, row := range rows {
		tags = append(tags, tagFrom(payload{data: row}))
	}

	return tags, nil
}

// MergedSince is merge requests merged (well: in merged state, updated) since
// a moment — the release-notes source.
//
// Known limitation: GitLab's list API cannot filter on merge date, so this
// filters state=merged by updated_after. Every merge request merged after
// since is included (merging bumps updated_at), but one merged BEFORE since
// and touched afterwards — a comment, a relabel — appears too. The result is a
// superset keyed on update time, not merge time. Acceptable because the output
// is a release-notes DRAFT that a human reviews before publishing.
func (c *Client) MergedSince(project Project, since time.Time) ([]MergeRequest, *Failure) {
	query := url.Values{}
	query.Set("state", "merged")
	query.Set("scope", "all")
	query.Set("per_page", "100")
	query.Set("updated_after", since.Format(time.RFC3339))

	requestURL := fmt.Sprintf("%s/projects/%d/merge_requests?%s", c.apiBase, project.ID, query.Encode())

	return c.mergeRequests(requestURL, project.WebURL+"/-/merge_requests?state=merged", "merge requests")
}

// MergedSinceTag is merge requests merged since the given tag was created. It
// resolves the tag's commit date via Tags, then delegates to MergedSince.
//
// An unknown tag is a domain-level miss, not an HTTP one: the tag list came
// back fine, it just does not contain that tag. It is therefore a
// resource-missing failure with no status, and never a 404, so no message ever
// claims an HTTP status that did not happen.
func (c *Client) MergedSinceTag(project Project, tagName string) ([]MergeRequest, *Failure) {
	tags, failure := c.Tags(project)
	if failure != nil {
		return nil, failure
	}

	browserURL := project.WebURL + "/-/tags"
	for _, tag := range tags {
		if tag.Name != tagName {
			continue
		}
		if tag.CreatedAt.IsZero() {
			return nil, malformedResponse(
				fmt.Sprintf(
					"Tag %q in %s carries no commit date, so \"merged since that tag\" cannot be resolved.",
					tagName, project.PathWithNamespace,
				),
				0,
				browserURL,
			)
		}

		return c.MergedSince(project, tag.CreatedAt)
	}

	return nil, resourceMissing(fmt.Sprintf("tag %q", tagName), project.PathWithNamespace, browserURL)
}

// CreateMergeRequest opens a merge request from project's branch.
//
// The write that closes upkeep's loop: a branch started by `upkeep start` and
// pushed by `upkeep publish` becomes a merge request, which is what every
// other verb in this tool already knows how to handle. Deliberately *not* a
// merge, and subject to none of the fast-lane policy: opening a merge request
// proposes work for review, which is the opposite of the unattended-merge risk
// that policy exists to prevent.
//
// into is the project the merge request targets when it differs from the one
// holding the branch — which on drupal.org is the normal case, not the
// exception: the branch lives on an issue fork and the merge request goes into
// the canonical project. GitLab wants such a request POSTed to the *source*
// project with target_project_id naming the destination, which reads backwards
// until you remember the branch is the subject.
func (c *Client) CreateMergeRequest(
	project Project,
	sourceBranch, targetBranch, title, description string,
	into *Project,
) (*MergeRequest, *Failure) {
	target := project
	if into != nil {
		target = *into
	}
	browserURL := target.WebURL + "/-/merge_requests"

	if c.IsAnonymous() {
		return nil, unauthorized(browserURL)
	}

	body := map[string]any{
		"source_branch": sourceBranch,
		"target_branch": targetBranch,
		"title":         title,
		"description":   description,
		// Drupal.org convention: the branch is the contributor's and stays
		// theirs. Nothing here deletes what it did not create.
		"remove_source_branch": false,
	}
	if into != nil && into.ID != project.ID {
		body["target_project_id"] = into.ID
	}

	data, failure := c.request(
		http.MethodPost,
		fmt.Sprintf("%s/projects/%d/merge_requests", c.apiBase, project.ID),
		body,
		target.WebURL+"/-/merge_requests/new",
	)
	if failure != nil {
		return nil, failure
	}

	return c.mergeRequestFromBody(data, browserURL)
}

// MergeRequestForBranch is the open merge request for a branch, or nil when
// there is none.
//
// Asked before opening a new one, because GitLab answers a duplicate with a
// 409 whose message is about validation rather than about the merge request
// that already exists — and the useful outcome for an operator re-running
// publish is a link to their own merge request, not an error.
//
// from narrows it to a branch on that project. Branch names on drupal.org are
// the issue node id and a slug, so the same name exists on every fork of an
// issue — without the narrowing, "is mine already open?" can answer yes about
// somebody else's.
func (c *Client) MergeRequestForBranch(
	project Project,
	sourceBranch string,
	from *Project,
) (*MergeRequest, *Failure) {
	requestURL := fmt.Sprintf(
		"%s/projects/%d/merge_requests?state=opened&per_page=100&source_branch=%s",
		c.apiBase, project.ID, url.QueryEscape(sourceBranch),
	)
	browserURL := project.WebURL + "/-/merge_requests"

	rows, failure := c.rows(requestURL, browserURL, "merge requests")
	if failure != nil {
		return nil, failure
	}

	for _, row := range rows {
		mr := mergeRequestFrom(payload{data: row})
		// A zero source project id is the payload not saying, which is
		// accepted rather than rejected: narrowing exists to exclude somebody
		// else's fork, not to discard a merge request GitLab described thinly.
		if from == nil || mr.SourceProjectID == 0 || mr.SourceProjectID == from.ID {
			return &mr, nil
		}
	}

	return nil, nil
}

// PostNote posts a note (comment) on a merge request.
func (c *Client) PostNote(project Project, iid int, body string) *Failure {
	browserURL := mergeRequestBrowserURL(project, iid)
	if c.IsAnonymous() {
		return unauthorized(browserURL)
	}

	_, failure := c.request(
		http.MethodPost,
		fmt.Sprintf("%s/projects/%d/merge_requests/%d/notes", c.apiBase, project.ID, iid),
		map[string]any{"body": body},
		browserURL,
	)

	return failure
}

// Merge merges exactly one merge request.
//
// Deliberately a single-action call: one project, one iid, one PUT — there is
// no batch variant at any layer of this client. That enforces the DA-policy
// stance (one human-approved action at a time) structurally.
//
// expectedHeadSHA, when given, is passed as the API's "sha" guard so the merge
// is refused if the branch moved since the human reviewed it.
//
// Note: this endpoint is unverified on git.drupalcode.org (a block-by-default
// instance); a 403 endpoint-closed failure carrying the merge request's
// browser URL is an expected outcome and the caller's degraded path.
func (c *Client) Merge(project Project, iid int, expectedHeadSHA string) (*MergeRequest, *Failure) {
	browserURL := mergeRequestBrowserURL(project, iid)
	if c.IsAnonymous() {
		return nil, unauthorized(browserURL)
	}

	body := map[string]any{}
	if expectedHeadSHA != "" {
		body["sha"] = expectedHeadSHA
	}

	data, failure := c.request(
		http.MethodPut,
		fmt.Sprintf("%s/projects/%d/merge_requests/%d/merge", c.apiBase, project.ID, iid),
		body,
		browserURL,
	)
	if failure != nil {
		return nil, failure
	}

	return c.mergeRequestFromBody(data, browserURL)
}

func mergeRequestBrowserURL(project Project, iid int) string {
	return fmt.Sprintf("%s/-/merge_requests/%d", project.WebURL, iid)
}

// mergeRequests is the shared tail of every merge-request listing.
func (c *Client) mergeRequests(requestURL, browserURL, what string) ([]MergeRequest, *Failure) {
	rows, failure := c.rows(requestURL, browserURL, what)
	if failure != nil {
		return nil, failure
	}

	list := make([]MergeRequest, 0, len(rows))
	for _, row := range rows {
		list = append(list, mergeRequestFrom(payload{data: row}))
	}

	return list, nil
}

// object narrows a decoded body to the single JSON object a resource endpoint
// promises.
func object(data any, what, browserURL string) (payload, *Failure) {
	if fields, ok := data.(map[string]any); ok {
		return payload{data: fields}, nil
	}

	return payload{}, malformedResponse(
		fmt.Sprintf("Expected a %s object from GitLab, got a JSON list.", what),
		200,
		browserURL,
	)
}

func (c *Client) projectFromBody(data any, browserURL string) (*Project, *Failure) {
	fields, failure := object(data, "project", browserURL)
	if failure != nil {
		return nil, failure
	}
	built := projectFrom(fields)

	return &built, nil
}

func (c *Client) mergeRequestFromBody(data any, browserURL string) (*MergeRequest, *Failure) {
	fields, failure := object(data, "merge request", browserURL)
	if failure != nil {
		return nil, failure
	}
	built := mergeRequestFrom(fields)

	return &built, nil
}

// rows GETs a collection endpoint and narrows the body to the list of JSON
// objects it promises.
func (c *Client) rows(requestURL, browserURL, what string) ([]map[string]any, *Failure) {
	data, failure := c.get(requestURL, browserURL)
	if failure != nil {
		return nil, failure
	}

	return objectRows(data, what, requestURL, browserURL)
}

// objectRows is the collection half of the boundary: it narrows a decoded body
// into the list of JSON objects a collection endpoint promises, or says why it
// is not one.
//
// Two structural faults are caught here, both of which the tolerant field
// narrowing in payload cannot paper over: a JSON object where a list was
// promised — the shape that used to make pagination loop forever — and a list
// carrying an entry that is not an object, which used to reach a model as a
// scalar. After this, every row handed to a from* function is a real object.
func objectRows(data any, what, requestURL, browserURL string) ([]map[string]any, *Failure) {
	list, ok := data.([]any)
	if !ok {
		return nil, malformedResponse(
			fmt.Sprintf("Expected a list of %s from %s, got a JSON object.", what, requestURL),
			200,
			browserURL,
		)
	}

	rows := make([]map[string]any, 0, len(list))
	for index, item := range list {
		fields, ok := item.(map[string]any)
		if !ok {
			return nil, malformedResponse(
				fmt.Sprintf(
					"Expected the list of %s from %s to hold JSON objects; entry %d is %s.",
					what, requestURL, index, jsonTypeName(item),
				),
				200,
				browserURL,
			)
		}
		rows = append(rows, fields)
	}

	return rows, nil
}

// jsonTypeName names a decoded value the way the PHP side's get_debug_type
// does, so the two messages read alike.
func jsonTypeName(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "float"
	case string:
		return "string"
	case []any:
		return "array"
	default:
		return fmt.Sprintf("%T", value)
	}
}

// get performs a GET and decodes the JSON body, or returns a failure.
//
// It memoizes successes and stable failures. A transient failure is returned
// but NOT stored: a rate limit or a network blip midway through a long
// dashboard run must not turn into a permanent verdict for that resource.
func (c *Client) get(requestURL, browserURL string) (any, *Failure) {
	c.mu.Lock()
	entry, hit := c.cache[requestURL]
	c.mu.Unlock()
	if hit {
		return entry.data, entry.failure
	}

	data, failure := c.request(http.MethodGet, requestURL, nil, browserURL)
	if failure == nil || !failure.Transient() {
		c.mu.Lock()
		c.cache[requestURL] = cached{data: data, failure: failure}
		c.mu.Unlock()
	}

	return data, failure
}

// request performs a request and decodes the JSON body, or returns a failure.
//
// Total by construction: every transport error is caught, so the documented
// "never fails except as a *Failure" holds. The token never reaches a message
// — it travels only in the PRIVATE-TOKEN header, and error text is taken from
// the response body.
func (c *Client) request(method, requestURL string, body any, browserURL string) (any, *Failure) {
	response, raw, err := c.send(method, requestURL, body)
	if err != nil {
		return nil, transportError("HTTP transport failure: "+err.Error(), browserURL)
	}

	status := response.StatusCode
	if status >= 400 {
		retryAfter := 0
		if status == http.StatusTooManyRequests {
			if seconds, err := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After"))); err == nil {
				retryAfter = seconds
			}
		}

		return nil, failureFor(status, errorDetail(raw), browserURL, retryAfter)
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, malformedResponse(
			fmt.Sprintf("Unusable response body (HTTP %d) from %s: %s", status, requestURL, err.Error()),
			status,
			browserURL,
		)
	}
	switch decoded.(type) {
	case map[string]any, []any:
		return decoded, nil
	default:
		return nil, malformedResponse(
			fmt.Sprintf(
				"Unusable response body (HTTP %d) from %s: expected a JSON object or array, got %s",
				status, requestURL, jsonTypeName(decoded),
			),
			status,
			browserURL,
		)
	}
}

// send is the one place an HTTP request leaves this process.
func (c *Client) send(method, requestURL string, body any) (*http.Response, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), maxDuration)
	defer cancel()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return nil, nil, err
	}
	// An absent header when anonymous, rather than an empty PRIVATE-TOKEN —
	// GitLab reads a present-but-empty credential as a bad one and answers
	// 401, where no header at all is an ordinary public read.
	if !c.IsAnonymous() {
		request.Header.Set("PRIVATE-TOKEN", c.token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, nil, err
	}

	return response, raw, nil
}

// errorDetail is the human-readable complaint out of a GitLab error body, if
// it has one. Only the body's message/error field is read — never request
// headers, so no credential can be quoted back.
func errorDetail(body []byte) string {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}

	detail, ok := decoded["message"]
	if !ok {
		detail = decoded["error"]
	}

	// GitLab returns either a string or a list of complaints. Only the scalar
	// entries are quotable; a nested structure has no sensible one-line
	// rendering and is dropped rather than printed as "Array".
	if list, ok := detail.([]any); ok {
		parts := []string{}
		for _, entry := range list {
			if text, ok := scalarString(entry); ok {
				parts = append(parts, text)
			}
		}

		return strings.Join(parts, "; ")
	}

	if text, ok := detail.(string); ok {
		return text
	}

	return ""
}

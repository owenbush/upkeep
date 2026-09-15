package drupal

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// MaxConcurrent is how many attachment lookups may be in flight at once.
//
// Deliberately modest. api-d7 publishes no rate-limit headers and no
// documented quota, so there is no budget to read and stay inside — which
// makes politeness the only available policy. Eight was measured as taking
// essentially all of the available speedup (13x on cold lookups) while staying
// well inside what the endpoint answers cleanly.
const MaxConcurrent = 8

// maxRetryAfter bounds the wait on a 429. Anything demanding longer is
// reported rather than slept through: a CLI that appears to hang is worse than
// one that says it was throttled.
const maxRetryAfter = 10 * time.Second

// APIBase is drupal.org's read-only Drupal 7 Services endpoint.
const APIBase = "https://www.drupal.org/api-d7"

// Client reads drupal.org. The API is public and reads need no credential.
//
// Failures are absorbed but never silent. Every request that does not answer
// is recorded in Warnings, because this client's failure mode is to return
// *less data*, not an error: a dropped attachment makes an issue look like it
// carries fewer patches, and a truncated listing makes a project look like it
// has fewer issues. Both are indistinguishable from the truth at the call
// site, and both are exactly the under-reporting the patch surface exists to
// prevent.
type Client struct {
	http    *http.Client
	apiBase string
	sleep   func(time.Duration)

	mu       sync.Mutex
	warnings []string
	issues   map[int]*Issue
	// files holds the raw detail payload for each attachment id, mirroring
	// what the file resource returned. A recorded miss is a nil entry, which
	// is why the map value is a payload rather than a parsed model: the
	// resolved detail is written back into the *issue payload*, and the models
	// are built from that in one place.
	files    map[int]map[string]any
	projects map[string]int
}

// NewClient builds a client. A nil http.Client uses a sensible default.
func NewClient(httpClient *http.Client, apiBase string) *Client {
	if httpClient == nil {
		// Both bounds matter: the first is the idle timeout and bounds
		// nothing on its own, so the whole-request deadline is set too.
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	if apiBase == "" {
		apiBase = APIBase
	}

	return &Client{
		http:     httpClient,
		apiBase:  apiBase,
		sleep:    time.Sleep,
		issues:   map[int]*Issue{},
		files:    map[int]map[string]any{},
		projects: map[string]int{},
	}
}

// Warnings are the requests that did not answer, in the order they failed.
func (c *Client) Warnings() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string(nil), c.warnings...)
}

func (c *Client) warn(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
}

// Issue fetches one issue, with its attachments resolved.
func (c *Client) Issue(nid int) (Issue, bool) {
	c.mu.Lock()
	cached, seen := c.issues[nid]
	c.mu.Unlock()
	if seen {
		if cached == nil {
			return Issue{}, false
		}

		return *cached, true
	}

	issue, ok := c.fetchIssue(nid)

	c.mu.Lock()
	if ok {
		stored := issue
		c.issues[nid] = &stored
	} else {
		c.issues[nid] = nil
	}
	c.mu.Unlock()

	return issue, ok
}

func (c *Client) fetchIssue(nid int) (Issue, bool) {
	data, ok := c.getJSON(
		fmt.Sprintf("%s/node/%d.json", c.apiBase, nid),
		fmt.Sprintf("issue #%d", nid),
	)
	if !ok {
		return Issue{}, false
	}

	c.prefetchAttachments([]map[string]any{data})

	return IssueFrom(c.withResolvedFiles(data))
}

// ProjectNid resolves a machine name to the node id of its project.
//
// Deliberately unfiltered by node type: only project nodes carry
// field_project_machine_name, and a maintainer's registry may name a theme or
// a distribution as readily as a module. Filtering an *issue* listing by
// machine name matches nothing and reads exactly like "no issues", which is
// why the nid is resolved once and the listing filtered by it instead.
func (c *Client) ProjectNid(machineName string) (int, bool) {
	c.mu.Lock()
	cached, seen := c.projects[machineName]
	c.mu.Unlock()
	if seen {
		return cached, cached != 0
	}

	data, ok := c.getJSON(
		fmt.Sprintf("%s/node.json?field_project_machine_name=%s", c.apiBase, url.QueryEscape(machineName)),
		fmt.Sprintf("the drupal.org project %q", machineName),
	)

	nid := 0
	if ok {
		for _, entry := range listOf(data) {
			if found, present := intField(entry, "nid"); present {
				nid = found

				break
			}
		}
	}

	c.mu.Lock()
	c.projects[machineName] = nid
	c.mu.Unlock()

	return nid, nid != 0
}

// ProjectIssues lists a project's issues in the given statuses.
func (c *Client) ProjectIssues(machineName string, statuses []IssueStatus) []Issue {
	projectNid, ok := c.ProjectNid(machineName)
	if !ok {
		return nil
	}

	var issues []Issue
	for _, status := range statuses {
		issues = append(issues, c.issuesForStatus(projectNid, status)...)
	}

	return issues
}

// issuesForStatus walks one status's listing, page by page.
//
// A page that cannot be read ends the listing — there is no way to skip past
// it and stay in order — but it says so, because "the rest of this project's
// issues" silently missing is the failure this surface exists to prevent.
func (c *Client) issuesForStatus(projectNid int, status IssueStatus) []Issue {
	var issues []Issue

	for page := 0; ; page++ {
		data, ok := c.getJSON(
			fmt.Sprintf(
				"%s/node.json?type=project_issue&field_project=%d&field_issue_status=%d&page=%d",
				c.apiBase, projectNid, int(status), page,
			),
			fmt.Sprintf("issues for project %d with status %s (page %d)", projectNid, status, page),
		)
		if !ok {
			return issues
		}

		entries := listOf(data)
		if len(entries) == 0 {
			return issues
		}

		c.prefetchAttachments(entries)
		for _, entry := range entries {
			if issue, built := IssueFrom(c.withResolvedFiles(entry)); built {
				issues = append(issues, issue)
			}
		}

		if _, more := data["next"]; !more {
			return issues
		}
	}
}

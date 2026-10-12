package gitlab

import (
	"fmt"
	"strings"

	"github.com/owenbush/upkeep/internal/drupal"
)

// Issues read from GitLab, for the projects whose queue has moved there.
//
// drupal.org is migrating issue queues into GitLab, and a project that has
// moved keeps its *merge* requests exactly where they were — so most of upkeep
// never notices.
//
// **api-d7 is not stale for a migrated project, and the first version of this
// comment said it was.** Measured on `helper`: api-d7 lists 73 open issues
// against GitLab's 74, and the statuses agree exactly — needs review 13,
// needs work 3, RTBC 1, to be ported 1. Every migrated issue kept its node,
// which still answers, and `www.drupal.org/node/<nid>` 301-redirects to the
// work item. So this is not a gap being filled.
//
// What justifies reading GitLab is the **one issue in that difference**. iid
// 3619172 was opened on 2026-10-02, after the migration, and has no drupal.org
// node at all — api-d7's own `node/3619172.json` is an unrelated change
// notice that happens to carry that number. A post-migration issue is not a
// drupal.org node, so no api-d7 query can return it, whatever it filters on.
// Today that is 1 issue of 74; it is *every future issue* on that project.
//
// The collision is the second half, and it is worse than a gap because it is
// wrong rather than missing: GitLab continues its iid sequence from the
// migrated nids, so a native issue's number is also some unrelated drupal.org
// node's id, and a URL or lookup built from it lands on that node. Reading the
// issue from GitLab is what keeps the number attached to the thing it names.
//
// **The same model comes back.** These return drupal.Issue, not a parallel
// type, because every consumer — the dashboard, the gate, the patch surface,
// `issues`, `start` — is about an issue rather than about where it was read
// from. A second shape would have been a second code path through all of them.
// Nid holds the GitLab iid: for a migrated issue that *is* the drupal.org nid,
// and for a native one it is the number the project uses to name the issue,
// which is the one a maintainer types.
//
// What is genuinely absent is attachments: a GitLab issue has no patch files,
// because a project that has moved takes contributions as merge requests. So
// Files is empty and the patch surface finds nothing, which is the truth
// rather than a gap.
//
// Detection is **not** here. drupal.org states it directly on the project node
// (`drupal.Client.UsesDrupalIssueQueue`), which is authoritative, costs no
// extra request, and answers for a migrated project with nothing open — where
// counting GitLab issues could not.

// The scoped labels a migrated project carries, which are drupal.org's own
// fields expressed the GitLab way.
//
// **Read off `helper`, never derived from drupal.org's own names**, and the
// difference is load-bearing: drupal.org's "Active" is `state::accepted` here,
// so the migration renames rather than transliterating. Anything not observed
// is therefore left out rather than guessed at — a wrong key reads a postponed
// issue as active, where a missing one leaves the structural state showing,
// which is true if coarse. An unrecognised label is ignored and the issue
// still lists.
//
// Measured over helper's 74 open and 100 most recently closed issues. The open
// vocabulary is complete at five values; `state::active`, `state::fixed`,
// `state::postponed` and `state::postponedNeedsInfo` return no rows at all.
var (
	// gitlabIssueStates is the *open* vocabulary. `state::closed` is
	// deliberately absent: closure is read from the structural state field and
	// refined by the why scope below, so leaving it out of this map is what
	// makes the mapping independent of label order.
	gitlabIssueStates = map[string]drupal.IssueStatus{
		"accepted":    drupal.StatusActive,
		"needsreview": drupal.StatusNeedsReview,
		"needswork":   drupal.StatusNeedsWork,
		"rtbc":        drupal.StatusRtbc,
		"tobeported":  drupal.StatusPatchToBePorted,
	}

	// gitlabIssueReasons is how the migration keeps drupal.org's several
	// flavours of closed, which it records as `state::closed` plus a reason —
	// 8 of helper's 100 closed issues carry one. Without this they would all
	// read as plainly fixed, which for a duplicate or a won't-fix is a
	// statement about somebody's issue that is simply false.
	//
	// Three are observed (duplicate, worksAsDesigned, wontFix) and are exactly
	// camelCase of drupal.org's own parenthetical — "Closed (works as
	// designed)", "Closed (won't fix)". The last two follow that rule, which
	// held three times for three; where the rule demonstrably broke, in the
	// state scope above, nothing is extrapolated.
	gitlabIssueReasons = map[string]drupal.IssueStatus{
		"duplicate":       drupal.StatusClosedDuplicate,
		"wontfix":         drupal.StatusClosedWontFix,
		"worksasdesigned": drupal.StatusClosedWorksAsDesigned,
		"outdated":        drupal.StatusClosedOutdated,
		"cannotreproduce": drupal.StatusClosedCannotReproduce,
	}

	gitlabIssueCategories = map[string]string{
		"bug":     "Bug report",
		"feature": "Feature request",
		"support": "Support request",
		"task":    "Task",
		"plan":    "Plan",
	}

	gitlabIssuePriorities = map[string]int{
		"critical": 400,
		"major":    300,
		"normal":   200,
		"minor":    100,
	}
)

// OpenIssues is every open issue the project has in GitLab.
func (c *Client) OpenIssues(project Project) ([]drupal.Issue, *Failure) {
	issues := []drupal.Issue{}

	for page := 1; page <= maxIssuePages; page++ {
		requestURL := fmt.Sprintf(
			"%s/projects/%d/issues?state=opened&per_page=100&page=%d", c.apiBase, project.ID, page)

		rows, failure := c.rows(requestURL, project.WebURL+"/-/issues", "issues")
		if failure != nil {
			return nil, failure
		}
		if len(rows) == 0 {
			break
		}

		for _, row := range rows {
			issues = append(issues, issueFromRow(row, project))
		}

		if len(rows) < 100 {
			break
		}
	}

	return issues, nil
}

// Issue is one GitLab issue by its iid, reporting false when it is not there.
func (c *Client) Issue(project Project, iid int) (*drupal.Issue, bool) {
	requestURL := fmt.Sprintf("%s/projects/%d/issues/%d", c.apiBase, project.ID, iid)

	// Mutation note: removing this check survives the suite, and the mutant is
	// equivalent rather than untested. A failure always comes back with nil
	// data — `request` returns `nil, failure` on every path and the cache
	// stores that pair — so the narrowing below refuses it too and the answer
	// is the same false. Kept because a read that failed and a body that was
	// the wrong shape are different things, and a reader should say which it
	// found even when the caller only wants a bool.
	data, failure := c.get(requestURL, project.WebURL+"/-/issues")
	if failure != nil {
		return nil, false
	}
	fields, failure := object(data, "issue", project.WebURL+"/-/issues")
	if failure != nil {
		return nil, false
	}

	issue := issueFromRow(fields.data, project)

	return &issue, true
}

// issueFromRow maps a GitLab issue onto the model every consumer already
// reads.
//
// Status is built in three passes, deliberately, so that label order cannot
// change the answer: the structural state field first, then the open state
// scope, then the closed-reason scope. GitLab returns labels alphabetically,
// which means `state::closed` and `why::duplicate` arrive in that order today
// and nothing guarantees they will tomorrow.
func issueFromRow(row map[string]any, project Project) drupal.Issue {
	entry := payload{data: row}

	issue := drupal.Issue{
		Nid:   entry.intOr("iid", 0),
		Title: entry.str("title", ""),
		// GitLab's own web_url, not a path assembled here: it already points
		// at /-/work_items/<iid>, which is what the project's links use.
		URL:     entry.str("web_url", ""),
		Project: project.Path,
		Status:  drupal.StatusActive,
	}
	// The structural state is the floor. A closed issue carrying no reason
	// label is plainly fixed, which is 92 of helper's 100 closed issues.
	if entry.str("state", "") == "closed" {
		issue.Status = drupal.StatusFixed
	}

	for _, label := range labelsIn(row) {
		scope, value, scoped := strings.Cut(label, "::")
		if !scoped {
			applyPlainLabel(&issue, label)

			continue
		}

		value = strings.ToLower(value)
		switch strings.ToLower(scope) {
		case "state":
			if status, known := gitlabIssueStates[value]; known {
				issue.Status = status
			}
		case "why":
			if status, known := gitlabIssueReasons[value]; known {
				issue.Status = status
			}
		case "category":
			if category, known := gitlabIssueCategories[value]; known {
				issue.Category = category
			}
		case "priority":
			if priority, known := gitlabIssuePriorities[value]; known {
				issue.Priority = priority
			}
		}
	}

	return issue
}

// applyPlainLabel sorts an unscoped label into the version or the component.
//
// A version label is drupal.org's branch, which the patch surface uses to pick
// a base branch. Everything else unscoped is a component or a tag — `code`,
// `documentation`, `ProjectUpdateBotD12` — and the first one is kept while the
// rest are ignored rather than concatenated into something nobody wrote.
//
// A *second* version label is ignored too, rather than falling through to the
// component: no issue on helper carries two, so this is latent rather than
// observed, but recording `v7.x-1.x-dev` as a component would be wrong by
// construction and the branch costs one line.
func applyPlainLabel(issue *drupal.Issue, label string) {
	if looksLikeBranch(label) {
		if issue.Version == "" {
			issue.Version = strings.TrimPrefix(label, "v")
		}

		return
	}

	if issue.Component == "" {
		issue.Component = label
	}
}

// looksLikeBranch tells a version label from a word beginning with v.
//
// "v8.x-1.x-dev" and "v2.x-dev" are branches; "views" is a component. A digit
// straight after the v is the whole test, and it is the one drupal.org's own
// branch names make true.
func looksLikeBranch(label string) bool {
	return strings.HasPrefix(label, "v") && len(label) > 1 && label[1] >= '0' && label[1] <= '9'
}

// labelsIn reads the label list off a row, tolerating its absence.
func labelsIn(row map[string]any) []string {
	raw, present := row["labels"]
	if !present {
		return nil
	}

	list, isList := raw.([]any)
	if !isList {
		return nil
	}

	labels := make([]string, 0, len(list))
	for _, each := range list {
		if label, isString := each.(string); isString {
			labels = append(labels, label)
		}
	}

	return labels
}

// maxIssuePages bounds the scan, as the fork listing is bounded: a project
// with more than this many open issues is not one upkeep can help with, and an
// unbounded loop against a paginating API is how a bug becomes a denial of
// service against somebody else's server.
const maxIssuePages = 20

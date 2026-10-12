package command

import (
	"fmt"
	"sync"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"

	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
)

// Which issue queue a project's issues are read from.
//
// drupal.org is moving issue queues into GitLab, one project at a time, so
// "where are this module's issues" stopped being a property of the tool and
// became a property of the module. This routes per project and records what it
// decided, because a command that says "Reading issues from drupal.org" while
// reading GitLab is worse than one that says nothing.
//
// **drupal.org's own answer decides**, not a count of what each side holds:
// `field_project_has_issue_queue` on the project node is false for a migrated
// project and true otherwise, it comes from a request `ProjectNid` already
// makes, and it answers for a migrated project with nothing open — which
// counting could not. Not knowing means reading drupal.org, which is what
// every release so far has done.
type IssueQueues struct {
	drupalClient drupalQueue

	// gitlabClient is resolved lazily: a project that has not moved never
	// needs it, and most have not.
	gitlabClient gitlabQueue
	gitlabFor    func(gitlab.Report) gitlabQueue

	mu sync.Mutex
	// read is which queue each project was actually read from, so a command
	// can report it rather than assert it.
	read map[string]Queue
	// notes are the GitLab-side failures. The drupal.org client keeps its own.
	notes []string
}

// drupalQueue is what the router reads from drupal.org, declared here because
// here is where it is used — the client itself knows nothing about routing.
type drupalQueue interface {
	ProjectIssues(machineName string, statuses []drupal.IssueStatus) []drupal.Issue
	Issue(nid int) (drupal.Issue, bool)
	User(uid int) (drupal.User, bool)
	// UsesDrupalIssueQueue is drupal.org's own statement about where the
	// project keeps its issues.
	UsesDrupalIssueQueue(machineName string) (uses, known bool)
	Warnings() []string
}

// gitlabQueue is what the router reads from GitLab, for a project that has
// moved.
type gitlabQueue interface {
	Project(moduleOrPath string) (*gitlab.Project, *gitlab.Failure)
	OpenIssues(project gitlab.Project) ([]drupal.Issue, *gitlab.Failure)
	Issue(project gitlab.Project, iid int) (*drupal.Issue, bool)
}

// Queue names an issue queue, for the commands that tell you which was read.
type Queue string

const (
	// QueueDrupalOrg is drupal.org's own issue queue, via api-d7.
	QueueDrupalOrg Queue = "drupal.org"
	// QueueGitLab is the project's GitLab issues, for a migrated project.
	QueueGitLab Queue = "GitLab"
)

// NewIssueQueues builds the production issue source.
//
// One drupal.org client for the whole run, so its warnings accumulate across
// every module a refresh touches and the shortfall is reported once at the
// end. The GitLab client comes from the same seam every other command reads
// through, rather than a second one built here, so a token and the cost of
// reading without one are resolved in exactly one place.
func NewIssueQueues(gitlabFor func(gitlab.Report) *gitlab.Client) *IssueQueues {
	return &IssueQueues{
		drupalClient: drupal.NewClient(nil, ""),
		gitlabFor: func(report gitlab.Report) gitlabQueue {
			return gitlabFor(report)
		},
		read: map[string]Queue{},
	}
}

// Issues is the reader a scan goes through.
func (q *IssueQueues) Issues() IssueSource { return q }

// Warnings is what the scans could not read, from both queues.
func (q *IssueQueues) Warnings() []string {
	q.mu.Lock()
	notes := append([]string{}, q.notes...)
	q.mu.Unlock()

	return append(notes, q.drupalClient.Warnings()...)
}

// QueueFor is which queue this project's issues were read from.
//
// Reports false before anything has been read, rather than guessing: the
// commands that print it do so after the scan, and a default would let the
// line be printed when nothing had happened.
func (q *IssueQueues) QueueFor(machineName string) (Queue, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	queue, decided := q.read[machineName]

	return queue, decided
}

// ProjectIssues lists a project's issues from whichever queue is live.
func (q *IssueQueues) ProjectIssues(machineName string, statuses []drupal.IssueStatus) []drupal.Issue {
	if !q.readsGitLab(machineName) {
		q.recordQueue(machineName, QueueDrupalOrg)

		return q.drupalClient.ProjectIssues(machineName, statuses)
	}

	project, failure := q.gitlab().Project(machineName)
	if failure != nil {
		// A migrated project whose GitLab project will not resolve is a
		// shortfall, not a reason to read the queue drupal.org has just said
		// is not the live one — that would report somebody's open issues as
		// though they were all there were.
		q.note(fmt.Sprintf(
			"could not resolve the GitLab project for %q [%s]: %s — its issues have moved to "+
				"GitLab, so none are listed",
			machineName, failure.ShortCode(), failure.Message,
		))
		q.recordQueue(machineName, QueueGitLab)

		return nil
	}

	issues, failure := q.gitlab().OpenIssues(*project)
	if failure != nil {
		q.note(fmt.Sprintf(
			"could not read %q's GitLab issues [%s]: %s",
			machineName, failure.ShortCode(), failure.Message,
		))
		q.recordQueue(machineName, QueueGitLab)

		return nil
	}
	q.recordQueue(machineName, QueueGitLab)

	return inStatuses(issues, statuses)
}

// Issue is one issue by the number the project uses to name it.
//
// The project is an argument because it has to be: a GitLab read is scoped to
// a project, where an api-d7 node id was global. It was always implicit — both
// call sites had the module in hand — and for a migrated project the implicit
// version is actively wrong, since GitLab continues its iid sequence from the
// migrated node ids and a native issue's number also belongs to some
// unrelated drupal.org node.
func (q *IssueQueues) Issue(machineName string, nid int) (drupal.Issue, bool) {
	if !q.readsGitLab(machineName) {
		q.recordQueue(machineName, QueueDrupalOrg)

		return q.drupalClient.Issue(nid)
	}

	project, failure := q.gitlab().Project(machineName)
	if failure != nil {
		q.recordQueue(machineName, QueueGitLab)

		return drupal.Issue{}, false
	}
	q.recordQueue(machineName, QueueGitLab)

	issue, found := q.gitlab().Issue(*project, nid)
	if !found {
		return drupal.Issue{}, false
	}

	return *issue, true
}

// User is the account that posted a patch file, which only drupal.org has.
//
// Never routed: a uid is a drupal.org account id whatever queue the issue came
// from, and the only caller is a patch promotion's attribution. A migrated
// project has no patch files to promote — it takes merge requests — so this is
// unreachable for one, and routing it would mean inventing a GitLab answer to
// a drupal.org question.
func (q *IssueQueues) User(uid int) (drupal.User, bool) {
	return q.drupalClient.User(uid)
}

// readsGitLab asks drupal.org where this project keeps its issues.
func (q *IssueQueues) readsGitLab(machineName string) bool {
	uses, known := q.drupalClient.UsesDrupalIssueQueue(machineName)

	return known && !uses
}

// gitlab resolves the read-only client once.
//
// A nil check under the mutex rather than a sync.Once, so a test can supply a
// reader directly: the alternative is a Once that has already fired, which
// there is no honest way to construct.
func (q *IssueQueues) gitlab() gitlabQueue {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.gitlabClient == nil {
		q.gitlabClient = q.gitlabFor(func(note string) {
			q.notes = append(q.notes, note)
		})
	}

	return q.gitlabClient
}

func (q *IssueQueues) note(note string) {
	q.mu.Lock()
	q.notes = append(q.notes, note)
	q.mu.Unlock()
}

func (q *IssueQueues) recordQueue(machineName string, queue Queue) {
	q.mu.Lock()
	q.read[machineName] = queue
	q.mu.Unlock()
}

// inStatuses narrows a GitLab read to the statuses the caller asked for.
//
// The GitLab read is the *open* queue, because every caller asks for open
// statuses (drupal.OpenStatuses, AwaitingReviewStatuses). A closed status in
// the request therefore matches nothing, which is why the filter is an
// intersection rather than a second query: answering it would mean fetching
// the closed queue on every scan to satisfy a caller that does not exist.
func inStatuses(issues []drupal.Issue, statuses []drupal.IssueStatus) []drupal.Issue {
	wanted := make(map[drupal.IssueStatus]bool, len(statuses))
	for _, status := range statuses {
		wanted[status] = true
	}

	kept := make([]drupal.Issue, 0, len(issues))
	for _, issue := range issues {
		if wanted[issue.Status] {
			kept = append(kept, issue)
		}
	}

	return kept
}

// QueueReader is a reader that can say which queue it read.
//
// An interface rather than the concrete router, so a command depends on being
// told where its answer came from and not on which implementation told it —
// and so the test fakes stay as small as they were.
type QueueReader interface {
	QueueFor(machineName string) (Queue, bool)
}

// reportIssueQueue says where a module's issues came from, when that is not
// where they have always come from.
//
// Silent for drupal.org, which is every project that has not moved and so
// almost all of them: a line confirming the status quo on every run is a line
// people stop reading, and this one exists to be noticed. Silent too for a
// reader that cannot say, rather than guessing on its behalf.
func reportIssueQueue(cmd *cobra.Command, source IssueSource, machineName string) {
	reader, says := source.(QueueReader)
	if !says {
		return
	}

	queue, decided := reader.QueueFor(machineName)
	if !decided || queue != QueueGitLab {
		return
	}

	cli.Progressf(cmd,
		"%s keeps its issues in GitLab, not on drupal.org — read from %s.",
		machineName, QueueGitLab)
}

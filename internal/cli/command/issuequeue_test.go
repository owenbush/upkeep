package command

import (
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// fakeDrupalQueue answers for drupal.org without a network.
type fakeDrupalQueue struct {
	// movedQueue names the projects drupal.org says have moved to GitLab, and
	// unknownQueue the ones it will not answer for.
	movedQueue   map[string]bool
	unknownQueue map[string]bool

	listed  []drupal.Issue
	issue   *drupal.Issue
	scanned []string
	reads   int
	user    *drupal.User
}

func (f *fakeDrupalQueue) ProjectIssues(machineName string, _ []drupal.IssueStatus) []drupal.Issue {
	f.scanned = append(f.scanned, machineName)

	return f.listed
}

func (f *fakeDrupalQueue) Issue(int) (drupal.Issue, bool) {
	f.reads++
	if f.issue == nil {
		return drupal.Issue{}, false
	}

	return *f.issue, true
}

func (f *fakeDrupalQueue) User(int) (drupal.User, bool) {
	if f.user == nil {
		return drupal.User{}, false
	}

	return *f.user, true
}

func (f *fakeDrupalQueue) UsesDrupalIssueQueue(machineName string) (bool, bool) {
	if f.unknownQueue[machineName] {
		return false, false
	}

	return !f.movedQueue[machineName], true
}

func (f *fakeDrupalQueue) Warnings() []string { return nil }

// fakeGitlabQueue answers for GitLab without a network.
type fakeGitlabQueue struct {
	issues       []drupal.Issue
	issue        *drupal.Issue
	projectFails *gitlab.Failure
	listFails    *gitlab.Failure
	scanned      []string
	reads        int
}

func (f *fakeGitlabQueue) Project(moduleOrPath string) (*gitlab.Project, *gitlab.Failure) {
	if f.projectFails != nil {
		return nil, f.projectFails
	}

	return &gitlab.Project{ID: 15204, Path: moduleOrPath}, nil
}

func (f *fakeGitlabQueue) OpenIssues(project gitlab.Project) ([]drupal.Issue, *gitlab.Failure) {
	f.scanned = append(f.scanned, project.Path)
	if f.listFails != nil {
		return nil, f.listFails
	}

	return f.issues, nil
}

func (f *fakeGitlabQueue) Issue(gitlab.Project, int) (*drupal.Issue, bool) {
	f.reads++

	return f.issue, f.issue != nil
}

// queuesFor builds a router over the two fakes.
func queuesFor(drupalSide *fakeDrupalQueue, gitlabSide *fakeGitlabQueue) *IssueQueues {
	return &IssueQueues{
		drupalClient: drupalSide,
		gitlabClient: gitlabSide,
		gitlabFor:    func(gitlab.Report) gitlabQueue { return gitlabSide },
		read:         map[string]Queue{},
	}
}

func queuedIssue(nid int, title string) drupal.Issue {
	return drupal.Issue{Nid: nid, Title: title, Status: drupal.StatusActive}
}

// A project that has not moved is read from drupal.org, which is every project
// but a handful.
func TestAProjectThatHasNotMovedIsReadFromDrupalOrg(t *testing.T) {
	drupalSide := &fakeDrupalQueue{listed: []drupal.Issue{queuedIssue(1, "from drupal.org")}}
	gitlabSide := &fakeGitlabQueue{issues: []drupal.Issue{queuedIssue(2, "from GitLab")}}
	queues := queuesFor(drupalSide, gitlabSide)

	found := queues.ProjectIssues("pathauto", drupal.OpenStatuses())

	if len(found) != 1 || found[0].Nid != 1 {
		t.Fatalf("got %+v", found)
	}
	if len(gitlabSide.scanned) != 0 {
		t.Errorf("it read GitLab for a project that has not moved: %v", gitlabSide.scanned)
	}
	if queue, _ := queues.QueueFor("pathauto"); queue != QueueDrupalOrg {
		t.Errorf("recorded %q", queue)
	}
}

// A project whose queue has moved is read from GitLab.
func TestAMovedProjectIsReadFromGitlab(t *testing.T) {
	drupalSide := &fakeDrupalQueue{
		movedQueue: map[string]bool{"helper": true},
		listed:     []drupal.Issue{queuedIssue(1, "stale")},
	}
	gitlabSide := &fakeGitlabQueue{issues: []drupal.Issue{queuedIssue(3619172, "native")}}
	queues := queuesFor(drupalSide, gitlabSide)

	found := queues.ProjectIssues("helper", drupal.OpenStatuses())

	if len(found) != 1 || found[0].Nid != 3619172 {
		t.Fatalf("got %+v", found)
	}
	if len(drupalSide.scanned) != 0 {
		t.Errorf("it read drupal.org for a moved project: %v", drupalSide.scanned)
	}
	if queue, _ := queues.QueueFor("helper"); queue != QueueGitLab {
		t.Errorf("recorded %q", queue)
	}
}

// Not knowing where the issues are means reading drupal.org, which is what
// every release so far has done.
//
// The other reading would scan an empty GitLab queue and report a module as
// having no open issues at all.
func TestNotKnowingFallsBackToDrupalOrg(t *testing.T) {
	drupalSide := &fakeDrupalQueue{
		unknownQueue: map[string]bool{"pathauto": true},
		listed:       []drupal.Issue{queuedIssue(1, "still listed")},
	}
	gitlabSide := &fakeGitlabQueue{}
	queues := queuesFor(drupalSide, gitlabSide)

	found := queues.ProjectIssues("pathauto", drupal.OpenStatuses())

	if len(found) != 1 {
		t.Fatalf("an unanswerable project lost its issues: %+v", found)
	}
	if queue, _ := queues.QueueFor("pathauto"); queue != QueueDrupalOrg {
		t.Errorf("recorded %q", queue)
	}
}

// Each project is routed on its own, because the queue is a property of the
// project and not of the run.
func TestRoutingIsPerProject(t *testing.T) {
	drupalSide := &fakeDrupalQueue{
		movedQueue: map[string]bool{"helper": true},
		listed:     []drupal.Issue{queuedIssue(1, "drupal.org")},
	}
	gitlabSide := &fakeGitlabQueue{issues: []drupal.Issue{queuedIssue(2, "GitLab")}}
	queues := queuesFor(drupalSide, gitlabSide)

	queues.ProjectIssues("helper", drupal.OpenStatuses())
	queues.ProjectIssues("pathauto", drupal.OpenStatuses())

	if strings.Join(gitlabSide.scanned, ",") != "helper" {
		t.Errorf("GitLab scanned %v", gitlabSide.scanned)
	}
	if strings.Join(drupalSide.scanned, ",") != "pathauto" {
		t.Errorf("drupal.org scanned %v", drupalSide.scanned)
	}
}

// A GitLab read is narrowed to the statuses the caller asked for.
func TestAGitlabReadIsNarrowedToTheRequestedStatuses(t *testing.T) {
	drupalSide := &fakeDrupalQueue{movedQueue: map[string]bool{"helper": true}}
	gitlabSide := &fakeGitlabQueue{issues: []drupal.Issue{
		{Nid: 1, Status: drupal.StatusActive},
		{Nid: 2, Status: drupal.StatusNeedsReview},
		{Nid: 3, Status: drupal.StatusRtbc},
	}}
	queues := queuesFor(drupalSide, gitlabSide)

	found := queues.ProjectIssues("helper", drupal.AwaitingReviewStatuses())

	if len(found) != 2 {
		t.Fatalf("got %d issues, want needs-review and RTBC: %+v", len(found), found)
	}
	for _, issue := range found {
		if issue.Status == drupal.StatusActive {
			t.Error("an active issue survived an awaiting-review scan")
		}
	}
}

// A moved project whose GitLab side will not answer is a shortfall that gets
// reported, never a silent fall back to the queue drupal.org has just said is
// not the live one.
//
// Falling back there would list somebody's migrated issues as though they were
// all there were.
func TestAFailedGitlabReadIsReportedNotFalledBack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		side  *fakeGitlabQueue
		match string
	}{
		{
			"the project will not resolve",
			&fakeGitlabQueue{projectFails: &gitlab.Failure{Message: "no such project"}},
			"could not resolve the GitLab project",
		},
		{
			"the issue list will not read",
			&fakeGitlabQueue{listFails: &gitlab.Failure{Message: "slow down"}},
			"could not read",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drupalSide := &fakeDrupalQueue{
				movedQueue: map[string]bool{"helper": true},
				listed:     []drupal.Issue{queuedIssue(1, "must not appear")},
			}
			queues := queuesFor(drupalSide, tc.side)

			found := queues.ProjectIssues("helper", drupal.OpenStatuses())

			if len(found) != 0 {
				t.Errorf("it fell back to drupal.org: %+v", found)
			}
			if len(drupalSide.scanned) != 0 {
				t.Errorf("it scanned drupal.org: %v", drupalSide.scanned)
			}

			warnings := strings.Join(queues.Warnings(), "\n")
			if !strings.Contains(warnings, tc.match) {
				t.Errorf("the shortfall was not reported: %q", warnings)
			}
			if !strings.Contains(warnings, "helper") {
				t.Errorf("the report does not name the module: %q", warnings)
			}
		})
	}
}

// One issue is read from whichever queue is live for that project.
func TestASingleIssueIsRoutedToo(t *testing.T) {
	native := queuedIssue(3619172, "native")
	fromDrupal := queuedIssue(3619171, "migrated")

	drupalSide := &fakeDrupalQueue{
		movedQueue: map[string]bool{"helper": true},
		issue:      &fromDrupal,
	}
	gitlabSide := &fakeGitlabQueue{issue: &native}
	queues := queuesFor(drupalSide, gitlabSide)

	issue, found := queues.Issue("helper", 3619172)
	if !found || issue.Nid != 3619172 {
		t.Fatalf("got %+v, found %t", issue, found)
	}
	if drupalSide.reads != 0 {
		t.Error("it read drupal.org for a moved project")
	}

	if _, found := queues.Issue("pathauto", 3619171); !found {
		t.Error("a project that has not moved lost its issue")
	}
	if drupalSide.reads != 1 || gitlabSide.reads != 1 {
		t.Errorf("drupal reads %d, gitlab reads %d", drupalSide.reads, gitlabSide.reads)
	}
}

// An issue that is not there reports false rather than a blank issue, from
// either queue.
func TestAMissingIssueIsAbsentFromEitherQueue(t *testing.T) {
	for _, moved := range []bool{true, false} {
		drupalSide := &fakeDrupalQueue{movedQueue: map[string]bool{"helper": moved}}
		queues := queuesFor(drupalSide, &fakeGitlabQueue{})

		if issue, found := queues.Issue("helper", 1); found {
			t.Errorf("moved=%t: it found %+v", moved, issue)
		}
	}
}

// A moved project whose GitLab project will not resolve cannot read an issue,
// and does not read drupal.org's copy instead.
func TestASingleIssueDoesNotFallBackEither(t *testing.T) {
	fromDrupal := queuedIssue(3619171, "migrated")
	drupalSide := &fakeDrupalQueue{
		movedQueue: map[string]bool{"helper": true},
		issue:      &fromDrupal,
	}
	gitlabSide := &fakeGitlabQueue{projectFails: &gitlab.Failure{Message: "no"}}
	queues := queuesFor(drupalSide, gitlabSide)

	if _, found := queues.Issue("helper", 3619171); found {
		t.Error("it answered from drupal.org for a moved project")
	}
	if drupalSide.reads != 0 {
		t.Error("it read drupal.org")
	}
}

// The account that posted a patch file is always a drupal.org account.
//
// A uid is a drupal.org id whatever queue the issue came from, and a migrated
// project has no patch files to promote — so this is never routed, and routing
// it would mean inventing a GitLab answer to a drupal.org question.
func TestTheAttributionAccountIsNeverRouted(t *testing.T) {
	account := drupal.User{UID: 7, Name: "somebody"}
	drupalSide := &fakeDrupalQueue{
		movedQueue: map[string]bool{"helper": true},
		user:       &account,
	}
	queues := queuesFor(drupalSide, &fakeGitlabQueue{})

	user, found := queues.User(7)

	if !found || user.Name != "somebody" {
		t.Errorf("got %+v, found %t", user, found)
	}
}

// Nothing has been read yet, so nothing is claimed about where from.
func TestTheQueueIsUnknownBeforeAnythingIsRead(t *testing.T) {
	queues := queuesFor(&fakeDrupalQueue{}, &fakeGitlabQueue{})

	if queue, decided := queues.QueueFor("pathauto"); decided {
		t.Errorf("it claimed %q before reading anything", queue)
	}
}

// Both queues' shortfalls are reported, because a scan that read from each
// can fall short in either.
func TestWarningsCarryBothSides(t *testing.T) {
	queues := queuesFor(&fakeDrupalQueue{}, &fakeGitlabQueue{})
	queues.note("a GitLab shortfall")

	if warnings := strings.Join(queues.Warnings(), "\n"); !strings.Contains(warnings, "a GitLab shortfall") {
		t.Errorf("got %q", warnings)
	}
}

// The production source hands out a reader and resolves its GitLab client only
// when a project that has moved needs one.
func TestTheGitlabClientIsResolvedLazily(t *testing.T) {
	resolved := 0
	queues := &IssueQueues{
		drupalClient: &fakeDrupalQueue{movedQueue: map[string]bool{"helper": true}},
		gitlabFor: func(gitlab.Report) gitlabQueue {
			resolved++

			return &fakeGitlabQueue{}
		},
		read: map[string]Queue{},
	}

	queues.ProjectIssues("pathauto", drupal.OpenStatuses())
	if resolved != 0 {
		t.Error("it built a GitLab client for a project that has not moved")
	}

	queues.ProjectIssues("helper", drupal.OpenStatuses())
	queues.ProjectIssues("helper", drupal.OpenStatuses())
	if resolved != 1 {
		t.Errorf("it built %d clients", resolved)
	}
}

// `upkeep issues` says when it read GitLab, because it used to say "from
// drupal.org" unconditionally — a claim rather than a report.
func TestIssuesSaysWhenItReadGitlab(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	queues := queuesFor(
		&fakeDrupalQueue{movedQueue: map[string]bool{"pathauto": true}},
		&fakeGitlabQueue{issues: []drupal.Issue{queuedIssue(3619172, "a native issue")}},
	)

	code, _, stderr := runIssuesCommand(t, queues,
		scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, want := range []string{"pathauto", "GitLab", "not on drupal.org"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("%q is missing from the report: %q", want, stderr)
		}
	}
}

// And it stays silent for a project that has not moved, which is almost all of
// them: a line confirming the status quo on every run is a line people stop
// reading, and this one exists to be noticed.
func TestIssuesSaysNothingForAProjectThatHasNotMoved(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	queues := queuesFor(
		&fakeDrupalQueue{listed: []drupal.Issue{queuedIssue(1, "an ordinary issue")}},
		&fakeGitlabQueue{},
	)

	code, _, stderr := runIssuesCommand(t, queues,
		scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if strings.Contains(stderr, "GitLab, not on drupal.org") {
		t.Errorf("it announced a move that did not happen: %q", stderr)
	}
}

// The progress line no longer asserts where the issues came from, because it
// is printed before anything knows.
func TestTheScanLineDoesNotClaimAQueue(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	queues := queuesFor(
		&fakeDrupalQueue{movedQueue: map[string]bool{"pathauto": true}},
		&fakeGitlabQueue{},
	)

	_, _, stderr := runIssuesCommand(t, queues,
		scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--cockpit="+root)

	if strings.Contains(stderr, "issues from drupal.org") {
		t.Errorf("the scan line claimed drupal.org while reading GitLab: %q", stderr)
	}
}

// The GitLab client's own diagnostics are not issue-scan shortfalls.
//
// The one it issues is the cost of reading anonymously, which the ordinary
// reading path already announces once for the merge requests the same command
// reads. Collecting it here printed that paragraph twice and filed it under
// "requests that did not answer", which it is not. Caught running against the
// real `helper`, not by the suite, which is why it is pinned.
func TestTheGitlabClientsOwnNoticeIsNotAScanShortfall(t *testing.T) {
	queues := &IssueQueues{
		drupalClient: &fakeDrupalQueue{movedQueue: map[string]bool{"helper": true}},
		gitlabFor: func(report gitlab.Report) gitlabQueue {
			report("No GitLab token configured, so this is reading anonymously ...")

			return &fakeGitlabQueue{}
		},
		read: map[string]Queue{},
	}

	queues.ProjectIssues("helper", drupal.OpenStatuses())

	for _, warning := range queues.Warnings() {
		if strings.Contains(warning, "token configured") {
			t.Errorf("the anonymous-read notice was reported as a shortfall: %q", warning)
		}
	}
}

// A read that genuinely falls short still reports, so the fix above did not
// silence the thing the notes exist for.
func TestARealShortfallStillReportsAfterThat(t *testing.T) {
	queues := &IssueQueues{
		drupalClient: &fakeDrupalQueue{movedQueue: map[string]bool{"helper": true}},
		gitlabFor: func(report gitlab.Report) gitlabQueue {
			report("an ignored notice")

			return &fakeGitlabQueue{listFails: &gitlab.Failure{Message: "slow down"}}
		},
		read: map[string]Queue{},
	}

	queues.ProjectIssues("helper", drupal.OpenStatuses())

	if warnings := strings.Join(queues.Warnings(), "\n"); !strings.Contains(warnings, "slow down") {
		t.Errorf("a real shortfall went unreported: %q", warnings)
	}
}

// A reader that has not decided yet is read as drupal.org, not as GitLab.
//
// Unreachable through `publish`, which asks only after requireIssue has
// recorded a queue — so it is pinned here rather than through a command. It
// guards the direction a wrong answer would break: routing to the canonical
// project on a reader that has said nothing would push a drupal.org project's
// branch somewhere it does not belong.
func TestAnUndecidedReaderIsNotReadAsGitlab(t *testing.T) {
	queues := queuesFor(
		&fakeDrupalQueue{movedQueue: map[string]bool{"helper": true}},
		&fakeGitlabQueue{},
	)

	// Nothing read yet, so nothing decided — even though helper *has* moved.
	if readFromGitLab(queues.Issues(), "helper") {
		t.Error("an undecided reader was read as GitLab")
	}

	queues.ProjectIssues("helper", drupal.OpenStatuses())

	if !readFromGitLab(queues.Issues(), "helper") {
		t.Error("a decided GitLab reader was not read as GitLab")
	}
}

// And a source that cannot say at all is read as drupal.org, which is the
// behaviour every release so far had.
func TestASourceThatCannotSayIsReadAsDrupalOrg(t *testing.T) {
	if readFromGitLab(noIssues{}, "helper") {
		t.Error("a source with no opinion was read as GitLab")
	}
}

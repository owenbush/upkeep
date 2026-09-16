package command

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
	"github.com/owenbush/upkeep/internal/workflow"
)

// The two SHAs a merge request has, and the whole point of this command's
// lookup: the head is the contributor's branch, the merge ref is that branch
// merged into the current tip of its target — which is the tree CI analyses
// and the tree `check` files its evidence under.
const (
	nwHeadSHA     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	nwMergeRefSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	nwOlderSHA    = "cccccccccccccccccccccccccccccccccccccccc"
)

// notingGitlab answers the merge-request resolution and records what was
// posted.
type notingGitlab struct {
	server *httptest.Server

	mu     sync.Mutex
	posted []string

	mergeRefSHA string
	headSHA     string
	postStatus  int
}

func aNotingGitlab(t *testing.T) *notingGitlab {
	t.Helper()

	scene := &notingGitlab{
		mergeRefSHA: nwMergeRefSHA, headSHA: nwHeadSHA, postStatus: http.StatusCreated,
	}

	scene.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.EscapedPath()

		switch {
		case strings.HasSuffix(path, "/notes") && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			scene.mu.Lock()
			text, _ := body["body"].(string)
			scene.posted = append(scene.posted, text)
			scene.mu.Unlock()
			w.WriteHeader(scene.postStatus)
			_, _ = w.Write([]byte(`{"id":1}`))

		case strings.HasSuffix(path, "/merge_ref"):
			if scene.mergeRefSHA == "" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"404"}`))

				return
			}
			_, _ = w.Write([]byte(`{"commit_id":"` + scene.mergeRefSHA + `"}`))

		case strings.HasSuffix(path, "/projects/project%2Fpathauto"):
			_, _ = w.Write([]byte(
				`{"id":1,"path":"pathauto","path_with_namespace":"project/pathauto",` +
					`"web_url":"https://git.drupalcode.org/project/pathauto"}`))

		case strings.HasSuffix(path, "/merge_requests/12"):
			_, _ = w.Write([]byte(
				`{"iid":12,"title":"Issue #3300001: Fix the cache","state":"opened",` +
					`"source_branch":"3300001-fix-the-cache","target_branch":"2.0.x",` +
					`"description":"","sha":"` + scene.headSHA + `",` +
					`"web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/12"}`))

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))
		}
	}))
	t.Cleanup(scene.server.Close)

	return scene
}

// client is authenticated: this command writes, and an anonymous client
// refuses in the client before any request.
func (s *notingGitlab) client() *gitlab.Client {
	return gitlab.NewClient(nil, "a-token-long-enough", s.server.URL+"/api/v4", s.server.URL)
}

func (s *notingGitlab) comment(t *testing.T) string {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.posted) != 1 {
		t.Fatalf("posted %d comments, want 1", len(s.posted))
	}

	return s.posted[0]
}

// storeEvidence files a check result under a revision, exactly as `check`
// does.
func storeEvidence(t *testing.T, root, revision string, recordedAt time.Time, checks ...check.Result) {
	t.Helper()

	where, _ := cockpit.New(root)
	key, err := results.MergeRequestKey(12)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if err := results.NewCache(where.ResultsPath()).Store(
		"pathauto", key, "11", revision, check.RunResult{Results: checks}, recordedAt,
	); err != nil {
		t.Fatalf("store: %v", err)
	}
}

// aFailedRun is the evidence this command exists to publish.
func aFailedRun() []check.Result {
	zero, one := 0, 1

	return []check.Result{
		{
			Type: check.PhpCs, Status: check.Passed, ExitCode: &zero,
			// Output on a *passing* check, so quoting it would be visible: a
			// clean phpcs run still prints, and none of it belongs on a public
			// merge request.
			Output:   "..... 5 / 5 (100%)",
			Duration: 3200 * time.Millisecond,
		},
		{
			Type: check.PhpUnit, Status: check.Failed, ExitCode: &one,
			Output:   "There was 1 failure:\n1) Drupal\\Tests\\pathauto\\AliasTest::testLongTitle",
			Duration: 42 * time.Second,
		},
	}
}

// runNeedsWorkCommand invokes the tree against a scripted GitLab and browser.
func runNeedsWorkCommand(
	t *testing.T, scene *notingGitlab, browser cli.Browser, args ...string,
) (int, string, string) {
	t.Helper()

	clients := cli.GitlabClients(scriptedClients{client: scene.client()})

	return runIssueLoop(t, aWorkingEngine(), noIssues{}, clients, browser, args...)
}

// Evidence is filed under the merge ref, so it is looked up there: the head
// SHA is the contributor's branch, which is not what was checked.
func TestNeedsWorkFindsEvidenceByTheMergeRef(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)
	storeEvidence(t, root, nwMergeRefSHA, time.Now(), aFailedRun()...)

	code, _, stderr := runNeedsWorkCommand(t, scene, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--no-open", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// Nothing is stale. The PHP looks this up by the head SHA, misses, falls
	// back to the newest result and then calls it stale because its SHA is not
	// the head SHA — which it never was.
	if strings.Contains(stderr, "recorded against") {
		t.Errorf("a current result was reported as stale: %q", stderr)
	}

	comment := scene.comment(t)
	// And the comment names the ref for what it is: a reader who goes looking
	// for this SHA in the branch will not find it.
	if !strings.Contains(comment, "merge ref: `bbbbbbbb`") {
		t.Errorf("the comment does not name the revision correctly:\n%s", comment)
	}
}

// Where GitLab publishes no merge ref — the merge request conflicts with its
// target — the head SHA is the revision, and the comment says SHA.
func TestNeedsWorkFallsBackToTheHeadSha(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)
	scene.mergeRefSHA = ""
	storeEvidence(t, root, nwHeadSHA, time.Now(), aFailedRun()...)

	code, _, stderr := runNeedsWorkCommand(t, scene, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--no-open", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if strings.Contains(stderr, "recorded against") {
		t.Errorf("a current result was reported as stale: %q", stderr)
	}
	if comment := scene.comment(t); !strings.Contains(comment, "SHA: `aaaaaaaa`") {
		t.Errorf("the comment does not name the head SHA:\n%s", comment)
	}
}

// Evidence about a tree that has since moved is still published — a
// maintainer who ran the checks before the last push has evidence — but the
// run says so.
func TestNeedsWorkPublishesOlderEvidenceAndSaysSo(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)
	storeEvidence(t, root, nwOlderSHA, time.Now().Add(-time.Hour), aFailedRun()...)

	code, _, stderr := runNeedsWorkCommand(t, scene, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--no-open", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "cccccccc") || !strings.Contains(stderr, "bbbbbbbb") {
		t.Errorf("the warning does not name both revisions: %q", stderr)
	}
	if comment := scene.comment(t); !strings.Contains(comment, "cccccccc") {
		t.Errorf("the comment does not carry the revision it is about:\n%s", comment)
	}
}

// No evidence at all is a refusal naming the command that produces it, and
// nothing is posted: a comment saying nothing was checked is worse than none.
func TestNeedsWorkRefusesWithNothingToPublish(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)

	code, _, stderr := runNeedsWorkCommand(t, scene, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "upkeep check pathauto 12 --version=11") {
		t.Errorf("the refusal does not name the command that records results: %q", stderr)
	}
	if len(scene.posted) != 0 {
		t.Errorf("it posted %v with nothing to publish", scene.posted)
	}
}

// The comment is what somebody who did not run the checks reads: a table of
// outcomes, the failure's output folded away, and no pretence about what did
// not run.
func TestTheCommentSaysWhatHappened(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)
	checks := append(aFailedRun(),
		check.Result{Type: check.PhpStan, Status: check.NoTests},
		check.Result{Type: check.Deprecation, Status: check.Unavailable},
	)
	storeEvidence(t, root, nwMergeRefSHA, time.Now(), checks...)

	code, _, stderr := runNeedsWorkCommand(t, scene, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--no-open", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	comment := scene.comment(t)
	for _, expected := range []string{
		"### upkeep local check results",
		"Core: 11",
		"| Check | Status | Duration |",
		"| phpcs | pass | 3.2s |",
		"| phpunit | **FAIL** | 42.0s |",
		// Neither of these is evidence the module works, so neither is folded
		// into a pass.
		"| phpstan | no tests | — |",
		"| deprecation | unavailable | — |",
		// The failure's own output, behind a fold so a long run does not bury
		// the summary.
		"<details><summary>phpunit output</summary>",
		"AliasTest::testLongTitle",
		"*Posted via [upkeep](https://github.com/owenbush/upkeep)*",
	} {
		if !strings.Contains(comment, expected) {
			t.Errorf("the comment is missing %q:\n%s", expected, comment)
		}
	}
	// Only the failure's output is quoted: a passing check's output is noise
	// on a public merge request.
	if strings.Contains(comment, "phpcs output") || strings.Contains(comment, "5 / 5") {
		t.Errorf("a passing check's output was quoted:\n%s", comment)
	}
}

// A failure with nothing to quote gets no empty fold.
func TestTheCommentOmitsAnEmptyFold(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)
	one := 1
	storeEvidence(t, root, nwMergeRefSHA, time.Now(),
		check.Result{Type: check.PhpUnit, Status: check.Failed, ExitCode: &one})

	runNeedsWorkCommand(t, scene, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--no-open", "--cockpit="+root)

	if comment := scene.comment(t); strings.Contains(comment, "<details>") {
		t.Errorf("an empty fold was written:\n%s", comment)
	}
}

// --dry-run prints the comment and posts nothing: this writes to a public
// merge request, so seeing it first has to be possible.
func TestNeedsWorkDryRunPostsNothing(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)
	storeEvidence(t, root, nwMergeRefSHA, time.Now(), aFailedRun()...)

	code, stdout, stderr := runNeedsWorkCommand(t, scene, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--dry-run", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "upkeep local check results") {
		t.Errorf("the preview was not printed:\n%s", stdout)
	}
	if len(scene.posted) != 0 {
		t.Errorf("--dry-run posted %v", scene.posted)
	}
}

// The issue is opened after the comment lands, because that is where the
// status change happens and the API cannot make it.
func TestNeedsWorkOpensTheIssueForTheStatusChange(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)
	storeEvidence(t, root, nwMergeRefSHA, time.Now(), aFailedRun()...)
	browser := &recordingBrowser{}

	code, _, stderr := runNeedsWorkCommand(t, scene, browser,
		"needs-work", "pathauto", "12", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(browser.opened) != 1 || !strings.Contains(browser.opened[0], "3300001") {
		t.Errorf("opened %v", browser.opened)
	}
	if !strings.Contains(stderr, "Needs work") {
		t.Errorf("it does not say what to do there: %q", stderr)
	}

	// A browser that will not open says where to go instead.
	refusing := &recordingBrowser{refuse: true}
	_, _, stderr = runNeedsWorkCommand(t, aNotingGitlab(t), refusing,
		"needs-work", "pathauto", "12", "--cockpit="+root)
	if !strings.Contains(stderr, "drupal.org/node/3300001") {
		t.Errorf("it does not print the issue URL: %q", stderr)
	}
}

// --no-open is for a terminal with no browser behind it.
func TestNeedsWorkOpensNothingWhenToldNotTo(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)
	storeEvidence(t, root, nwMergeRefSHA, time.Now(), aFailedRun()...)
	browser := &recordingBrowser{}

	runNeedsWorkCommand(t, scene, browser,
		"needs-work", "pathauto", "12", "--no-open", "--cockpit="+root)

	if len(browser.opened) != 0 {
		t.Errorf("it opened %v", browser.opened)
	}
}

// A post that GitLab refuses is a failure with its own words, not a success
// nobody can see.
func TestNeedsWorkReportsARefusedComment(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)
	scene.postStatus = http.StatusForbidden
	storeEvidence(t, root, nwMergeRefSHA, time.Now(), aFailedRun()...)
	browser := &recordingBrowser{}

	code, _, stderr := runNeedsWorkCommand(t, scene, browser,
		"needs-work", "pathauto", "12", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "could not post comment on !12") {
		t.Errorf("stderr: %q", stderr)
	}
	// And the issue is not opened: nothing has been said on the merge request,
	// so there is nothing to change a status because of.
	if len(browser.opened) != 0 {
		t.Errorf("it opened %v after failing to post", browser.opened)
	}
}

// It writes, so it takes the authenticated path — and refuses before any
// request rather than after resolving a merge request it cannot comment on.
func TestNeedsWorkRefusesWithoutACredential(t *testing.T) {
	root := aDashboardCockpit(t)

	code, _, stderr := runIssueLoop(t, aWorkingEngine(), noIssues{},
		scriptedClients{}, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "token") {
		t.Errorf("stderr: %q", stderr)
	}
}

// Every status the cache will hand over has a word in the comment: a blank
// cell on a public merge request says nothing about a check that ran.
func TestEveryStatusHasAWordInTheComment(t *testing.T) {
	for _, status := range check.Statuses() {
		if commentStatuses[status] == "" {
			t.Errorf("%q has no word in the comment", status)
		}
	}
	// And only a failure is emphasised, because that is what the comment
	// exists to report.
	for status, word := range commentStatuses {
		if strings.Contains(word, "**") != (status == check.Failed) {
			t.Errorf("%q is spelled %q", status, word)
		}
	}
}

// A results directory that cannot be read is an infrastructure failure, not an
// empty cache: publishing "no results" over an unreadable disk would say the
// checks were never run.
func TestNeedsWorkReportsAnUnreadableResultsCache(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)
	storeEvidence(t, root, nwOlderSHA, time.Now(), aFailedRun()...)

	where, _ := cockpit.New(root)
	if err := os.Chmod(where.ResultsPath(), 0o000); err != nil {
		t.Skipf("cannot make the directory unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(where.ResultsPath(), 0o700) })
	if _, err := os.ReadDir(where.ResultsPath()); err == nil {
		t.Skip("the directory is still readable; probably running as root")
	}

	code, _, stderr := runNeedsWorkCommand(t, scene, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--no-open", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	// Not "no cached check results": the cache is not empty, it could not be
	// read, and telling a maintainer to run the checks they already ran sends
	// them to do the work twice.
	if strings.Contains(stderr, "no cached check results") {
		t.Errorf("an unreadable cache was reported as an empty one: %q", stderr)
	}
	if stderr == "" {
		t.Error("it failed silently")
	}
	if len(scene.posted) != 0 {
		t.Errorf("it posted %v over an unreadable cache", scene.posted)
	}
}

// A merge request with neither a merge ref nor a head SHA has no revision to
// compare against, so nothing is called stale: a warning naming an empty SHA
// would be noise about a comparison that was never made.
func TestNeedsWorkSaysNothingAboutStalenessWithNoRevisionToCompare(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aNotingGitlab(t)
	scene.mergeRefSHA = ""
	scene.headSHA = ""
	storeEvidence(t, root, nwOlderSHA, time.Now(), aFailedRun()...)

	code, _, stderr := runNeedsWorkCommand(t, scene, cli.NoBrowser{},
		"needs-work", "pathauto", "12", "--no-open", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if strings.Contains(stderr, "recorded against") {
		t.Errorf("it compared against a revision it does not have: %q", stderr)
	}
	// The evidence is still published — it is the only evidence there is.
	if comment := scene.comment(t); !strings.Contains(comment, "cccccccc") {
		t.Errorf("comment:\n%s", comment)
	}
}

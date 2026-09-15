package command

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
	"github.com/owenbush/upkeep/internal/workflow"
)

// checkingEngine is an engine that records the flow and answers with a
// scripted check run.
type checkingEngine struct {
	fakeEngine

	applied    []int
	fixtures   []string
	run        check.RunResult
	runErr     error
	applyErr   error
	fixtureErr error
	served     adapter.ServeResult
	serveErr   error
	status     adapter.WorkingCopyStatus
	statusOK   bool
	checksRun  int
}

func (e *checkingEngine) ApplyMr(_ adapter.Environment, mergeRequest gitlab.MergeRequest) error {
	e.applied = append(e.applied, mergeRequest.IID)

	return e.applyErr
}

func (e *checkingEngine) LoadFixture(_ adapter.Environment, name string) error {
	e.fixtures = append(e.fixtures, name)

	return e.fixtureErr
}

func (e *checkingEngine) RunChecks(adapter.Environment, []check.Type) (check.RunResult, error) {
	e.checksRun++

	return e.run, e.runErr
}

func (e *checkingEngine) Serve(adapter.Environment) (adapter.ServeResult, error) {
	return e.served, e.serveErr
}

func (e *checkingEngine) InspectWorkingCopy(string, string) (adapter.WorkingCopyStatus, bool) {
	return e.status, e.statusOK
}

// aCheckingEngine is one wired for a healthy merge-request run.
func aCheckingEngine() *checkingEngine {
	zero := 0

	return &checkingEngine{
		fakeEngine: fakeEngine{environment: adapter.Environment{
			ModuleName: "pathauto", CoreMajor: "11",
			ProjectName: "upkeep-pathauto-d11", ProjectPath: "/projects/upkeep-pathauto-d11",
			PrimaryURL: "https://upkeep-pathauto-d11.ddev.site",
		}},
		run: check.RunResult{Results: []check.Result{
			{Type: check.PhpCs, Status: check.Passed, ExitCode: &zero, Duration: 3 * time.Second},
			{Type: check.PhpUnit, Status: check.Passed, ExitCode: &zero, Duration: 42 * time.Second},
		}},
		served: adapter.ServeResult{
			URL:      "https://upkeep-pathauto-d11.ddev.site",
			LoginURL: "https://upkeep-pathauto-d11.ddev.site/user/reset/1/x",
		},
	}
}

// scriptedClients hands back a client talking to a stub GitLab, and records
// which path the command asked for.
//
// The distinction matters: a command that only looks must take the read-only
// path, which works with no token at all. Taking the authenticated path would
// make check and review refuse on a public project for want of a credential
// GitLab does not ask for.
type scriptedClients struct {
	client *gitlab.Client
	asked  *[]string
}

func (c scriptedClients) ReadOnly(gitlab.Report) *gitlab.Client {
	if c.asked != nil {
		*c.asked = append(*c.asked, "read-only")
	}

	return c.client
}

func (c scriptedClients) Authenticated(gitlab.Report) (*gitlab.Client, error) {
	if c.asked != nil {
		*c.asked = append(*c.asked, "authenticated")
	}

	return c.client, nil
}

// runCheckCommand invokes the tree against scripted GitLab and engine.
func runCheckCommand(
	t *testing.T, engine adapter.Engine, client *gitlab.Client, args ...string,
) (int, string, string) {
	t.Helper()

	code, stdout, stderr, _ := runCheckRecording(t, engine, client, args...)

	return code, stdout, stderr
}

// runCheckRecording additionally reports which GitLab path was asked for.
func runCheckRecording(
	t *testing.T, engine adapter.Engine, client *gitlab.Client, args ...string,
) (int, string, string, []string) {
	t.Helper()

	asked := []string{}
	root := NewRoot(
		&fakeFactory{engine: &fakeEngine{}, replace: engine},
		scriptedClients{client: client, asked: &asked}, noVolumes{}, noSizer,
	)
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)

	code := cli.Execute(root, errOut)

	return code, out.String(), errOut.String(), asked
}

// aResolvableGitlab answers the two requests a merge-request resolution makes.
func aResolvableGitlab(t *testing.T, extra map[string]string) (*gitlab.Client, func()) {
	t.Helper()

	answers := map[string]string{
		"/api/v4/projects/project%2Fpathauto": `{"id":1,"path":"pathauto",` +
			`"path_with_namespace":"project/pathauto","web_url":"https://git.drupalcode.org/project/pathauto"}`,
		"/api/v4/projects/1/merge_requests/12": `{"iid":12,"title":"Fix it","state":"opened",` +
			`"source_branch":"fix","target_branch":"2.0.x",` +
			`"sha":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"}`,
	}
	for path, body := range extra {
		answers[path] = body
	}
	server, client := aGitlabStub(t, answers)

	return client, server.Close
}

// Console can express "required" and "optional" but not "exactly one of
// these two", so both refusals are the command's — guessing either way would
// run a different check than was asked for.
func TestCheckRefusesBothModesAndNeither(t *testing.T) {
	root := anEnvironmentCockpit(t)

	code, _, stderr := runCheckCommand(t, aCheckingEngine(), nil,
		"check", "pathauto", "--cockpit="+root)
	if code != workflow.Infrastructure {
		t.Errorf("exit %d with neither mode", code)
	}
	if !strings.Contains(stderr, "--working-copy") {
		t.Errorf("the refusal does not offer the other mode: %q", stderr)
	}

	code, _, stderr = runCheckCommand(t, aCheckingEngine(), nil,
		"check", "pathauto", "12", "--working-copy", "--cockpit="+root)
	if code != workflow.Infrastructure {
		t.Errorf("exit %d with both modes", code)
	}
	if !strings.Contains(stderr, "not both") {
		t.Errorf("the refusal does not say why: %q", stderr)
	}
}

// The working-copy mode touches neither GitLab nor drupal.org: "is what I have
// in front of me green?" is a question a maintainer has every right to ask
// offline, on a branch no remote has heard of.
func TestTheWorkingCopyModeNeedsNoNetworkAndCachesNothing(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = true
	engine.status = adapter.WorkingCopyStatus{CurrentBranch: "3601234-fix-the-thing"}

	// A nil client: reaching for one at all would panic, which is the
	// assertion.
	code, stdout, stderr := runCheckCommand(t, engine, nil,
		"check", "pathauto", "--working-copy", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(engine.applied) != 0 {
		t.Errorf("it applied a merge request: %v", engine.applied)
	}
	if !strings.Contains(stdout, "Nothing was cached") {
		t.Errorf("it did not say it cached nothing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "3601234-fix-the-thing") {
		t.Errorf("it did not name the branch it checked:\n%s", stdout)
	}

	where, _ := cockpit.New(root)
	if entries, _ := os.ReadDir(where.ResultsPath()); len(entries) != 0 {
		t.Errorf("a working-copy run left cached evidence: %v", entries)
	}
}

// A dirty tree is checkable but not reportable: the run describes a state that
// exists only in that moment, and the first thing anybody does with a green
// result is act on it.
func TestADirtyWorkingCopyIsCheckedAndSaidSo(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = true
	engine.status = adapter.WorkingCopyStatus{
		CurrentBranch: "2.0.x", HasUnstagedChanges: true,
	}

	code, _, stderr := runCheckCommand(t, engine, nil,
		"check", "pathauto", "--working-copy", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr, "Uncommitted changes are included") {
		t.Errorf("it checked a dirty tree silently: %q", stderr)
	}
	if !strings.Contains(stderr, "Unstaged changes") {
		t.Errorf("it did not say what was uncommitted: %q", stderr)
	}
}

// A working copy that cannot be read has nothing to check, and the refusal
// says how to make one.
func TestAWorkingCopyThatCannotBeReadIsARefusal(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = false

	code, _, stderr := runCheckCommand(t, engine, nil,
		"check", "pathauto", "--working-copy", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "upkeep start") {
		t.Errorf("the refusal does not say how to start: %q", stderr)
	}
}

// A red suite is exit 1: upkeep worked, the work it supervised failed.
func TestAFailedCheckIsExitOneNotTwo(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = true
	one := 1
	engine.run = check.RunResult{Results: []check.Result{
		{Type: check.PhpCs, Status: check.Failed, ExitCode: &one,
			Output: "FILE: src/Thing.php\n  12 | ERROR | Missing doc comment"},
	}}

	code, stdout, _ := runCheckCommand(t, engine, nil,
		"check", "pathauto", "--working-copy", "--cockpit="+root)

	if code != workflow.Failed {
		t.Errorf("exit %d", code)
	}
	// The output of what failed, because a table saying "phpcs FAILED" and
	// nothing else sends somebody to run it again by hand.
	if !strings.Contains(stdout, "Missing doc comment") {
		t.Errorf("the failing output was dropped:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 check(s) failed") {
		t.Errorf("no summary:\n%s", stdout)
	}
}

// An honest non-failure is not a failure: an unavailable check and a suite
// with no tests both exit 0.
func TestHonestNonFailuresAreGreen(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = true
	engine.run = check.RunResult{Results: []check.Result{
		check.NotAvailable(check.Deprecation, "the pinned add-on ships no command for it"),
		{Type: check.PhpUnit, Status: check.NoTests},
	}}

	code, stdout, _ := runCheckCommand(t, engine, nil,
		"check", "pathauto", "--working-copy", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stdout, "All checks green") {
		t.Errorf("an honest non-failure read as red:\n%s", stdout)
	}
	// And they are still shown, rather than omitted: a suite that silently
	// skipped one would report green over something that never ran.
	for _, expected := range []string{string(check.Deprecation), string(check.Unavailable)} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("%q was not listed:\n%s", expected, stdout)
		}
	}
}

// A fixture is loaded before any check runs: a green suite over the wrong
// database is worse than no suite at all.
func TestAFixtureIsLoadedBeforeTheChecks(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = true

	code, _, _ := runCheckCommand(t, engine, nil,
		"check", "pathauto", "--working-copy", "--fixture=sample-content", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if len(engine.fixtures) != 1 || engine.fixtures[0] != "sample-content" {
		t.Errorf("fixtures %v", engine.fixtures)
	}
}

// A fixture that will not load stops the run: the checks would otherwise
// report on a database nobody asked for.
func TestAFixtureThatWillNotLoadStopsTheRun(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = true
	engine.fixtureErr = errors.New("no such fixture")

	code, stdout, _ := runCheckCommand(t, engine, nil,
		"check", "pathauto", "--working-copy", "--fixture=nope", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if strings.Contains(stdout, "All checks green") {
		t.Errorf("it reported a verdict over a fixture that never loaded:\n%s", stdout)
	}
}

// Without a fixture flag, nothing is loaded — the database is whatever the
// environment was seeded with.
func TestNoFixtureFlagLoadsNothing(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = true

	runCheckCommand(t, engine, nil, "check", "pathauto", "--working-copy", "--cockpit="+root)

	if len(engine.fixtures) != 0 {
		t.Errorf("it loaded %v unasked", engine.fixtures)
	}
}

// The verdict goes into the cache keyed on the merge ref's SHA: that is the
// tree that was checked, and it moves when either side does.
func TestAMergeRequestVerdictIsCachedOnTheMergeRef(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()

	server, client := aGitlabStub(t, map[string]string{
		"/api/v4/projects/project%2Fpathauto": `{"id":1,"path":"pathauto",` +
			`"path_with_namespace":"project/pathauto","web_url":"https://git.drupalcode.org/project/pathauto"}`,
		"/api/v4/projects/1/merge_requests/12": `{"iid":12,"title":"Automated Project Update Bot fixes",` +
			`"state":"opened","source_branch":"project-update-bot-only","target_branch":"2.0.x",` +
			`"sha":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"}`,
		"/api/v4/projects/1/merge_requests/12/merge_ref": `{"commit_id":"0f1e2d3c4b5a0f1e2d3c4b5a0f1e2d3c4b5a0f1e"}`,
	})
	defer server.Close()

	code, _, stderr := runCheckCommand(t, engine, client,
		"check", "pathauto", "12", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(engine.applied) != 1 || engine.applied[0] != 12 {
		t.Errorf("applied %v", engine.applied)
	}

	where, _ := cockpit.New(root)
	key, _ := results.MergeRequestKey(12)
	found := results.NewCache(where.ResultsPath()).Find(
		"pathauto", key, "11", "0f1e2d3c4b5a0f1e2d3c4b5a0f1e2d3c4b5a0f1e")
	if found == nil {
		t.Fatalf("the verdict was not cached on the merge ref:\n%s", stderr)
	}
	// And not on the head SHA, which would still read as current after the
	// target gained a commit.
	if results.NewCache(where.ResultsPath()).Find(
		"pathauto", key, "11", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2") != nil {
		t.Error("the verdict was cached on the branch rather than the merge")
	}
}

// No merge ref means the head SHA is the revision: GitLab computes no merge
// ref for a merge request that conflicts with its target, and the adapter
// falls back to the branch there too, so both halves fall back together.
func TestWithNoMergeRefTheHeadShaIsTheRevision(t *testing.T) {
	root := anEnvironmentCockpit(t)

	server, client := aGitlabStub(t, map[string]string{
		"/api/v4/projects/project%2Fpathauto": `{"id":1,"path":"pathauto",` +
			`"path_with_namespace":"project/pathauto","web_url":"https://git.drupalcode.org/project/pathauto"}`,
		"/api/v4/projects/1/merge_requests/12": `{"iid":12,"title":"Fix it","state":"opened",` +
			`"source_branch":"fix","target_branch":"2.0.x",` +
			`"sha":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"}`,
	})
	defer server.Close()

	code, _, stderr := runCheckCommand(t, aCheckingEngine(), client,
		"check", "pathauto", "12", "--cockpit="+root)
	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	where, _ := cockpit.New(root)
	key, _ := results.MergeRequestKey(12)
	if results.NewCache(where.ResultsPath()).Find(
		"pathauto", key, "11", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2") == nil {
		t.Error("with no merge ref, the head SHA was not used")
	}
}

// No revision at all means no entry: one keyed on a guess is one staleness
// checks could never match, which is worse than no evidence.
func TestNoRevisionMeansNoCachedEvidence(t *testing.T) {
	root := anEnvironmentCockpit(t)

	server, client := aGitlabStub(t, map[string]string{
		"/api/v4/projects/project%2Fpathauto": `{"id":1,"path":"pathauto",` +
			`"path_with_namespace":"project/pathauto","web_url":"https://git.drupalcode.org/project/pathauto"}`,
		"/api/v4/projects/1/merge_requests/12": `{"iid":12,"title":"Fix it","state":"opened",` +
			`"source_branch":"fix","target_branch":"2.0.x"}`,
	})
	defer server.Close()

	code, _, stderr := runCheckCommand(t, aCheckingEngine(), client,
		"check", "pathauto", "12", "--cockpit="+root)

	// The run still happened and still has a verdict; only the caching is
	// skipped.
	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// The specific reason, not merely "NOT cached": an entry rejected further
	// down for an unusable key says that too, and the two are different bugs.
	if !strings.Contains(stderr, "no merge-ref or head SHA") {
		t.Errorf("it did not say why nothing was cached: %q", stderr)
	}

	where, _ := cockpit.New(root)
	if entries, _ := os.ReadDir(filepath.Join(where.ResultsPath(), "pathauto")); len(entries) != 0 {
		t.Errorf("something was cached with no revision: %v", entries)
	}
}

// review puts the change in front of a human and runs no checks, so it never
// returns the code that means a check failed.
func TestReviewServesAndNeverReturnsACheckFailure(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	one := 1
	// Even with a red suite available, review must not run it.
	engine.run = check.RunResult{Results: []check.Result{
		{Type: check.PhpCs, Status: check.Failed, ExitCode: &one},
	}}

	server, client := aGitlabStub(t, map[string]string{
		"/api/v4/projects/project%2Fpathauto": `{"id":1,"path":"pathauto",` +
			`"path_with_namespace":"project/pathauto","web_url":"https://git.drupalcode.org/project/pathauto"}`,
		"/api/v4/projects/1/merge_requests/12": `{"iid":12,"title":"Fix it","state":"opened",` +
			`"source_branch":"fix","target_branch":"2.0.x","sha":"head1"}`,
	})
	defer server.Close()

	code, stdout, stderr := runCheckCommand(t, engine, client,
		"review", "pathauto", "12", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// The Site URL line itself, not the bare URL: the login URL has the site
	// URL as a prefix, so a substring test passes even with the site line
	// removed entirely.
	if !strings.Contains(stdout, "Site URL:   https://upkeep-pathauto-d11.ddev.site\n") {
		t.Errorf("it did not say where to look:\n%s", stdout)
	}
	if !strings.Contains(stdout, "one-time") {
		t.Errorf("the login URL was not offered:\n%s", stdout)
	}
	if len(engine.applied) != 1 {
		t.Errorf("it did not apply the merge request: %v", engine.applied)
	}
	// review runs no checks — that is what check is for, and running them here
	// would make a browsable site cost a full suite.
	if engine.checksRun != 0 {
		t.Errorf("review ran %d check suites", engine.checksRun)
	}
}

// A login the engine cannot mint is absent rather than an empty line.
func TestReviewWithNoLoginUrlPrintsNoLoginLine(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.served = adapter.ServeResult{URL: "https://upkeep-pathauto-d11.ddev.site"}

	server, client := aGitlabStub(t, map[string]string{
		"/api/v4/projects/project%2Fpathauto": `{"id":1,"path":"pathauto",` +
			`"path_with_namespace":"project/pathauto","web_url":"https://git.drupalcode.org/project/pathauto"}`,
		"/api/v4/projects/1/merge_requests/12": `{"iid":12,"title":"Fix it","state":"opened",` +
			`"source_branch":"fix","target_branch":"2.0.x","sha":"head1"}`,
	})
	defer server.Close()

	_, stdout, _ := runCheckCommand(t, engine, client, "review", "pathauto", "12", "--cockpit="+root)

	if strings.Contains(stdout, "Login URL") {
		t.Errorf("it printed a login line with no login:\n%s", stdout)
	}
}

// The MR-IID rule applies before anything reaches the network.
func TestCheckRefusesAnImpossibleMergeRequestIid(t *testing.T) {
	root := anEnvironmentCockpit(t)

	for _, raw := range []string{"0", "abc", "1.5", "12x"} {
		code, _, stderr := runCheckCommand(t, aCheckingEngine(), nil,
			"check", "pathauto", raw, "--cockpit="+root)

		if code != workflow.Infrastructure {
			t.Errorf("!%s gave exit %d", raw, code)
		}
		if !strings.Contains(stderr, "positive integer") {
			t.Errorf("!%s: the refusal does not say the rule: %q", raw, stderr)
		}
	}

	// A negative number never reaches the rule: the framework reads it as a
	// shorthand flag first. Still refused, still exit 2, which is what a
	// script cares about — but the message is the framework's, so this asserts
	// the outcome rather than the wording.
	code, _, _ := runCheckCommand(t, aCheckingEngine(), nil,
		"check", "pathauto", "-1", "--cockpit="+root)
	if code != workflow.Infrastructure {
		t.Errorf("!-1 gave exit %d", code)
	}
}

// A merge request that cannot be applied has no verdict to report: a suite run
// over the wrong tree is evidence about something nobody asked about.
func TestAMergeRequestThatWillNotApplyProducesNoVerdict(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.applyErr = errors.New("patch does not apply, even with a three-way merge")

	client, done := aResolvableGitlab(t, nil)
	defer done()

	code, stdout, stderr := runCheckCommand(t, engine, client,
		"check", "pathauto", "12", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if strings.Contains(stdout, "All checks green") {
		t.Errorf("it reported a verdict over a tree it never built:\n%s", stdout)
	}
	if engine.checksRun != 0 {
		t.Errorf("it ran %d check suites after a failed apply", engine.checksRun)
	}
	if !strings.Contains(stderr, "three-way merge") {
		t.Errorf("the adapter's own words were dropped: %q", stderr)
	}
}

// A red suite on the merge-request path is exit 1 too: upkeep worked, the work
// it supervised failed.
func TestARedMergeRequestSuiteIsExitOne(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	one := 1
	engine.run = check.RunResult{Results: []check.Result{
		{Type: check.PhpStan, Status: check.Failed, ExitCode: &one, Output: "Found 3 errors"},
	}}

	client, done := aResolvableGitlab(t, nil)
	defer done()

	code, stdout, _ := runCheckCommand(t, engine, client,
		"check", "pathauto", "12", "--cockpit="+root)

	if code != workflow.Failed {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stdout, "Found 3 errors") {
		t.Errorf("the failing output was dropped:\n%s", stdout)
	}

	// A red verdict is still evidence and is still cached: the dashboard's
	// whole point is showing what is known, and "checked, failed" is known.
	where, _ := cockpit.New(root)
	key, _ := results.MergeRequestKey(12)
	if results.NewCache(where.ResultsPath()).Find(
		"pathauto", key, "11", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2") == nil {
		t.Error("a red verdict was not cached")
	}
}

// --version selects the core the run targets, and reaches the cache key: two
// cores are two separate pieces of evidence about the same merge request.
func TestTheTargetCoreSelectsTheRunAndKeysTheEvidence(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()

	client, done := aResolvableGitlab(t, nil)
	defer done()

	code, _, stderr := runCheckCommand(t, engine, client,
		"check", "pathauto", "12", "--version=10", "--cockpit="+root)
	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	where, _ := cockpit.New(root)
	key, _ := results.MergeRequestKey(12)
	cache := results.NewCache(where.ResultsPath())
	sha := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

	if cache.Find("pathauto", key, "10", sha) == nil {
		t.Error("the requested core was ignored")
	}
	if cache.Find("pathauto", key, "11", sha) != nil {
		t.Error("it cached against the default core as well")
	}
}

// The registry is a watchlist, not a gate: a merge request is a subject like
// any other, and the disk answers for a module nobody registered.
func TestAnUnregisteredModuleCanStillBeChecked(t *testing.T) {
	root := anEnvironmentCockpit(t)
	where, _ := cockpit.New(root)
	// Something built here, which is what gives a derived module its cores.
	if err := os.MkdirAll(filepath.Join(where.BaseArtifactsPath(), "11"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	server, client := aGitlabStub(t, map[string]string{
		"/api/v4/projects/project%2Ftoken": `{"id":2,"path":"token",` +
			`"path_with_namespace":"project/token","web_url":"https://git.drupalcode.org/project/token"}`,
		"/api/v4/projects/2/merge_requests/12": `{"iid":12,"title":"Fix it","state":"opened",` +
			`"source_branch":"fix","target_branch":"2.0.x",` +
			`"sha":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"}`,
	})
	defer server.Close()

	code, _, stderr := runCheckCommand(t, aCheckingEngine(), client,
		"check", "token", "12", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("an unregistered module was refused: exit %d (%s)", code, stderr)
	}
}

// check and review only look, so they take the anonymous path: drupalcode
// serves a public project's merge requests without a credential, and demanding
// one would refuse work GitLab is happy to do.
func TestCheckAndReviewReadAnonymously(t *testing.T) {
	root := anEnvironmentCockpit(t)

	for _, verb := range []string{"check", "review"} {
		client, done := aResolvableGitlab(t, nil)

		code, _, stderr, asked := runCheckRecording(t, aCheckingEngine(), client,
			verb, "pathauto", "12", "--cockpit="+root)
		done()

		if code != workflow.OK {
			t.Errorf("%s: exit %d (%s)", verb, code, stderr)
		}
		if len(asked) != 1 || asked[0] != "read-only" {
			t.Errorf("%s asked for %v", verb, asked)
		}
	}
}

// The branch under test is named on the way past, so somebody watching a long
// run can see it is checking what they meant.
func TestTheWorkingCopyBranchIsNamedWhileItRuns(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = true
	engine.status = adapter.WorkingCopyStatus{CurrentBranch: "3601234-fix-the-thing"}

	_, _, stderr := runCheckCommand(t, engine, nil,
		"check", "pathauto", "--working-copy", "--cockpit="+root)

	if !strings.Contains(stderr, "Working copy branch: 3601234-fix-the-thing") {
		t.Errorf("the branch was not named while it ran: %q", stderr)
	}
}

// A detached HEAD is said as such rather than as a blank.
func TestADetachedHeadIsNamedAsOne(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = true
	engine.status = adapter.WorkingCopyStatus{}

	_, _, stderr := runCheckCommand(t, engine, nil,
		"check", "pathauto", "--working-copy", "--cockpit="+root)

	if !strings.Contains(stderr, "(detached HEAD)") {
		t.Errorf("a detached HEAD read as a blank branch: %q", stderr)
	}
}

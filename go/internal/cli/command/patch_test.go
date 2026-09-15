package command

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/results"
	"github.com/owenbush/upkeep/internal/workflow"
)

// patchingEngine records the patch flow.
type patchingEngine struct {
	checkingEngine

	applied   []adapter.PatchApplication
	refreshes []adapter.BaseRefresh
	patchErr  error

	promoted   []string
	promotion  adapter.PatchPromotion
	promoteErr error
	partial    bool
}

func (e *patchingEngine) ApplyPatch(
	_ adapter.Environment, patch adapter.PatchApplication, refresh adapter.BaseRefresh,
) error {
	e.applied = append(e.applied, patch)
	e.refreshes = append(e.refreshes, refresh)

	return e.patchErr
}

func (e *patchingEngine) PromotePatch(
	_ adapter.Environment, patch adapter.PatchApplication, branch adapter.IssueBranch,
	message string, _ adapter.BaseRefresh, partial bool,
) (adapter.PatchPromotion, error) {
	e.applied = append(e.applied, patch)
	e.promoted = append(e.promoted, branch.Name+"|"+message)
	e.partial = partial

	return e.promotion, e.promoteErr
}

// aPatchingEngine is one wired for a healthy run.
func aPatchingEngine() *patchingEngine {
	return &patchingEngine{
		checkingEngine: *aCheckingEngine(),
		promotion: adapter.CommittedPromotion(
			"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", []string{"src/Thing.php"}),
	}
}

// aPatchHost serves the patch file itself, and counts the fetches.
func aPatchHost(t *testing.T, body string) (*httptest.Server, *int) {
	t.Helper()

	fetches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches++
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	return server, &fetches
}

// aPatchDiff is something the fetcher will accept as a patch.
const aPatchDiff = "diff --git a/src/Thing.php b/src/Thing.php\n" +
	"--- a/src/Thing.php\n+++ b/src/Thing.php\n@@ -1 +1 @@\n-old\n+new\n"

// anIssueWith builds an issue carrying the given attachments.
func anIssueWith(host string, names ...string) drupal.Issue {
	files := make([]drupal.IssueFile, 0, len(names))
	for i, name := range names {
		files = append(files, drupal.IssueFile{
			Name: name, URL: host + "/files/" + name,
			Size: int64(1000 * (i + 1)), Timestamp: int64(1700000000 + i), OwnerUID: 42,
		})
	}

	return drupal.Issue{
		Nid: 3597857, Title: "Fix the token cache", Status: drupal.StatusNeedsReview,
		Version: "2.0.x", Files: files,
	}
}

// runPatchCommand invokes the tree against a scripted engine, issue source and
// patch host.
func runPatchCommand(
	t *testing.T, engine adapter.Engine, issues IssueClients, prompt scriptedPrompt, args ...string,
) (int, string, string) {
	t.Helper()

	at := 0
	prompt.at = &at

	root := NewRoot(Surface{
		Engines: &fakeFactory{engine: &fakeEngine{}, replace: engine},
		Clients: noClients{}, Issues: issues,
		Prompts: func(*cobra.Command) cli.Prompt { return prompt },
		Volumes: someVolumes(nil), Sizer: noSizer, Downloader: http.DefaultClient,
		Browser: cli.NoBrowser{},
	})

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)

	return cli.Execute(root, errOut), out.String(), errOut.String()
}

// The node id rule: a positive integer, refused before anything is fetched.
func TestTheIssueNidRule(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, fetches := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}

	for _, raw := range []string{"0", "abc", "35.9", "3597857x"} {
		code, _, stderr := runPatchCommand(t, aPatchingEngine(), issues,
			scriptedPrompt{}, "patch:apply", "pathauto", raw, "--cockpit="+root)

		if code != workflow.Infrastructure {
			t.Errorf("#%s gave exit %d", raw, code)
		}
		if !strings.Contains(stderr, "positive integer") {
			t.Errorf("#%s: the refusal does not say the rule: %q", raw, stderr)
		}
	}
	if *fetches != 0 {
		t.Errorf("it downloaded %d patches for issues that cannot exist", *fetches)
	}
}

// An issue that cannot be read is a refusal that says where the number comes
// from.
func TestAnUnreadableIssueIsARefusal(t *testing.T) {
	root := anEnvironmentCockpit(t)

	code, _, stderr := runPatchCommand(t, aPatchingEngine(), noIssues{},
		scriptedPrompt{}, "patch:apply", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "number in the issue URL") {
		t.Errorf("the refusal does not say where the node id comes from: %q", stderr)
	}
}

// patch:apply prints the environment path and nothing else, so it composes.
func TestPatchApplyPrintsThePathAlone(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()

	code, stdout, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:apply", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if stdout != "/projects/upkeep-pathauto-d11\n" {
		t.Errorf("stdout carried more than the path: %q", stdout)
	}
	if len(engine.applied) != 1 {
		t.Fatalf("applied %+v", engine.applied)
	}
	// The issue is what the branch is keyed on.
	if engine.applied[0].IssueNid != 3597857 {
		t.Errorf("applied %+v", engine.applied[0])
	}
	// And the file actually reached the disk.
	if engine.applied[0].LocalPath == "" {
		t.Error("nothing was downloaded")
	}
	if _, err := os.Stat(engine.applied[0].LocalPath); err != nil {
		t.Errorf("the patch is not on disk: %v", err)
	}
}

// One patch settles itself; several with no terminal take the newest and say
// so, because a scripted run that silently guessed would report a verdict on a
// patch the operator never named.
func TestSeveralPatchesWithNoTerminalTakeTheNewestAndSaySo(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{
		issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch", "fix-2.patch")},
	}
	engine := aPatchingEngine()

	code, _, stderr := runPatchCommand(t, engine, issues,
		scriptedPrompt{interactive: false},
		"patch:apply", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "taking the newest") {
		t.Errorf("it guessed silently: %q", stderr)
	}
	if !strings.Contains(stderr, "--file or --latest") {
		t.Errorf("it did not say how to be explicit: %q", stderr)
	}
	// The newest is the one with the later timestamp.
	if !strings.Contains(engine.applied[0].Name, "fix-2.patch") {
		t.Errorf("it took %q", engine.applied[0].Name)
	}
}

// With a terminal, the operator is asked — and the answer is honoured.
func TestWithATerminalTheOperatorIsAsked(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{
		issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch", "fix-2.patch")},
	}
	engine := aPatchingEngine()

	asked := []string{}
	code, _, stderr := runPatchCommand(t, engine, issues,
		scriptedPrompt{interactive: true, answers: []string{"second"}, asked: &asked},
		"patch:apply", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "Which patch") {
		t.Errorf("asked %v", asked)
	}
	// An unrecognised answer takes the default, which is the newest — the same
	// direction every other prompt here defaults in.
	if !strings.Contains(engine.applied[0].Name, "fix-2.patch") {
		t.Errorf("it applied %q", engine.applied[0].Name)
	}
}

// --latest and --file settle it without asking.
func TestLatestAndFileSettleItWithoutAsking(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{
		issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch", "fix-2.patch")},
	}

	asked := []string{}
	engine := aPatchingEngine()
	runPatchCommand(t, engine, issues,
		scriptedPrompt{interactive: true, asked: &asked},
		"patch:apply", "pathauto", "3597857", "--latest", "--cockpit="+root)
	if len(asked) != 0 {
		t.Errorf("--latest still asked: %v", asked)
	}
	if !strings.Contains(engine.applied[0].Name, "fix-2.patch") {
		t.Errorf("--latest applied %q", engine.applied[0].Name)
	}

	asked = nil
	engine = aPatchingEngine()
	runPatchCommand(t, engine, issues,
		scriptedPrompt{interactive: true, asked: &asked},
		"patch:apply", "pathauto", "3597857", "--file=fix-1.patch", "--cockpit="+root)
	if len(asked) != 0 {
		t.Errorf("--file still asked: %v", asked)
	}
	if !strings.Contains(engine.applied[0].Name, "fix-1.patch") {
		t.Errorf("--file applied %q", engine.applied[0].Name)
	}
}

// A --file naming nothing on the issue is a refusal, never a fallback: the
// operator named a specific patch and getting a different one is worse than
// being told it is not there.
func TestAFileNamingNothingIsARefusal(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()

	code, _, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:apply", "pathauto", "3597857", "--file=nope.patch", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if len(engine.applied) != 0 {
		t.Errorf("it applied %+v anyway", engine.applied)
	}
	if stderr == "" {
		t.Error("it refused silently")
	}
}

// --url takes a patch from somewhere else entirely — a fork, a re-roll posted
// elsewhere — while the issue still keys the branch and the report.
func TestAUrlTakesAPatchFromElsewhere(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, fetches := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()

	code, _, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:apply", "pathauto", "3597857",
		"--url="+host.URL+"/elsewhere/reroll.patch", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if *fetches != 1 {
		t.Errorf("%d downloads", *fetches)
	}
	if !strings.Contains(engine.applied[0].Name, "reroll.patch") {
		t.Errorf("it applied %q", engine.applied[0].Name)
	}
	// The issue still keys the work.
	if engine.applied[0].IssueNid != 3597857 {
		t.Errorf("applied %+v", engine.applied[0])
	}
}

// The base branch comes from the issue's version: a 2.0.x issue's patch
// applied to the default branch reads as needing a re-roll when it does not.
func TestTheBaseBranchComesFromTheIssueVersion(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()

	// The project has the branch the issue names.
	server, _ := aGitlabStub(t, map[string]string{
		"/api/v4/projects/project%2Fpathauto": `{"id":1,"path":"pathauto",` +
			`"path_with_namespace":"project/pathauto","web_url":"https://x"}`,
		"/api/v4/projects/1/repository/branches": `[{"name":"1.0.x"},{"name":"2.0.x"}]`,
	})

	code, _, stderr := runPatchWithGitlab(t, engine, issues, server,
		"patch:apply", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if engine.applied[0].BaseBranch != "2.0.x" {
		t.Errorf("base branch %q", engine.applied[0].BaseBranch)
	}
	if !strings.Contains(stderr, "from the issue version") {
		t.Errorf("it did not say where the branch came from: %q", stderr)
	}
}

// A version naming no real branch is said out loud and falls back, rather than
// refusing: the field holds whatever anyone typed.
func TestAVersionNamingNoBranchFallsBackAndSaysSo(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()

	server, _ := aGitlabStub(t, map[string]string{
		"/api/v4/projects/project%2Fpathauto": `{"id":1,"path":"pathauto",` +
			`"path_with_namespace":"project/pathauto","web_url":"https://x"}`,
		"/api/v4/projects/1/repository/branches": `[{"name":"1.0.x"}]`,
	})

	code, _, stderr := runPatchWithGitlab(t, engine, issues, server,
		"patch:apply", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if engine.applied[0].BaseBranch != "" {
		t.Errorf("it invented a base branch: %q", engine.applied[0].BaseBranch)
	}
	if !strings.Contains(stderr, "matches no branch") {
		t.Errorf("it fell back silently: %q", stderr)
	}
}

// GitLab being unreachable costs the base branch and nothing else: the patch
// surface is otherwise GitLab-free and stays usable without a token.
func TestGitlabBeingUnreachableCostsOnlyTheBaseBranch(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()

	code, _, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:apply", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(engine.applied) != 1 {
		t.Errorf("the patch was not applied: %+v", engine.applied)
	}
	if engine.applied[0].BaseBranch != "" {
		t.Errorf("base branch %q", engine.applied[0].BaseBranch)
	}
}

// --no-update opts out of refreshing the base, which is what makes a verdict
// reproducible against the tree as it was.
func TestNoUpdateReachesTheAdapter(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}

	engine := aPatchingEngine()
	runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:apply", "pathauto", "3597857", "--cockpit="+root)
	if engine.refreshes[0] != adapter.RefreshUpdate {
		t.Errorf("the default is %v", engine.refreshes[0])
	}

	engine = aPatchingEngine()
	runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:apply", "pathauto", "3597857", "--no-update", "--cockpit="+root)
	if engine.refreshes[0] != adapter.RefreshSkip {
		t.Errorf("--no-update gave %v", engine.refreshes[0])
	}
}

// A patch that will not apply produces no verdict: exit 2, because nothing
// about the contribution was established.
func TestAPatchThatWillNotApplyProducesNoVerdict(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()
	engine.patchErr = errors.New("patch does not apply, even with reduced context")

	code, stdout, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:check", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if engine.checksRun != 0 {
		t.Errorf("it ran %d check suites over a patch that did not apply", engine.checksRun)
	}
	if strings.Contains(stdout, "All checks green") {
		t.Errorf("it reported a verdict:\n%s", stdout)
	}
	if !strings.Contains(stderr, "reduced context") {
		t.Errorf("the adapter's own words were dropped: %q", stderr)
	}
}

// A patch verdict is cached under the patch namespace, which is what keeps it
// away from the fast-lane gate: a patch is not something upkeep can merge.
func TestAPatchVerdictIsCachedUnderThePatchNamespace(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()

	code, _, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:check", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	where, _ := cockpit.New(root)
	patchKey, _ := results.PatchKey(3597857)
	latest, err := results.NewCache(where.ResultsPath()).Latest("pathauto", patchKey, "11")
	if err != nil || latest == nil {
		t.Fatalf("the verdict was not cached: %v (%v)\n%s", latest, err, stderr)
	}

	// And nothing landed under the merge-request namespace, where the gate
	// reads: a patch verdict must never look like grounds for merging.
	mrKey, _ := results.MergeRequestKey(3597857)
	if found, _ := results.NewCache(where.ResultsPath()).Latest("pathauto", mrKey, "11"); found != nil {
		t.Error("a patch verdict landed where the fast-lane gate reads")
	}
}

// A red patch suite points at the issue, because the drupal.org API is
// read-only and the status change is a browser action.
func TestARedPatchSuitePointsAtTheIssue(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()
	one := 1
	engine.run = check.RunResult{Results: []check.Result{
		{Type: check.PhpCs, Status: check.Failed, ExitCode: &one, Output: "3 errors"},
	}}

	code, stdout, _ := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:check", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.Failed {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stdout, "drupal.org/node/3597857") {
		t.Errorf("it did not point at the issue:\n%s", stdout)
	}
	if !strings.Contains(stdout, "read-only") {
		t.Errorf("it did not say why it cannot do it for you:\n%s", stdout)
	}
}

// Promoting credits the patch's author in the commit: it moves somebody else's
// work into history, and the commit is the durable record of whose it was.
func TestPromotingCreditsThePatchAuthor(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{
		issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")},
		users: map[int]drupal.User{42: {
			UID: 42, Name: "someone", ProfileURL: "https://www.drupal.org/u/someone",
		}},
	}
	engine := aPatchingEngine()

	code, stdout, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:promote", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(engine.promoted) != 1 {
		t.Fatalf("promoted %v", engine.promoted)
	}
	if !strings.Contains(engine.promoted[0], "someone") {
		t.Errorf("the commit does not credit the author: %q", engine.promoted[0])
	}
	if !strings.Contains(engine.promoted[0], "3597857") {
		t.Errorf("the commit does not name the issue: %q", engine.promoted[0])
	}
	if !strings.Contains(stderr, "Patch posted by someone") {
		t.Errorf("it did not say who wrote it: %q", stderr)
	}

	// Nothing was pushed, and the next steps are named.
	if !strings.Contains(stdout, "Nothing has been pushed") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "upkeep publish pathauto 3597857") {
		t.Errorf("it did not name the publishing step:\n%s", stdout)
	}
}

// An unattributable patch is reported, never quietly promoted as if it were
// yours: a commit that dropped the name is indistinguishable from one that
// never had a name to drop.
func TestAnUnattributablePatchIsReportedNotHidden(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	// No user records for the owner.
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()

	code, _, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:promote", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	// A warning, not a progress line: both land on stderr, and the prefix is
	// what tells a reader this is something to act on rather than something
	// happening.
	if !strings.Contains(stderr, "Warning: drupal.org records no readable account") {
		t.Errorf("a missing author was reported as ordinary progress: %q", stderr)
	}
	// And the commit still says the work is not the promoter's.
	if len(engine.promoted) != 1 || !strings.Contains(engine.promoted[0], "3597857") {
		t.Errorf("promoted %v", engine.promoted)
	}
}

// A partial promotion is exit 1: the patch did not apply, which is the
// supervised work failing, and a script treating it as success would push a
// half-applied patch.
func TestAPartialPromotionIsExitOneAndCommitsNothing(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()
	engine.promotion = adapter.PartialPromotion(
		[]string{"src/Applied.php"}, []string{"src/Rejected.php"})

	code, stdout, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:promote", "pathauto", "3597857", "--partial", "--cockpit="+root)

	if code != workflow.Failed {
		t.Errorf("exit %d", code)
	}
	if !engine.partial {
		t.Error("--partial did not reach the adapter")
	}
	for _, expected := range []string{
		"src/Applied.php", "src/Rejected.php", "src/Rejected.php.rej", "Nothing is committed",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the report is missing %q:\n%s", expected, stdout)
		}
	}
	if !strings.Contains(stderr, "did not apply cleanly") {
		t.Errorf("stderr: %q", stderr)
	}
}

// --branch promotes onto a branch the maintainer names, rather than the
// convention.
func TestPromotingOntoANamedBranch(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}

	engine := aPatchingEngine()
	runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:promote", "pathauto", "3597857", "--cockpit="+root)
	conventional := strings.SplitN(engine.promoted[0], "|", 2)[0]
	if !strings.HasPrefix(conventional, "3597857-") {
		t.Errorf("the conventional branch is %q", conventional)
	}

	engine = aPatchingEngine()
	runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:promote", "pathauto", "3597857", "--branch=my-reroll", "--cockpit="+root)
	named := strings.SplitN(engine.promoted[0], "|", 2)[0]
	if !strings.Contains(named, "my-reroll") {
		t.Errorf("the named branch is %q", named)
	}
}

// The promoter's courtesy line is best-effort: an unset identity omits it
// rather than failing the promotion.
func TestAnUnsetPromoterOmitsTheCourtesyLine(t *testing.T) {
	t.Setenv(promoterEnvVar, "")

	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()

	if code, _, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:promote", "pathauto", "3597857", "--cockpit="+root); code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	t.Setenv(promoterEnvVar, "  a maintainer  ")
	named := aPatchingEngine()
	runPatchCommand(t, named, issues, scriptedPrompt{},
		"patch:promote", "pathauto", "3597857", "--cockpit="+root)

	if !strings.Contains(named.promoted[0], "a maintainer") {
		t.Errorf("the promoter was not carried: %q", named.promoted[0])
	}
	if strings.Contains(named.promoted[0], "  a maintainer") {
		t.Errorf("the promoter was not trimmed: %q", named.promoted[0])
	}
}

// runPatchWithGitlab is runPatchCommand with a GitLab that answers.
func runPatchWithGitlab(
	t *testing.T, engine adapter.Engine, issues IssueClients,
	server *httptest.Server, args ...string,
) (int, string, string) {
	t.Helper()

	root := NewRoot(Surface{
		Engines: &fakeFactory{engine: &fakeEngine{}, replace: engine},
		Clients: scriptedClients{client: gitlabClientFor(server)}, Issues: issues,
		Prompts: func(*cobra.Command) cli.Prompt { return scriptedPrompt{} },
		Volumes: someVolumes(nil), Sizer: noSizer, Downloader: http.DefaultClient,
		Browser: cli.NoBrowser{},
	})

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)

	return cli.Execute(root, errOut), out.String(), errOut.String()
}

// An issue whose version names nothing resolvable costs no requests: the patch
// surface is otherwise GitLab-free, and asking about branches for a version
// field nobody set is two round trips per run for an answer that cannot exist.
func TestAnUnsetIssueVersionAsksGitlabNothing(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)

	issue := anIssueWith(host.URL, "fix-1.patch")
	issue.Version = ""
	issues := scriptedIssues{issues: []drupal.Issue{issue}}

	asked := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404"}`))
	}))
	t.Cleanup(server.Close)

	engine := aPatchingEngine()
	code, _, stderr := runPatchWithGitlab(t, engine, issues, server,
		"patch:apply", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if asked != 0 {
		t.Errorf("it made %d requests about a version nobody set", asked)
	}
	if engine.applied[0].BaseBranch != "" {
		t.Errorf("base branch %q", engine.applied[0].BaseBranch)
	}
}

// A patch that will not apply stops patch:apply too — the path it prints is an
// invitation to go and look at something that is not there.
func TestPatchApplyReportsAFailedApply(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()
	engine.patchErr = errors.New("patch does not apply")

	code, stdout, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:apply", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if stdout != "" {
		t.Errorf("it printed a path for a patch that did not apply: %q", stdout)
	}
	if !strings.Contains(stderr, "does not apply") {
		t.Errorf("stderr: %q", stderr)
	}
}

// A partial promotion says what landed and what did not, rather than exiting
// quietly: the whole point is that the rejects are where a re-roll starts.
func TestAPartialPromotionSaysWhatIsLeftToDo(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()
	engine.promotion = adapter.PartialPromotion(nil, []string{"src/Rejected.php"})

	_, stdout, _ := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:promote", "pathauto", "3597857", "--partial", "--cockpit="+root)

	if stdout == "" {
		t.Fatal("a partial promotion reported nothing at all")
	}
	// The next steps, because the rejects are the start of the work rather
	// than the end of it.
	for _, expected := range []string{
		"src/Rejected.php.rej", "Resolve the rejects", "upkeep publish pathauto 3597857",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the report is missing %q:\n%s", expected, stdout)
		}
	}
	// Nothing applied means no "Applied:" heading over an empty list.
	if strings.Contains(stdout, "Applied:") {
		t.Errorf("it printed an empty applied list:\n%s", stdout)
	}
}

// A --url that happens to name one of the issue's attachments keeps that
// attachment's metadata — its owner, which is who the promotion credits.
func TestAUrlNamingAnAttachmentKeepsItsMetadata(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issue := anIssueWith(host.URL, "fix-1.patch")
	issues := scriptedIssues{
		issues: []drupal.Issue{issue},
		users:  map[int]drupal.User{42: {UID: 42, Name: "someone", ProfileURL: "https://x/u/someone"}},
	}
	engine := aPatchingEngine()

	code, _, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:promote", "pathauto", "3597857",
		"--url="+issue.Files[0].URL, "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// The owner came from the attachment, not from a file invented around the
	// URL — an invented one has no owner and the commit would credit nobody.
	if !strings.Contains(engine.promoted[0], "someone") {
		t.Errorf("the attachment's metadata was lost: %q", engine.promoted[0])
	}
}

// An answer that names a patch is honoured, which is the point of asking.
func TestTheChosenPatchIsTheOneApplied(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{
		issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch", "fix-2.patch")},
	}
	engine := aPatchingEngine()

	// Answer with the label the prompt is about to offer for the older patch.
	chosen := ""
	prompt := answeringPrompt{choose: func(_ string, labels []string) string {
		chosen = labels[len(labels)-1]

		return chosen
	}}

	code, _, stderr := runPatchWithPrompt(t, engine, issues, prompt,
		"patch:apply", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(chosen, "fix-1.patch") {
		t.Fatalf("the oldest label is %q", chosen)
	}
	if !strings.Contains(engine.applied[0].Name, "fix-1.patch") {
		t.Errorf("it applied %q rather than the one chosen", engine.applied[0].Name)
	}
}

// A project whose branches cannot be listed costs the base branch and nothing
// else.
func TestBranchesThatCannotBeListedCostOnlyTheBaseBranch(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}
	engine := aPatchingEngine()

	// The project resolves; its branches do not.
	server, _ := aGitlabStub(t, map[string]string{
		"/api/v4/projects/project%2Fpathauto": `{"id":1,"path":"pathauto",` +
			`"path_with_namespace":"project/pathauto","web_url":"https://x"}`,
	})

	code, _, stderr := runPatchWithGitlab(t, engine, issues, server,
		"patch:apply", "pathauto", "3597857", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(engine.applied) != 1 || engine.applied[0].BaseBranch != "" {
		t.Errorf("applied %+v", engine.applied)
	}
}

// A commit is abbreviated for a progress line, and anything already short is
// left alone.
func TestACommitIsAbbreviatedForReading(t *testing.T) {
	if got := shortSHA("a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"); got != "a1b2c3d4" {
		t.Errorf("got %q", got)
	}
	for _, short := range []string{"", "abc", "a1b2c3d4"} {
		if got := shortSHA(short); got != short {
			t.Errorf("%q became %q", short, got)
		}
	}
}

// answeringPrompt answers with whatever a function decides, so a test can
// choose by label rather than by position.
type answeringPrompt struct {
	choose func(question string, answers []string) string
}

func (p answeringPrompt) Interactive() bool { return true }

func (p answeringPrompt) Choose(question string, answers []string) string {
	return p.choose(question, answers)
}

// runPatchWithPrompt is runPatchCommand with a prompt that reads the labels.
func runPatchWithPrompt(
	t *testing.T, engine adapter.Engine, issues IssueClients, prompt cli.Prompt, args ...string,
) (int, string, string) {
	t.Helper()

	root := NewRoot(Surface{
		Engines: &fakeFactory{engine: &fakeEngine{}, replace: engine},
		Clients: noClients{}, Issues: issues,
		Prompts: func(*cobra.Command) cli.Prompt { return prompt },
		Volumes: someVolumes(nil), Sizer: noSizer, Downloader: http.DefaultClient,
		Browser: cli.NoBrowser{},
	})

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)

	return cli.Execute(root, errOut), out.String(), errOut.String()
}

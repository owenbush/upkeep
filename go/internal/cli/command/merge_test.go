package command

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
	"github.com/owenbush/upkeep/internal/workflow"
)

// The Project Update Bot's identity, which is what makes a merge request
// eligible for the fast lane at all.
const (
	botUser   = "Project-Update-Bot"
	botID     = 66574
	botBranch = "project-update-bot-only"
	botTitle  = "Automated Project Update Bot fixes"

	greenHead = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	movedHead = "0f1e2d3c4b5a0f1e2d3c4b5a0f1e2d3c4b5a0f1e"
)

// gitlabScene is a GitLab that answers a fast-lane run, and records the
// requests it received — which is how the one-action stance is asserted.
type gitlabScene struct {
	mu       sync.Mutex
	requests []string

	mergeRequest map[string]any
	// freshMergeRequest, when set, is what every fetch *after the first* sees.
	//
	// After the first, because that is what "it moved since it was
	// classified" actually looks like: the row assembly reads it once and the
	// freshness re-check reads it again, and modelling the move any other way
	// would have the row demoted before the prompt — which is a different
	// mechanism being tested by accident.
	freshMergeRequest map[string]any
	// refetchFails makes every fetch after the first fail, which is what a
	// freshness re-check that cannot be made looks like.
	refetchFails bool
	fetches      int
	// mergeRefSHA is what the merge ref reads, and freshMergeRefSHA what it
	// reads after the first time, for a commit landing on the target.
	mergeRefSHA      string
	freshMergeRefSHA string
	refReads         int
	mergeStatus      int
	mergeBody        string
	// second is another eligible merge request, for the cases where one row
	// cannot tell "stopped here" from "did them all".
	second map[string]any
	server *httptest.Server
}

func newGitlabScene(t *testing.T) *gitlabScene {
	t.Helper()

	scene := &gitlabScene{
		mergeRequest: map[string]any{
			"iid": 12, "title": botTitle, "state": "opened",
			"source_branch": botBranch, "target_branch": "2.0.x",
			"sha": greenHead, "draft": false,
			"author":        map[string]any{"username": botUser, "id": botID},
			"web_url":       "https://git.drupalcode.org/project/pathauto/-/merge_requests/12",
			"head_pipeline": map[string]any{"status": "success", "web_url": "https://ci/1"},
		},
		mergeRefSHA: greenHead,
		mergeStatus: http.StatusOK,
		mergeBody:   `{"iid":12,"state":"merged"}`,
	}

	scene.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scene.mu.Lock()
		scene.requests = append(scene.requests, r.Method+" "+r.URL.EscapedPath())
		merges := 0
		for _, seen := range scene.requests {
			if strings.HasSuffix(seen, "/merge") && strings.HasPrefix(seen, "PUT") {
				merges++
			}
		}
		scene.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.EscapedPath() == "/api/v4/projects/project%2Fpathauto":
			_, _ = w.Write([]byte(`{"id":1,"path":"pathauto","path_with_namespace":"project/pathauto",` +
				`"web_url":"https://git.drupalcode.org/project/pathauto"}`))

		case r.URL.EscapedPath() == "/api/v4/projects/1/merge_requests" && r.Method == http.MethodGet:
			listed := []any{scene.mergeRequest}
			if scene.second != nil {
				listed = append(listed, scene.second)
			}
			body, _ := json.Marshal(listed)
			_, _ = w.Write(body)

		case r.URL.EscapedPath() == "/api/v4/projects/1/merge_requests/13" && r.Method == http.MethodGet:
			body, _ := json.Marshal(scene.second)
			_, _ = w.Write(body)

		case r.URL.EscapedPath() == "/api/v4/projects/1/merge_requests/12" && r.Method == http.MethodGet:
			scene.mu.Lock()
			scene.fetches++
			payload := scene.mergeRequest
			if scene.freshMergeRequest != nil && scene.fetches > 1 {
				payload = scene.freshMergeRequest
			}
			refetchFails := scene.refetchFails && scene.fetches > 1
			scene.mu.Unlock()

			if refetchFails {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"500 Internal Server Error"}`))

				return
			}
			body, _ := json.Marshal(payload)
			_, _ = w.Write(body)

		case strings.HasSuffix(r.URL.EscapedPath(), "/merge_ref"):
			scene.mu.Lock()
			scene.refReads++
			sha := scene.mergeRefSHA
			if scene.freshMergeRefSHA != "" && scene.refReads > 1 {
				sha = scene.freshMergeRefSHA
			}
			scene.mu.Unlock()
			if sha == "" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"404"}`))

				return
			}
			_, _ = w.Write([]byte(`{"commit_id":"` + sha + `"}`))

		case strings.HasSuffix(r.URL.EscapedPath(), "/merge") && r.Method == http.MethodPut:
			w.WriteHeader(scene.mergeStatus)
			_, _ = w.Write([]byte(scene.mergeBody))

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))
		}
	}))
	t.Cleanup(scene.server.Close)

	return scene
}

func (s *gitlabScene) client() *gitlab.Client {
	return gitlab.NewClient(nil, "a-token-long-enough", s.server.URL+"/api/v4", s.server.URL)
}

// merges is how many merge calls actually reached GitLab.
func (s *gitlabScene) merges() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, seen := range s.requests {
		if strings.HasPrefix(seen, "PUT") && strings.HasSuffix(seen, "/merge") {
			count++
		}
	}

	return count
}

// aMergeableCockpit is a cockpit with one watched module and green local
// evidence for the merge request the scene serves.
func aMergeableCockpit(t *testing.T, revision string) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	root := filepath.Join(home, "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}
	where, _ := cockpit.New(root)
	registry := "modules:\n  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\"]\n"
	if err := os.WriteFile(where.RegistryPath(), []byte(registry), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if revision != "" {
		zero := 0
		key, _ := results.MergeRequestKey(12)
		if err := results.NewCache(where.ResultsPath()).Store(
			"pathauto", key, "11", revision,
			check.RunResult{Results: []check.Result{
				{Type: check.PhpCs, Status: check.Passed, ExitCode: &zero},
			}},
			time.Now(),
		); err != nil {
			t.Fatalf("store: %v", err)
		}
	}

	return root
}

// runMergeCommand invokes the tree with a scripted prompt.
func runMergeCommand(
	t *testing.T, scene *gitlabScene, prompt scriptedPrompt, args ...string,
) (int, string, string) {
	t.Helper()

	at := 0
	prompt.at = &at

	root := NewRoot(
		&fakeFactory{engine: &fakeEngine{}},
		scriptedClients{client: scene.client()},
		noIssues{},
		func(*cobra.Command) cli.Prompt { return prompt },
		noVolumes{}, noSizer,
	)
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)

	return cli.Execute(root, errOut), out.String(), errOut.String()
}

// The flag names the workflow; it never skips approval. Without it there is no
// mode to run, which is bad usage rather than a verdict about any merge
// request.
func TestMergeWithoutTheFastLaneFlagIsBadUsage(t *testing.T) {
	scene := newGitlabScene(t)
	root := aMergeableCockpit(t, greenHead)

	code, _, stderr := runMergeCommand(t, scene, scriptedPrompt{interactive: true},
		"merge", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "never skips approval") {
		t.Errorf("the refusal does not say what the flag is: %q", stderr)
	}
	if scene.merges() != 0 {
		t.Error("something merged without the flag")
	}
}

// One prompt, one approval, one API call. The stance is structural: nothing
// merges without somebody typing the word.
func TestAnApprovedMergeIsOnePromptAndOneCall(t *testing.T) {
	scene := newGitlabScene(t)
	root := aMergeableCockpit(t, greenHead)

	asked := []string{}
	code, stdout, stderr := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge"}, asked: &asked},
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if len(asked) != 1 {
		t.Errorf("it asked %d times: %v", len(asked), asked)
	}
	if scene.merges() != 1 {
		t.Errorf("%d merge calls reached GitLab", scene.merges())
	}
	if !strings.Contains(stdout, "Merged pathauto !12") {
		t.Errorf("it did not say what it merged:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Merged: 1") {
		t.Errorf("the summary is wrong:\n%s", stdout)
	}
}

// The default answer does nothing. A bare Enter, an unrecognised reply, or
// anything short of the word must leave the merge request alone.
func TestTheDefaultAnswerNeverMerges(t *testing.T) {
	for name, answer := range map[string]string{
		"an empty reply":      "",
		"an unrecognised one": "yes",
		"the default":         "skip",
	} {
		scene := newGitlabScene(t)
		root := aMergeableCockpit(t, greenHead)

		code, stdout, _ := runMergeCommand(t, scene,
			scriptedPrompt{interactive: true, answers: []string{answer}},
			"merge", "--fast-lane", "--cockpit="+root)

		if code != workflow.OK {
			t.Errorf("%s: exit %d", name, code)
		}
		if scene.merges() != 0 {
			t.Errorf("%s merged something", name)
		}
		if !strings.Contains(stdout, "Skipped pathauto !12") {
			t.Errorf("%s: it did not report the skip:\n%s", name, stdout)
		}
	}
}

// No terminal means no explicit per-merge-request affirmative is possible, and
// without one nothing merges — ever. The rows are reported as skipped rather
// than silently consuming defaults.
func TestANonInteractiveRunMergesNothing(t *testing.T) {
	scene := newGitlabScene(t)
	root := aMergeableCockpit(t, greenHead)

	code, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: false, answers: []string{"merge", "merge", "merge"}},
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if scene.merges() != 0 {
		t.Error("a non-interactive run merged something")
	}
	if !strings.Contains(stdout, "no interactive approval possible") {
		t.Errorf("it did not say why nothing happened:\n%s", stdout)
	}
}

// Quitting leaves the rest untouched, which is what makes a long list safe to
// start walking.
func TestQuittingLeavesTheRestUntouched(t *testing.T) {
	scene := newGitlabScene(t)
	root := aMergeableCockpit(t, greenHead)

	code, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"quit"}},
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if scene.merges() != 0 {
		t.Error("quitting merged something")
	}
	if !strings.Contains(stdout, "leaving the remaining rows untouched") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// The freshness re-check: anything that moved between classification and the
// prompt demotes the row with its reasons instead of merging.
func TestAMergeRequestThatMovedIsDemotedRatherThanMerged(t *testing.T) {
	for name, scripted := range map[string]struct {
		fresh  map[string]any
		reason string
	}{
		"its head moved": {
			fresh: map[string]any{
				"iid": 12, "title": botTitle, "state": "opened",
				"source_branch": botBranch, "target_branch": "2.0.x", "sha": movedHead,
				"author":        map[string]any{"username": botUser, "id": botID},
				"head_pipeline": map[string]any{"status": "success"},
			},
			reason: "sha-drift",
		},
		"somebody closed it": {
			fresh: map[string]any{
				"iid": 12, "title": botTitle, "state": "closed",
				"source_branch": botBranch, "target_branch": "2.0.x", "sha": greenHead,
				"author":        map[string]any{"username": botUser, "id": botID},
				"head_pipeline": map[string]any{"status": "success"},
			},
			reason: "state-changed:closed",
		},
		"CI went red": {
			fresh: map[string]any{
				"iid": 12, "title": botTitle, "state": "opened",
				"source_branch": botBranch, "target_branch": "2.0.x", "sha": greenHead,
				"author":        map[string]any{"username": botUser, "id": botID},
				"head_pipeline": map[string]any{"status": "failed"},
			},
			reason: "ci",
		},
		"it became a draft": {
			fresh: map[string]any{
				"iid": 12, "title": "Draft: " + botTitle, "state": "opened", "draft": true,
				"source_branch": botBranch, "target_branch": "2.0.x", "sha": greenHead,
				"author":        map[string]any{"username": botUser, "id": botID},
				"head_pipeline": map[string]any{"status": "success"},
			},
			reason: "draft",
		},
	} {
		scene := newGitlabScene(t)
		scene.freshMergeRequest = scripted.fresh
		root := aMergeableCockpit(t, greenHead)
		_ = name

		code, stdout, _ := runMergeCommand(t, scene,
			scriptedPrompt{interactive: true, answers: []string{"merge"}},
			"merge", "--fast-lane", "--cockpit="+root)

		if code != workflow.OK {
			t.Errorf("%s: exit %d", name, code)
		}
		if scene.merges() != 0 {
			t.Errorf("%s and it merged anyway", name)
		}
		if !strings.Contains(stdout, "Demoted pathauto !12") {
			t.Errorf("%s: it did not demote:\n%s", name, stdout)
		}
		if !strings.Contains(stdout, scripted.reason) {
			t.Errorf("%s: the reason %q is missing:\n%s", name, scripted.reason, stdout)
		}
	}
}

// A commit landing on the *target* makes the evidence about a different tree
// while the head SHA sits perfectly still — which is why the merge ref is
// re-read too, not just the merge request.
func TestAMovedTargetDemotesEvenThoughTheHeadDidNotMove(t *testing.T) {
	scene := newGitlabScene(t)
	// The branch is where it was; the target gained a commit between the row
	// being classified and the approval, so the merge ref moved and the
	// evidence is now about a different tree.
	scene.freshMergeRefSHA = movedHead
	root := aMergeableCockpit(t, greenHead)

	code, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge"}},
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if scene.merges() != 0 {
		t.Error("it merged on evidence about a different tree")
	}
	if !strings.Contains(stdout, "Demoted pathauto !12") {
		t.Errorf("a moved target did not demote:\n%s", stdout)
	}
	if strings.Contains(stdout, "sha-drift") {
		t.Errorf("it blamed the branch for a target that moved:\n%s", stdout)
	}
}

// The merge call carries the expected head SHA, so GitLab rejects races the
// re-check cannot see.
func TestTheMergeCallCarriesTheExpectedHead(t *testing.T) {
	scene := newGitlabScene(t)
	root := aMergeableCockpit(t, greenHead)

	// Wrapped rather than replaced: the scripted handler still has to answer
	// every other request, and this only needs to read one body on the way
	// past.
	var sent map[string]any
	captured := scene.server.Config.Handler
	scene.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.EscapedPath(), "/merge") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent = body
		}
		captured.ServeHTTP(w, r)
	})

	runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge"}},
		"merge", "--fast-lane", "--cockpit="+root)

	if sent["sha"] != greenHead {
		t.Errorf("the merge call carried sha=%v, want the expected head", sent["sha"])
	}
}

// An instance that refuses API merges is a policy decision, not a broken
// credential: the approved action becomes handing over the browser URL.
func TestAnInstanceThatRefusesApiMergesHandsOverTheBrowserUrl(t *testing.T) {
	scene := newGitlabScene(t)
	scene.mergeStatus = http.StatusForbidden
	scene.mergeBody = `{"message":"403 Forbidden"}`
	root := aMergeableCockpit(t, greenHead)

	code, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge"}},
		"merge", "--fast-lane", "--cockpit="+root)

	// Not a failure: the work can still be done, by hand, and the command said
	// exactly how.
	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stdout, "merge_requests/12") {
		t.Errorf("it did not hand over the browser URL:\n%s", stdout)
	}
	if !strings.Contains(stdout, "handled-manually") {
		t.Errorf("it did not mark the row:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Handed to browser: 1") {
		t.Errorf("the summary is wrong:\n%s", stdout)
	}
}

// A rejected credential is the operator's setup, not a verdict about this
// merge request — every remaining row would fail the same way.
func TestARejectedCredentialIsAnInfrastructureFailure(t *testing.T) {
	scene := newGitlabScene(t)
	scene.mergeStatus = http.StatusUnauthorized
	scene.mergeBody = `{"message":"401 Unauthorized"}`
	root := aMergeableCockpit(t, greenHead)

	code, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge"}},
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stdout, "Merge failed for pathauto !12") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// An ordinary merge failure is the work failing, which is exit 1.
func TestAFailedMergeIsExitOne(t *testing.T) {
	scene := newGitlabScene(t)
	scene.mergeStatus = http.StatusConflict
	scene.mergeBody = `{"message":"405 Method Not Allowed"}`
	root := aMergeableCockpit(t, greenHead)

	code, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge"}},
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.Failed {
		t.Errorf("exit %d", code)
	}
	// The browser URL is offered whatever went wrong.
	if !strings.Contains(stdout, "Merge in the browser instead") {
		t.Errorf("no manual fallback was offered:\n%s", stdout)
	}
}

// Rows that are not READY-AUTO are listed and never offered: seeing what the
// lane will not touch is how a maintainer knows it is narrow rather than
// broken.
func TestRowsThatAreNotReadyAreListedButNeverOffered(t *testing.T) {
	scene := newGitlabScene(t)
	// A human's merge request, which the lane never takes.
	scene.mergeRequest["author"] = map[string]any{"username": "someone", "id": 1}
	scene.mergeRequest["title"] = "Fix the token cache"
	root := aMergeableCockpit(t, greenHead)

	asked := []string{}
	code, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge"}, asked: &asked},
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if len(asked) != 0 {
		t.Errorf("it offered a row the lane does not take: %v", asked)
	}
	if scene.merges() != 0 {
		t.Error("it merged a row that is not READY-AUTO")
	}
	if !strings.Contains(stdout, "Needs a human (never offered for merge)") {
		t.Errorf("it did not list what it will not touch:\n%s", stdout)
	}
	if !strings.Contains(stdout, "No READY-AUTO rows") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// A merge request with no local evidence is never offered: the gate needs
// every applicable core green, and nothing checked is not green.
func TestAMergeRequestWithNoLocalEvidenceIsNeverOffered(t *testing.T) {
	scene := newGitlabScene(t)
	root := aMergeableCockpit(t, "")

	asked := []string{}
	_, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge"}, asked: &asked},
		"merge", "--fast-lane", "--cockpit="+root)

	if len(asked) != 0 {
		t.Errorf("an unchecked merge request was offered: %v", asked)
	}
	if scene.merges() != 0 {
		t.Error("it merged something nobody had checked")
	}
	if !strings.Contains(stdout, "pathauto !12") {
		t.Errorf("it was not even listed:\n%s", stdout)
	}
}

// Evidence about a revision that is no longer current is not evidence.
func TestStaleEvidenceIsNeverOffered(t *testing.T) {
	scene := newGitlabScene(t)
	// Checked against a revision the merge request has moved past.
	root := aMergeableCockpit(t, movedHead)

	asked := []string{}
	_, _, _ = runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge"}, asked: &asked},
		"merge", "--fast-lane", "--cockpit="+root)

	if len(asked) != 0 {
		t.Errorf("a stale row was offered: %v", asked)
	}
	if scene.merges() != 0 {
		t.Error("it merged on stale evidence")
	}
}

// Merging writes, so it takes the strict path: no credential means nothing can
// be classified, let alone merged.
func TestMergeRefusesWithNoCredential(t *testing.T) {
	root := aMergeableCockpit(t, greenHead)

	code, _, stderr := runCheckCommand(t, aCheckingEngine(), nil,
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if stderr == "" {
		t.Error("it refused silently")
	}
}

// aSecondMergeRequest adds another eligible row, so "stopped here" can be told
// from "did them all".
func aSecondMergeRequest(t *testing.T, scene *gitlabScene, root string) {
	t.Helper()

	scene.second = map[string]any{
		"iid": 13, "title": botTitle, "state": "opened",
		"source_branch": botBranch, "target_branch": "2.0.x",
		"sha": greenHead, "draft": false,
		"author":        map[string]any{"username": botUser, "id": botID},
		"web_url":       "https://git.drupalcode.org/project/pathauto/-/merge_requests/13",
		"head_pipeline": map[string]any{"status": "success", "web_url": "https://ci/2"},
	}

	where, _ := cockpit.New(root)
	zero := 0
	key, _ := results.MergeRequestKey(13)
	if err := results.NewCache(where.ResultsPath()).Store(
		"pathauto", key, "11", greenHead,
		check.RunResult{Results: []check.Result{
			{Type: check.PhpCs, Status: check.Passed, ExitCode: &zero},
		}},
		time.Now(),
	); err != nil {
		t.Fatalf("store: %v", err)
	}
}

// Quitting stops the walk: the rows after it are never even offered, which is
// what makes a long list safe to start.
func TestQuittingStopsTheWalkRatherThanSkippingOne(t *testing.T) {
	scene := newGitlabScene(t)
	root := aMergeableCockpit(t, greenHead)
	aSecondMergeRequest(t, scene, root)

	asked := []string{}
	code, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"quit", "merge"}, asked: &asked},
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if len(asked) != 1 {
		t.Errorf("it kept asking after quit: %v", asked)
	}
	if scene.merges() != 0 {
		t.Errorf("%d merges happened after quit", scene.merges())
	}
	if !strings.Contains(stdout, "leaving the remaining rows untouched") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// One approval is one merge: answering for the first row says nothing about
// the second, which is the whole shape of the one-approval stance.
func TestEachRowNeedsItsOwnApproval(t *testing.T) {
	scene := newGitlabScene(t)
	root := aMergeableCockpit(t, greenHead)
	aSecondMergeRequest(t, scene, root)

	asked := []string{}
	code, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge", "skip"}, asked: &asked},
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if len(asked) != 2 {
		t.Errorf("it asked %d times for two rows: %v", len(asked), asked)
	}
	if scene.merges() != 1 {
		t.Errorf("%d merges for one approval", scene.merges())
	}
	if !strings.Contains(stdout, "Merged: 1") || !strings.Contains(stdout, "Skipped: 1") {
		t.Errorf("the summary is wrong:\n%s", stdout)
	}
}

// A freshness re-check that cannot be made is a demotion, not a merge: not
// knowing whether the merge request moved is not the same as knowing it did
// not.
func TestAFreshnessRecheckThatFailsDemotesRatherThanMerging(t *testing.T) {
	scene := newGitlabScene(t)
	scene.refetchFails = true
	root := aMergeableCockpit(t, greenHead)

	code, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge"}},
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if scene.merges() != 0 {
		t.Error("it merged without knowing whether the merge request had moved")
	}
	if !strings.Contains(stdout, "freshness re-check failed") {
		t.Errorf("it did not say why:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Demoted: 1") {
		t.Errorf("the summary is wrong:\n%s", stdout)
	}
}

// Evidence that cannot be read is not evidence that says "never checked": it
// is a question that could not be asked, and merging on the strength of one is
// exactly what the re-check exists to stop.
//
// Staged at the prompt, which is the real seam: the row was classified from a
// readable cache, and something changed underneath the run while a human was
// deciding.
func TestEvidenceThatBecomesUnreadableDemotesRatherThanMerging(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory regardless of its mode")
	}

	scene := newGitlabScene(t)
	root := aMergeableCockpit(t, greenHead)
	where, _ := cockpit.New(root)
	entry := filepath.Join(where.ResultsPath(), "pathauto", "12", "11")

	code, stdout, _ := runMergeCommand(t, scene, scriptedPrompt{
		interactive: true,
		answers:     []string{"merge"},
		before: func() {
			if err := os.Chmod(entry, 0o000); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			t.Cleanup(func() { _ = os.Chmod(entry, 0o755) })
		},
	}, "merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if scene.merges() != 0 {
		t.Error("it merged on evidence it could not read")
	}
	if !strings.Contains(stdout, "evidence-unreadable") {
		t.Errorf("it did not say the evidence could not be read:\n%s", stdout)
	}
}

// A module whose project could not be read is listed as such and never
// offered: there is no merge request to act on, and saying so is better than
// leaving the module out of the listing entirely.
func TestAModuleThatCouldNotBeReadIsListedNotOffered(t *testing.T) {
	scene := newGitlabScene(t)
	root := aMergeableCockpit(t, greenHead)

	// The project endpoint stops answering, which is what a private or
	// renamed project looks like to an anonymous read.
	captured := scene.server.Config.Handler
	scene.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/api/v4/projects/project%2Fpathauto" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Project Not Found"}`))

			return
		}
		captured.ServeHTTP(w, r)
	})

	asked := []string{}
	code, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"merge"}, asked: &asked},
		"merge", "--fast-lane", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if len(asked) != 0 {
		t.Errorf("it offered a module it could not read: %v", asked)
	}
	if !strings.Contains(stdout, "merge requests unavailable") {
		t.Errorf("the module was left out of the listing entirely:\n%s", stdout)
	}
	if !strings.Contains(stdout, "pathauto") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// A merge request with no head SHA says "unknown" rather than printing a blank
// where a commit should be.
func TestAMergeRequestWithNoHeadShaSaysUnknown(t *testing.T) {
	scene := newGitlabScene(t)
	delete(scene.mergeRequest, "sha")
	// The evidence is then keyed on the merge ref, which is what check would
	// have filed it under.
	root := aMergeableCockpit(t, greenHead)

	_, stdout, _ := runMergeCommand(t, scene,
		scriptedPrompt{interactive: true, answers: []string{"skip"}},
		"merge", "--fast-lane", "--cockpit="+root)

	if !strings.Contains(stdout, "Head:   unknown") {
		t.Errorf("a missing head printed as a blank:\n%s", stdout)
	}
}

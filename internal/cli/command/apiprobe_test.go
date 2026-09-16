package command

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// probableGitlab answers a project, its open merge requests, and the single
// merge request the probe re-fetches for the pipeline.
type probableGitlab struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []string

	project      string
	open         string
	detail       string
	projectFails bool
	openStatus   int
	detailStatus int
}

func aProbableGitlab(t *testing.T) *probableGitlab {
	t.Helper()

	scene := &probableGitlab{
		project: `{"id":7,"path":"pathauto","path_with_namespace":"project/pathauto",` +
			`"web_url":"https://git.drupalcode.org/project/pathauto"}`,
		open: `[{"iid":12,"title":"Issue #3500001: Fix it","state":"opened",` +
			`"source_branch":"3500001-fix","target_branch":"2.0.x","draft":false,` +
			`"author":{"username":"amaintainer","id":11},` +
			`"sha":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",` +
			`"web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/12"}]`,
		detail: `{"iid":12,"title":"Issue #3500001: Fix it","state":"opened",` +
			`"source_branch":"3500001-fix","target_branch":"2.0.x","draft":false,` +
			`"author":{"username":"amaintainer","id":11},` +
			`"sha":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",` +
			`"detailed_merge_status":"mergeable",` +
			`"head_pipeline":{"id":9001,"status":"failed",` +
			`"web_url":"https://git.drupalcode.org/project/pathauto/-/pipelines/9001"},` +
			`"web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/12"}`,
		openStatus: http.StatusOK, detailStatus: http.StatusOK,
	}

	scene.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scene.mu.Lock()
		scene.requests = append(scene.requests, r.Method+" "+r.URL.String())
		scene.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		path := r.URL.EscapedPath()

		switch {
		case strings.HasSuffix(path, "/merge_requests/12"):
			if scene.detailStatus != http.StatusOK {
				w.WriteHeader(scene.detailStatus)
				_, _ = w.Write([]byte(`{"message":"500 Internal Server Error"}`))

				return
			}
			_, _ = w.Write([]byte(scene.detail))

		case strings.HasSuffix(path, "/merge_requests"):
			if scene.openStatus != http.StatusOK {
				w.WriteHeader(scene.openStatus)
				_, _ = w.Write([]byte(`{"message":"500 Internal Server Error"}`))

				return
			}
			_, _ = w.Write([]byte(scene.open))

		case strings.Contains(path, "/projects/"):
			if scene.projectFails {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"404 Project Not Found"}`))

				return
			}
			_, _ = w.Write([]byte(scene.project))

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))
		}
	}))
	t.Cleanup(scene.server.Close)

	return scene
}

func (s *probableGitlab) client() *gitlab.Client {
	return gitlab.NewClient(nil, "", s.server.URL+"/api/v4", s.server.URL)
}

func (s *probableGitlab) asked(fragment string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, seen := range s.requests {
		if strings.Contains(seen, fragment) {
			return true
		}
	}

	return false
}

// runProbe invokes the tree against a scripted GitLab.
func runProbe(t *testing.T, scene *probableGitlab, args ...string) (int, string, string) {
	t.Helper()

	return runIssueLoop(t, aWorkingEngine(), noIssues{},
		scriptedClients{client: scene.client()}, cli.NoBrowser{}, args...)
}

// The probe reports what GitLab said: the project, how many merge requests are
// open, and every field some part of this tool decides on.
func TestTheProbeReportsWhatGitlabSaid(t *testing.T) {
	scene := aProbableGitlab(t)

	code, stdout, stderr := runProbe(t, scene, "api:probe", "pathauto")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, expected := range []string{
		"project/pathauto", "project id 7",
		"Open merge requests: 1",
		"MR !12", "Issue #3500001: Fix it",
		// The author's id as well as the name: the bot is recognised by
		// either, and a rename is why the id is there.
		"amaintainer (id 11)",
		"3500001-fix",
		// The value, not just the label: this is a field the gate reads.
		"Draft:                 no",
		"mergeable",
		"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		// The pipeline, which is the whole reason for the second request.
		"Head pipeline: failed (#9001)",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the report is missing %q:\n%s", expected, stdout)
		}
	}
}

// The single merge request is re-fetched because only that endpoint carries
// the head pipeline.
func TestTheProbeRefetchesTheMergeRequestForItsPipeline(t *testing.T) {
	scene := aProbableGitlab(t)

	if code, _, stderr := runProbe(t, scene, "api:probe", "pathauto"); code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	if !scene.asked("/merge_requests/12") {
		t.Errorf("it never asked for the single merge request: %v", scene.requests)
	}
}

// A merge request with no pipeline says so rather than printing nothing.
func TestTheProbeSaysWhenThereIsNoPipeline(t *testing.T) {
	scene := aProbableGitlab(t)
	scene.detail = `{"iid":12,"title":"Issue #3500001: Fix it","state":"opened",
		"source_branch":"3500001-fix","author":{"username":"amaintainer","id":11},
		"web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/12"}`

	code, stdout, stderr := runProbe(t, scene, "api:probe", "pathauto")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "Head pipeline: none") {
		t.Errorf("stdout:\n%s", stdout)
	}
	// And a field GitLab did not send is distinguishable from one it sent
	// empty: for a head SHA that is "no commits" versus "the payload did not
	// say", which are answers to different questions.
	if !strings.Contains(stdout, "n/a") {
		t.Errorf("an absent field was printed blank:\n%s", stdout)
	}
}

// The re-fetch failing degrades rather than refuses: the listed payload
// answers everything except the pipeline, and losing one field is not worth
// losing the report.
func TestTheProbeDegradesWhenTheRefetchFails(t *testing.T) {
	scene := aProbableGitlab(t)
	scene.detailStatus = http.StatusInternalServerError

	code, stdout, stderr := runProbe(t, scene, "api:probe", "pathauto")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// Everything the listing carried is still reported.
	if !strings.Contains(stdout, "MR !12") || !strings.Contains(stdout, "3500001-fix") {
		t.Errorf("the report was lost with the pipeline:\n%s", stdout)
	}
	if !strings.Contains(stderr, "pipeline status unavailable") {
		t.Errorf("it did not say what was lost: %q", stderr)
	}
	// And no pipeline line at all, rather than one claiming there is none.
	if strings.Contains(stdout, "Head pipeline:") {
		t.Errorf("it reported a pipeline it could not read:\n%s", stdout)
	}
}

// A project with nothing open says so and stops: there is no merge request to
// inspect, which is a state rather than a problem.
func TestTheProbeStopsWhenNothingIsOpen(t *testing.T) {
	scene := aProbableGitlab(t)
	scene.open = `[]`

	code, stdout, stderr := runProbe(t, scene, "api:probe", "pathauto")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "Open merge requests: 0") ||
		!strings.Contains(stdout, "No open MR to inspect") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if scene.asked("/merge_requests/12") {
		t.Errorf("it re-fetched a merge request that does not exist: %v", scene.requests)
	}
}

// The two reads the report cannot be written without say what they were doing.
func TestTheProbeNamesTheStepThatFailed(t *testing.T) {
	for name, run := range map[string]struct {
		breaks  func(*probableGitlab)
		expects string
	}{
		"the project is not there": {
			breaks:  func(s *probableGitlab) { s.projectFails = true },
			expects: "project lookup failed",
		},
		"the merge requests do not list": {
			breaks:  func(s *probableGitlab) { s.openStatus = http.StatusInternalServerError },
			expects: "MR list failed",
		},
	} {
		scene := aProbableGitlab(t)
		run.breaks(scene)

		code, _, stderr := runProbe(t, scene, "api:probe", "pathauto")

		if code != workflow.Infrastructure {
			t.Errorf("%s: exit %d", name, code)
		}
		if !strings.Contains(stderr, run.expects) {
			t.Errorf("%s: the refusal does not say %q: %q", name, run.expects, stderr)
		}
	}
}

// It takes the argument as given and consults no registry: probing a project
// is a question about GitLab, not about what this cockpit watches.
func TestTheProbeAsksAboutWhateverItWasGiven(t *testing.T) {
	scene := aProbableGitlab(t)

	if code, _, stderr := runProbe(t, scene,
		"api:probe", "project/conditions_helper"); code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	if !scene.asked("project%2Fconditions_helper") {
		t.Errorf("it asked about something else: %v", scene.requests)
	}
}

// Reading needs no credential, and the probe is entirely reads — it never
// calls the merge endpoint.
func TestTheProbeOnlyEverReads(t *testing.T) {
	scene := aProbableGitlab(t)
	asked := []string{}

	tree := NewRoot(Surface{
		Engines: &fakeFactory{engine: &fakeEngine{}},
		Clients: scriptedClients{client: scene.client(), asked: &asked},
		Issues:  noIssues{}, Prompts: noPrompts, Volumes: noVolumes{}, Sizer: noSizer,
		Browser: cli.NoBrowser{},
	})
	if code, _, stderr := invokeWith(t, tree, "api:probe", "pathauto"); code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	if len(asked) != 1 || asked[0] != "read-only" {
		t.Errorf("it took the %v path", asked)
	}

	scene.mu.Lock()
	defer scene.mu.Unlock()

	for _, seen := range scene.requests {
		if !strings.HasPrefix(seen, "GET ") {
			t.Errorf("it made a request that is not a read: %q", seen)
		}
	}
}

// A draft is reported as one however GitLab signalled it: the flag, or the
// detailed merge status, which is the shape that made an empty MR read as real
// work elsewhere.
func TestTheProbeReportsADraftSignalledEitherWay(t *testing.T) {
	for name, detail := range map[string]string{
		"the draft flag": `{"iid":12,"title":"x","state":"opened","source_branch":"b",
			"draft":true,"web_url":"https://example.invalid/12"}`,
		"the merge status": `{"iid":12,"title":"x","state":"opened","source_branch":"b",
			"detailed_merge_status":"draft_status","web_url":"https://example.invalid/12"}`,
	} {
		scene := aProbableGitlab(t)
		scene.detail = detail

		_, stdout, _ := runProbe(t, scene, "api:probe", "pathauto")

		if !strings.Contains(stdout, "Draft:                 yes") {
			t.Errorf("%s: it was not reported as a draft:\n%s", name, stdout)
		}
	}
}

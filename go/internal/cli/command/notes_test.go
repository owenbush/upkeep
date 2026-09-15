package command

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// releasingGitlab serves a project, its tags and what merged since.
type releasingGitlab struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []string

	project     string
	tags        string
	merged      string
	tagStatus   int
	mergeStatus int
}

func aReleasingGitlab(t *testing.T) *releasingGitlab {
	t.Helper()

	scene := &releasingGitlab{
		project: `{"id":1,"path":"pathauto","path_with_namespace":"project/pathauto",` +
			`"web_url":"https://git.drupalcode.org/project/pathauto"}`,
		tags: `[{"name":"2.0.1","commit":{"created_at":"2026-05-01T10:00:00Z"}},
		        {"name":"2.0.0","commit":{"created_at":"2026-01-01T10:00:00Z"}}]`,
		merged:    `[]`,
		tagStatus: http.StatusOK, mergeStatus: http.StatusOK,
	}

	scene.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scene.mu.Lock()
		scene.requests = append(scene.requests, r.Method+" "+r.URL.String())
		scene.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		path := r.URL.EscapedPath()

		switch {
		case strings.HasSuffix(path, "/repository/tags"):
			if scene.tagStatus != http.StatusOK {
				w.WriteHeader(scene.tagStatus)
				_, _ = w.Write([]byte(`{"message":"500"}`))

				return
			}
			_, _ = w.Write([]byte(scene.tags))

		case strings.HasSuffix(path, "/merge_requests"):
			if scene.mergeStatus != http.StatusOK {
				w.WriteHeader(scene.mergeStatus)
				_, _ = w.Write([]byte(`{"message":"500"}`))

				return
			}
			_, _ = w.Write([]byte(scene.merged))

		case strings.Contains(path, "/projects/"):
			if scene.project == "" {
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

func (s *releasingGitlab) client() *gitlab.Client {
	return gitlab.NewClient(nil, "", s.server.URL+"/api/v4", s.server.URL)
}

// asked reports whether a request matching a fragment was made.
func (s *releasingGitlab) asked(fragment string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, seen := range s.requests {
		if strings.Contains(seen, fragment) {
			return true
		}
	}

	return false
}

// someMergedWork is a release's worth of merge requests: one from the update
// bot, two from people.
const someMergedWork = `[
	{"iid":41,"title":"Automated Project Update Bot fixes","state":"merged",
	 "source_branch":"project-update-bot-only","merged_at":"2026-05-10T10:00:00Z",
	 "author":{"username":"Project-Update-Bot","id":66574},
	 "web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/41"},
	{"iid":42,"title":"Issue #3400001: Fix the alias cache","state":"merged",
	 "source_branch":"3400001-fix","merged_at":"2026-05-11T10:00:00Z",
	 "author":{"username":"amaintainer","id":11},
	 "web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/42"},
	{"iid":43,"title":"Issue #3400002: Add a settings form","state":"merged",
	 "source_branch":"3400002-form","merged_at":"2026-05-12T10:00:00Z",
	 "author":{"username":"acontributor","id":12},
	 "web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/43"}
]`

// runNotesCommand invokes the tree against a scripted GitLab.
func runNotesCommand(
	t *testing.T, scene *releasingGitlab, args ...string,
) (int, string, string) {
	t.Helper()

	return runIssueLoop(t, aWorkingEngine(), noIssues{},
		scriptedClients{client: scene.client()}, cli.NoBrowser{}, args...)
}

// The draft is Markdown on stdout, ready to paste, with the bot's
// compatibility work grouped away from what people wrote.
func TestNotesDraftsWhatMergedSinceTheLastTag(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aReleasingGitlab(t)
	scene.merged = someMergedWork

	code, stdout, stderr := runNotesCommand(t, scene, "notes", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, expected := range []string{"pathauto", "2.0.1", "!42", "!43", "Fix the alias cache"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the draft is missing %q:\n%s", expected, stdout)
		}
	}
	// The newest tag is the boundary, so the older one is not what it asks
	// about.
	if !scene.asked("updated_after=2026-05-01") {
		t.Errorf("it did not ask from the newest tag: %v", scene.requests)
	}
	// Nothing but the Markdown on stdout: this is piped into a file.
	if strings.Contains(stdout, "Resolving") || strings.Contains(stdout, "Error") {
		t.Errorf("stdout carried diagnostics:\n%s", stdout)
	}
}

// A module with no tags is a first release: the draft covers the whole merged
// history and says so, rather than being empty.
func TestNotesDraftsTheWholeHistoryForAnUntaggedModule(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aReleasingGitlab(t)
	scene.tags = `[]`
	scene.merged = someMergedWork

	code, stdout, stderr := runNotesCommand(t, scene, "notes", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "!42") {
		t.Errorf("the history was lost with the tag:\n%s", stdout)
	}
	// And the heading says so, rather than naming a boundary tag that does
	// not exist.
	if !strings.Contains(stdout, "no previous tag") {
		t.Errorf("the draft does not say there is no tag:\n%s", stdout)
	}
	// From the epoch: everything ever merged.
	if !scene.asked("updated_after=1970-01-01") {
		t.Errorf("it did not ask from the beginning: %v", scene.requests)
	}
}

// A tag GitLab reports with no creation date cannot be a boundary, so it is
// skipped rather than ordered last — taking it would draft a range nobody
// asked for.
func TestNotesSkipsAnUndatedTag(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aReleasingGitlab(t)
	scene.tags = `[{"name":"2.1.0"},{"name":"2.0.1","commit":{"created_at":"2026-05-01T10:00:00Z"}}]`
	scene.merged = someMergedWork

	code, stdout, stderr := runNotesCommand(t, scene, "notes", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !scene.asked("updated_after=2026-05-01") {
		t.Errorf("an undated tag was used as the boundary: %v", scene.requests)
	}
	if strings.Contains(stdout, "2.1.0") {
		t.Errorf("an undated tag was named as the boundary:\n%s", stdout)
	}
}

// The registry's project path wins, because that is where the module lives —
// a machine name is not a path.
func TestNotesResolvesTheModuleThroughTheRegistry(t *testing.T) {
	root := aDashboardCockpit(t)
	where, _ := cockpit.New(root)
	if err := os.WriteFile(where.RegistryPath(), []byte(
		"modules:\n  pathauto:\n    project: project/elsewhere\n    core_versions: [\"11\"]\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	scene := aReleasingGitlab(t)

	code, _, stderr := runNotesCommand(t, scene, "notes", "pathauto", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !scene.asked("project%2Felsewhere") {
		t.Errorf("the registry's path was ignored: %v", scene.requests)
	}
}

// With no usable cockpit the argument simply *is* the project path: this is
// the one place a missing registry is not an error, because running it from
// anywhere is a supported way to use it.
func TestNotesTakesAProjectPathWithNoCockpit(t *testing.T) {
	scene := aReleasingGitlab(t)

	for name, root := range map[string]string{
		"no cockpit at all": filepath.Join(t.TempDir(), "nowhere"),
		// A registry that loads and simply does not carry the name: an
		// unregistered module is an ordinary way to run this.
		"a registry without the module": aDashboardCockpit(t),
		"an unreadable registry": func() string {
			root := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(root, cockpit.RegistryFilename), []byte("\tnot: [yaml"), 0o644,
			); err != nil {
				t.Fatalf("write: %v", err)
			}

			return root
		}(),
	} {
		scene.mu.Lock()
		scene.requests = nil
		scene.mu.Unlock()

		code, _, stderr := runNotesCommand(t, scene,
			"notes", "project/conditions_helper", "--cockpit="+root)

		if code != workflow.OK {
			t.Fatalf("%s: exit %d (%s)", name, code, stderr)
		}
		if !scene.asked("project%2Fconditions_helper") {
			t.Errorf("%s: it did not take the argument as a path: %v", name, scene.requests)
		}
	}
}

// The heading names the module as it was asked for, not the project path: the
// draft is about the thing being released, and "project/pathauto" is where it
// lives.
func TestNotesHeadsTheDraftWithTheModuleAsAsked(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aReleasingGitlab(t)

	_, stdout, _ := runNotesCommand(t, scene, "notes", "pathauto", "--cockpit="+root)

	if !strings.Contains(stdout, "pathauto") {
		t.Errorf("the draft does not name the module:\n%s", stdout)
	}
	if strings.Contains(stdout, "project/pathauto") {
		t.Errorf("the draft is headed with a path:\n%s", stdout)
	}
}

// Each of the three reads says what it was doing when it failed.
func TestNotesNamesTheStepThatFailed(t *testing.T) {
	root := aDashboardCockpit(t)

	for name, run := range map[string]struct {
		breaks  func(*releasingGitlab)
		expects string
	}{
		"the project is not there": {
			breaks:  func(s *releasingGitlab) { s.project = "" },
			expects: "project lookup failed",
		},
		"the tags do not list": {
			breaks:  func(s *releasingGitlab) { s.tagStatus = http.StatusInternalServerError },
			expects: "tag list failed",
		},
		"the merged merge requests do not list": {
			breaks:  func(s *releasingGitlab) { s.mergeStatus = http.StatusInternalServerError },
			expects: "merged-MR list failed",
		},
	} {
		scene := aReleasingGitlab(t)
		run.breaks(scene)

		code, stdout, stderr := runNotesCommand(t, scene, "notes", "pathauto", "--cockpit="+root)

		if code != workflow.Infrastructure {
			t.Errorf("%s: exit %d", name, code)
		}
		if !strings.Contains(stderr, run.expects) {
			t.Errorf("%s: the refusal does not say %q: %q", name, run.expects, stderr)
		}
		// And nothing half-drafted reached stdout, which somebody is piping.
		if stdout != "" {
			t.Errorf("%s: a failed run still produced a draft: %q", name, stdout)
		}
	}
}

// Reading needs no credential — a public project's tags and merged merge
// requests are served anonymously — so this runs without one.
func TestNotesNeedsNoCredential(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aReleasingGitlab(t)
	asked := []string{}

	tree := NewRoot(Surface{
		Engines: &fakeFactory{engine: &fakeEngine{}},
		Clients: scriptedClients{client: scene.client(), asked: &asked},
		Issues:  noIssues{}, Prompts: noPrompts, Volumes: noVolumes{}, Sizer: noSizer,
		Browser: cli.NoBrowser{},
	})
	if code, _, stderr := invokeWith(t, tree, "notes", "pathauto", "--cockpit="+root); code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}

	if len(asked) != 1 || asked[0] != "read-only" {
		t.Errorf("it took the %v path", asked)
	}
}

// It drafts and never publishes: tagging and cutting a release stay manual,
// so every request it makes is a read.
func TestNotesOnlyEverReads(t *testing.T) {
	root := aDashboardCockpit(t)
	scene := aReleasingGitlab(t)
	scene.merged = someMergedWork

	runNotesCommand(t, scene, "notes", "pathauto", "--cockpit="+root)

	scene.mu.Lock()
	defer scene.mu.Unlock()

	if len(scene.requests) == 0 {
		t.Fatal("it made no requests at all")
	}
	for _, seen := range scene.requests {
		if !strings.HasPrefix(seen, "GET ") {
			t.Errorf("it made a request that is not a read: %q", seen)
		}
	}
}

// A cockpit that cannot be resolved at all is the same fallback: the argument
// is the path. Refusing here would make `upkeep notes project/x` depend on
// where it was run from, which is the opposite of the point.
func TestNotesTakesAProjectPathWithAnUnresolvableCockpit(t *testing.T) {
	scene := aReleasingGitlab(t)

	code, _, stderr := runNotesCommand(t, scene,
		"notes", "project/conditions_helper", "--cockpit=")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !scene.asked("project%2Fconditions_helper") {
		t.Errorf("it did not take the argument as a path: %v", scene.requests)
	}
}

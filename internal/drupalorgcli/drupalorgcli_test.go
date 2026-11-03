package drupalorgcli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/owenbush/upkeep/internal/gitlab"
)

// testToken is long enough for the redactor to mask; short values are
// deliberately ignored by it.
const testToken = "glpat-SECRET-test-token-1234"

// stub installs a fake drupalorg as UPKEEP_DRUPALORG_BIN that reports version
// on --version and otherwise runs body, and resets the per-process gate so
// each test sees its own binary.
func stub(t *testing.T, version, body string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "drupalorg")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then\n" +
		"  echo x >> \"$(dirname \"$0\")/version-calls\"\n" +
		"  echo 'Drupal.org CLI " + version + "'\n" +
		"  exit 0\n" +
		"fi\n" +
		body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv(BinEnvVar, path)
	resetGate()
	t.Cleanup(resetGate)

	return path
}

// runnerWithToken builds a runner whose resolver yields token (or nothing).
func runnerWithToken(t *testing.T, token string) *Runner {
	t.Helper()

	t.Setenv("UPKEEP_DRUPALORG_TEST_TOKEN", token)

	return New(gitlab.NewTokenResolver("UPKEEP_DRUPALORG_TEST_TOKEN", filepath.Join(t.TempDir(), "none"), nil), 0)
}

func TestTheVersionGate(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T)
		wantErr []string // substrings; nil means the gate passes
	}{
		{"exact minimum", func(t *testing.T) { stub(t, "0.10.3", "echo '[]'") }, nil},
		{"newer patch compared numerically", func(t *testing.T) { stub(t, "0.10.10", "echo '[]'") }, nil},
		{"newer major", func(t *testing.T) { stub(t, "1.0.0", "echo '[]'") }, nil},
		{"too old", func(t *testing.T) { stub(t, "0.9.0", "echo '[]'") },
			[]string{"0.9.0", "0.10.3", BinEnvVar, "github.com/mglaman/drupalorg-cli"}},
		{"one patch short", func(t *testing.T) { stub(t, "0.10.2", "echo '[]'") }, []string{"0.10.2", "0.10.3"}},
		{"missing binary", func(t *testing.T) {
			t.Setenv(BinEnvVar, "")
			t.Setenv("PATH", t.TempDir())
			resetGate()
			t.Cleanup(resetGate)
		}, []string{"not found", BinEnvVar, "github.com/mglaman/drupalorg-cli"}},
		{"override points nowhere", func(t *testing.T) {
			t.Setenv(BinEnvVar, filepath.Join(t.TempDir(), "absent"))
			resetGate()
			t.Cleanup(resetGate)
		}, []string{"absent", BinEnvVar}},
		{"missing PHP", func(t *testing.T) {
			path := stub(t, "0.10.3", "")
			// What `#!/usr/bin/env php` does with no PHP installed.
			_ = os.WriteFile(path, []byte("#!/bin/sh\necho \"env: 'php': No such file or directory\" >&2\nexit 127\n"), 0o755)
		}, []string{"PHP", BinEnvVar, "exit 127"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			r := runnerWithToken(t, "")

			failure := r.Run(context.Background(), []string{"project:issues", "x"}, nil)
			if tc.wantErr == nil {
				if failure != nil {
					t.Fatalf("gate refused: %s", failure.Message)
				}

				return
			}
			if failure == nil {
				t.Fatal("gate passed, want a refusal")
			}
			if failure.Kind != gitlab.TransportError {
				t.Errorf("kind = %v, want TransportError", failure.Kind)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(failure.Message, want) {
					t.Errorf("message %q lacks %q", failure.Message, want)
				}
			}
		})
	}
}

func TestTheVersionIsCheckedOncePerProcess(t *testing.T) {
	path := stub(t, "0.10.3", "echo '[]'")
	r := runnerWithToken(t, "")

	for range 3 {
		if failure := r.Run(context.Background(), []string{"mr:list", "x"}, nil); failure != nil {
			t.Fatalf("run: %s", failure.Message)
		}
	}
	// A second runner in the same process shares the verdict.
	if failure := runnerWithToken(t, "").Run(context.Background(), []string{"mr:list", "x"}, nil); failure != nil {
		t.Fatalf("run: %s", failure.Message)
	}

	calls, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "version-calls"))
	if got := strings.Count(string(calls), "x"); got != 1 {
		t.Errorf("--version ran %d times, want 1", got)
	}
}

// echoBody prints the child's argv and token env as JSON, and also writes the
// token to a side file — stdout is redacted on its way back, so the file is
// what shows what the child actually received.
const echoBody = `printf '%s' "$DRUPALORG_GITLAB_TOKEN" > "$(dirname "$0")/token-seen"
printf '{"token":"%s","upkeep":"%s","args":"%s"}' "$DRUPALORG_GITLAB_TOKEN" "$UPKEEP_GITLAB_TOKEN" "$*"`

type echoed struct {
	Token  string `json:"token"`
	Upkeep string `json:"upkeep"`
	Args   string `json:"args"`
}

func TestTheTokenTravelsOnlyInTheChildEnvironment(t *testing.T) {
	path := stub(t, "0.10.3", echoBody)
	t.Setenv(gitlab.DefaultEnvVar, "glpat-inherited-upkeep-token")
	r := runnerWithToken(t, testToken)

	var got echoed
	if failure := r.Run(context.Background(), []string{"mr:list", "my_module"}, &got); failure != nil {
		t.Fatalf("run: %s", failure.Message)
	}

	seen, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "token-seen"))
	if string(seen) != testToken {
		t.Errorf("child DRUPALORG_GITLAB_TOKEN = %q, want the resolved token", seen)
	}
	if got.Token != "[REDACTED]" {
		t.Errorf("token echoed on stdout came back as %q, want it redacted", got.Token)
	}
	if strings.Contains(got.Args, testToken) {
		t.Errorf("token reached argv: %q", got.Args)
	}
	if got.Upkeep != "" {
		t.Error("child inherited UPKEEP_GITLAB_TOKEN; only DRUPALORG_GITLAB_TOKEN should carry it")
	}
	if got.Args != "mr:list my_module --format=json --no-interaction" {
		t.Errorf("argv = %q", got.Args)
	}
}

func TestNoTokenMeansNoTokenVariable(t *testing.T) {
	path := stub(t, "0.10.3", echoBody)
	t.Setenv(TokenEnvVar, "")
	r := runnerWithToken(t, "")

	if failure := r.Run(context.Background(), []string{"mr:list", "x"}, &echoed{}); failure != nil {
		t.Fatalf("run: %s", failure.Message)
	}
	if seen, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "token-seen")); len(seen) != 0 {
		t.Errorf("child DRUPALORG_GITLAB_TOKEN = %q, want unset", seen)
	}
}

func TestLocalCommandsCanOmitTheFormatFlags(t *testing.T) {
	stub(t, "0.10.3", `echo "$*"`)
	r := runnerWithToken(t, "")

	out, failure := r.RunRaw(context.Background(), []string{"issue:setup-remote", "123"}, WithoutFormat())
	if failure != nil {
		t.Fatalf("run: %s", failure.Message)
	}
	if got := strings.TrimSpace(string(out)); got != "issue:setup-remote 123" {
		t.Errorf("argv = %q", got)
	}
}

func TestFailuresMapOntoTheGitlabTaxonomy(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantKind   gitlab.FailureKind
		wantStatus int
		wantText   string
	}{
		{"project not found", `echo 'Project  not found.'; exit 1`, gitlab.NotFound, 0, "Project not found."},
		{"guzzle 404", "echo 'Client error: `GET https://git.drupalcode.org/api/v4/projects/x` resulted in a `404 Not Found` response' >&2; exit 1",
			gitlab.NotFound, 404, "404"},
		{"guzzle 401", "echo 'Client error: `GET https://git.drupalcode.org/api/v4/user` resulted in a `401 Unauthorized` response' >&2; exit 1",
			gitlab.Unauthorized, 401, "UPKEEP_DRUPALORG_TEST_TOKEN"},
		{"guzzle 403", "echo 'Client error: `POST x` resulted in a `403 Forbidden` response' >&2; exit 1",
			gitlab.EndpointClosed, 403, "403"},
		{"guzzle 429", "echo 'Client error: `GET x` resulted in a `429 Too Many Requests` response' >&2; exit 1",
			gitlab.RateLimited, 429, "429"},
		{"guzzle 500", "echo 'Server error: `GET x` resulted in a `500 Internal Server Error` response' >&2; exit 1",
			gitlab.RequestRejected, 500, "500"},
		{"auth wording", `echo '401 Unauthorized' >&2; exit 1`, gitlab.Unauthorized, 0, "token"},
		{"network", `echo 'cURL error 6: Could not resolve host: git.drupalcode.org' >&2; exit 1`,
			gitlab.TransportError, 0, "Could not resolve host"},
		{"usage error", `echo 'The query argument is required.'; exit 1`,
			gitlab.RequestRejected, 0, "The query argument is required."},
		{"token echoed in stderr is scrubbed", `echo "bad token $DRUPALORG_GITLAB_TOKEN rejected" >&2; exit 2`,
			gitlab.RequestRejected, 0, "[REDACTED]"},
		{"success that is not JSON", `echo 'Done!'`, gitlab.MalformedResponse, 0, "mr:list"},
		{"success with no output", `true`, gitlab.MalformedResponse, 0, "mr:list"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub(t, "0.10.3", tc.body)
			r := runnerWithToken(t, testToken)

			var out any
			failure := r.Run(context.Background(), []string{"mr:list", "x"}, &out)
			if failure == nil {
				t.Fatal("no failure")
			}
			if failure.Kind != tc.wantKind {
				t.Errorf("kind = %v, want %v (%s)", failure.Kind, tc.wantKind, failure.Message)
			}
			if failure.Status != tc.wantStatus {
				t.Errorf("status = %d, want %d", failure.Status, tc.wantStatus)
			}
			if !strings.Contains(failure.Message, tc.wantText) {
				t.Errorf("message %q lacks %q", failure.Message, tc.wantText)
			}
			if strings.Contains(failure.Message, testToken) {
				t.Errorf("token leaked into failure: %q", failure.Message)
			}
		})
	}
}

func TestACancelledRunIsATransportError(t *testing.T) {
	stub(t, "0.10.3", "sleep 5")
	r := runnerWithToken(t, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failure := r.Run(ctx, []string{"mr:list", "x"}, nil)
	if failure == nil || failure.Kind != gitlab.TransportError {
		t.Fatalf("failure = %+v, want TransportError", failure)
	}
}

func TestConcurrentRunsAreBounded(t *testing.T) {
	path := stub(t, "0.10.3", "")
	slots := filepath.Join(filepath.Dir(path), "running")
	if err := os.Mkdir(slots, 0o755); err != nil {
		t.Fatal(err)
	}
	// Each child marks itself running, records how many are, then leaves.
	body := `f="` + slots + `/$$"; : > "$f"; ls "` + slots + `" | wc -l >> "` + slots + `.log"; sleep 0.2; rm "$f"; echo '[]'`
	_ = os.WriteFile(path, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'Drupal.org CLI 0.10.3'; exit 0; fi\n"+body+"\n"), 0o755)

	const limit = 2
	r := New(nil, limit)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if failure := r.Run(context.Background(), []string{"mr:list", "x"}, nil); failure != nil {
				t.Errorf("run: %s", failure.Message)
			}
		})
	}
	wg.Wait()

	log, _ := os.ReadFile(slots + ".log")
	for line := range strings.FieldsSeq(string(log)) {
		if n, _ := strconv.Atoi(line); n > limit {
			t.Fatalf("%d children ran at once, limit %d", n, limit)
		}
	}
	if New(nil, 0).Capacity() != DefaultMaxConcurrent || DefaultMaxConcurrent != 8 {
		t.Errorf("default concurrency is not 8")
	}
}

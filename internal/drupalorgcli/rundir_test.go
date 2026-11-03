package drupalorgcli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/gitlab"
)

// RunRawIn runs the child inside the working tree it is given, which is the
// whole point of it: issue:setup-remote and issue:checkout act on the git
// repository in their working directory.
func TestRunRawInRunsTheChildInTheGivenDirectory(t *testing.T) {
	stub(t, "0.10.3", `echo "$@"; pwd -P`)
	dir := t.TempDir()

	out, failure := New(nil, 0).RunRawIn(context.Background(), dir,
		[]string{"issue:setup-remote", "123"}, WithoutFormat())
	if failure != nil {
		t.Fatalf("failure: %+v", failure)
	}

	want, _ := filepath.EvalSymlinks(dir)
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || lines[0] != "issue:setup-remote 123" || lines[1] != want {
		t.Errorf("output %q, want args then %q", out, want)
	}
}

func TestRunRawInRefusesNoDirectoryAndMapsFailures(t *testing.T) {
	stub(t, "0.10.3", `echo "Project  not found." >&2; exit 1`)
	runner := New(nil, 0)

	if _, failure := runner.RunRawIn(context.Background(), "", []string{"issue:checkout"}); failure == nil ||
		!strings.Contains(failure.Message, "directory") {
		t.Errorf("an empty directory was accepted: %+v", failure)
	}

	_, failure := runner.RunRawIn(context.Background(), t.TempDir(), []string{"issue:checkout", "1"})
	if failure == nil || failure.Kind != gitlab.NotFound {
		t.Errorf("failure %+v, want NotFound", failure)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, failure := runner.RunRawIn(ctx, t.TempDir(), []string{"issue:checkout"}); failure == nil ||
		failure.Kind != gitlab.TransportError {
		t.Errorf("a cancelled context: %+v", failure)
	}
}

func TestRunRawInStopsAtTheVersionGate(t *testing.T) {
	stub(t, "0.9.0", `echo ran`)

	if _, failure := New(nil, 0).RunRawIn(context.Background(), t.TempDir(), []string{"x"}); failure == nil ||
		!strings.Contains(failure.Message, "0.9.0") {
		t.Errorf("an old CLI was run: %+v", failure)
	}
}

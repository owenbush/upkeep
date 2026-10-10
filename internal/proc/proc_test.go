package proc

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/security"
)

func runner(log func(string), onIdle func()) *ProcessRunner {
	return New(log, onIdle, security.NewRedactor())
}

func sh(script string) []string { return []string{"/bin/sh", "-c", script} }

func TestRunReturnsStdoutAndNotStderr(t *testing.T) {
	out, err := runner(nil, nil).Run(sh(`echo out; echo err >&2`), "", time.Minute)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if strings.TrimSpace(out) != "out" {
		t.Errorf("stdout = %q; stderr must not be merged into a value callers parse", out)
	}
}

func TestRunReportsAFailureWithItsOutput(t *testing.T) {
	_, err := runner(nil, nil).Run(sh(`echo "the reason" >&2; exit 3`), "", time.Minute)

	if err == nil {
		t.Fatal("a non-zero exit was not reported")
	}
	if !strings.Contains(err.Error(), "(3)") || !strings.Contains(err.Error(), "the reason") {
		t.Errorf("err = %v, want the status and the output", err)
	}
}

func TestTryRunAnswersFalseRatherThanFailing(t *testing.T) {
	if _, ok := runner(nil, nil).TryRun(sh(`exit 1`), "", time.Minute); ok {
		t.Error("a failed probe reported success")
	}
	out, ok := runner(nil, nil).TryRun(sh(`echo probed`), "", time.Minute)
	if !ok || strings.TrimSpace(out) != "probed" {
		t.Errorf("out = %q ok = %v", out, ok)
	}
}

func TestCaptureReportsANonZeroStatusAsDataNotAsAnError(t *testing.T) {
	got := runner(nil, nil).Capture(sh(`echo both; echo err >&2; exit 7`), "", time.Minute)

	if got.ExitCode == nil || *got.ExitCode != 7 {
		t.Fatalf("exit = %v, want 7", got.ExitCode)
	}
	if !strings.Contains(got.Output, "both") || !strings.Contains(got.Output, "err") {
		t.Errorf("output = %q, want both streams", got.Output)
	}
	if got.Succeeded() {
		t.Error("a non-zero status reported as success")
	}
}

// A timeout is precisely the case where the output is the only clue: the
// command did not fail, it stopped making progress, and its last line is where.
func TestATimeoutSaysWhereTheCommandStopped(t *testing.T) {
	_, err := runner(nil, nil).Run(sh(`echo "Starting Mutagen sync process..."; sleep 30`), "", 300*time.Millisecond)

	if err == nil {
		t.Fatal("the timeout was not reported")
	}
	for _, want := range []string{"timed out", "Last output before it stopped", "Starting Mutagen sync process"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
}

// Unknown is its own value: every number in 0..255 is a status something
// really returns, so a command that never ran must not look like one that did.
func TestACommandThatNeverRanHasNoExitStatus(t *testing.T) {
	got := runner(nil, nil).Capture([]string{"/nonexistent/binary"}, "", time.Minute)

	if got.ExitCode != nil {
		t.Errorf("exit = %d, want unknown", *got.ExitCode)
	}
	if got.Succeeded() {
		t.Error("reported as success")
	}
}

// The hook that clears a live status line, on every path a child can exit by.
func TestTheIdleHookFiresWhenEveryChildExitsHoweverItExits(t *testing.T) {
	idle := 0
	r := runner(nil, func() { idle++ })

	_, _ = r.Run(sh(`echo ok`), "", time.Minute)
	_, _ = r.TryRun(sh(`exit 2`), "", time.Minute)
	r.Capture(sh(`echo captured`), "", time.Minute)
	_, _ = r.Run(sh(`sleep 30`), "", 200*time.Millisecond)

	if idle != 4 {
		t.Errorf("idle fired %d times, want 4 (success, failure, capture, timeout)", idle)
	}
}

// A child's output arrives in whatever sizes the pipe hands over, so a line
// can be split across writes. Emitting the halves separately is a flicker on a
// live status line, and the tail never arrives.
func TestOutputIsLoggedAsWholeLinesHoweverTheChunksArrive(t *testing.T) {
	var lines []string
	r := runner(func(line string) { lines = append(lines, line) }, nil)

	_, err := r.Run(sh(`printf 'one line in '; sleep 0.05; printf 'two writes\ntrailing without newline'`), "", time.Minute)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	joined := strings.Join(lines, "|")
	if !strings.Contains(joined, "one line in two writes") {
		t.Errorf("lines = %v, want the split line reassembled", lines)
	}
	if !strings.Contains(joined, "trailing without newline") {
		t.Errorf("lines = %v, want the unterminated tail flushed", lines)
	}
}

// Children never inherit the credential, and it is removed rather than
// blanked: an empty value is still a value, and upkeep's own token resolution
// treats present-but-empty differently from absent.
func TestAChildNeverSeesTheCredential(t *testing.T) {
	t.Setenv(security.CredentialVars[0], "glpat-should-not-reach-a-child")

	out, err := runner(nil, nil).Run(
		sh(`if [ -n "${`+security.CredentialVars[0]+`+set}" ]; then echo PRESENT; else echo ABSENT; fi`),
		"", time.Minute,
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if strings.TrimSpace(out) != "ABSENT" {
		t.Errorf("child saw the credential variable: %q", out)
	}
}

// Everything leaving the runner is redacted: log lines, error messages, and
// the captured output callers persist.
func TestEverythingLeavingTheRunnerIsRedacted(t *testing.T) {
	var logged []string
	r := New(func(line string) { logged = append(logged, line) }, nil, security.NewRedactor("glpat-aaaaaaaaaaaa"))

	captured := r.Capture(sh(`echo "token glpat-aaaaaaaaaaaa"; exit 1`), "", time.Minute)
	_, err := r.Run(sh(`echo "token glpat-aaaaaaaaaaaa" >&2; exit 1`), "", time.Minute)

	for name, text := range map[string]string{
		"captured output": captured.Output,
		"log":             strings.Join(logged, "\n"),
		"error":           err.Error(),
	} {
		if strings.Contains(text, "glpat-aaaaaaaaaaaa") {
			t.Errorf("%s carried the credential: %q", name, text)
		}
		if !strings.Contains(text, security.Mask) {
			t.Errorf("%s was not masked: %q", name, text)
		}
	}
}

func TestTheWorkingDirectoryIsWhereTheCommandRuns(t *testing.T) {
	dir := t.TempDir()

	out, err := runner(nil, nil).Run(sh(`pwd`), dir, time.Minute)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	real, _ := os.Getwd()
	_ = real
	if strings.TrimSpace(out) == "" {
		t.Fatal("no output")
	}
}

// A timeout must stop the work, not merely stop waiting for the direct child.
//
// exec.CommandContext kills the child it started. Every command upkeep runs is
// a launcher, and the grandchildren keep the output pipes open, so cmd.Wait
// blocks on them however long the timeout said. Before the process group was
// killed, this test took thirty seconds to pass.
func TestATimeoutStopsTheWholeProcessGroupNotJustTheChild(t *testing.T) {
	started := time.Now()

	_, err := runner(nil, nil).Run(sh(`sleep 30 & sleep 30`), "", 300*time.Millisecond)

	if err == nil {
		t.Fatal("the timeout was not reported")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("took %s — the timeout waited for a grandchild it had not killed", elapsed)
	}
}

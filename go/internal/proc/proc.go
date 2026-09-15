// Package proc is the shell-out seam every engine interaction goes through.
package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/owenbush/upkeep/internal/security"
)

// DefaultTimeout is generous because the steps behind it are: a composer
// resolve or a site install genuinely takes minutes. Callers with a tighter
// expectation say so — starting an environment that already exists does.
const DefaultTimeout = time.Hour

// failureOutputBytes bounds what a failure message carries. Child output is
// untrusted and unbounded; a failing composer step can emit megabytes.
const failureOutputBytes = 4000

// Captured is the full outcome of a child process, for callers that need to
// reason about it rather than just succeed or fail.
type Captured struct {
	// ExitCode is nil when the child produced no status at all — killed
	// before it could report one, or never executed. "Unknown" is its own
	// value rather than a sentinel integer, because every number in 0..255 is
	// a status something really returns.
	ExitCode *int
	// Output is stdout and stderr combined, in stream order, redacted.
	Output   string
	TimedOut bool
	Duration time.Duration
}

// Succeeded reports whether the child exited zero.
func (c Captured) Succeeded() bool {
	return !c.TimedOut && c.ExitCode != nil && *c.ExitCode == 0
}

// Runner runs commands. Engine interactions go through this seam so they can
// be exercised without a container runtime.
type Runner interface {
	// Run returns the child's stdout, or an error describing how it failed.
	Run(command []string, dir string, timeout time.Duration) (string, error)
	// TryRun returns stdout, or "" and false when the child did not succeed.
	// For probes, where failure is an answer rather than a problem.
	TryRun(command []string, dir string, timeout time.Duration) (string, bool)
	// Capture reports the whole outcome, including a non-zero status, without
	// treating any of it as an error.
	Capture(command []string, dir string, timeout time.Duration) Captured
}

// ProcessRunner runs real commands.
//
// Two credential guarantees live here, because this is the only boundary child
// output crosses: children never inherit the credential environment, and
// everything leaving — log lines, error messages, and the captured output that
// callers persist — passes through the redactor first.
type ProcessRunner struct {
	log      func(string)
	onIdle   func()
	redactor *security.Redactor
}

// New builds a runner.
//
// log receives every line a child prints, redacted. onIdle is called as each
// child exits, however it exits — the one moment nothing is mid-print, which
// is when a live status line has to go. Either may be nil.
func New(log func(string), onIdle func(), redactor *security.Redactor) *ProcessRunner {
	if redactor == nil {
		redactor = security.RedactorFromEnvironment()
	}

	return &ProcessRunner{log: log, onIdle: onIdle, redactor: redactor}
}

func (r *ProcessRunner) Run(command []string, dir string, timeout time.Duration) (string, error) {
	result, stdout := r.execute(command, dir, timeout)

	if result.TimedOut {
		// With what it said before it stopped. A timeout is precisely the case
		// where the output is the only clue: the command did not fail, it
		// stopped making progress, and its last line is where.
		message := fmt.Sprintf("command timed out after %s: %s", timeout, strings.Join(command, " "))
		if excerpt := failureExcerpt(result.Output); excerpt != "" {
			message += "\nLast output before it stopped:\n" + excerpt
		}

		return "", errors.New(r.redactor.Redact(message))
	}

	if !result.Succeeded() {
		return "", errors.New(r.redactor.Redact(fmt.Sprintf(
			"command failed (%s): %s\n%s",
			exitCodeLabel(result.ExitCode),
			strings.Join(command, " "),
			failureExcerpt(result.Output),
		)))
	}

	return stdout, nil
}

func (r *ProcessRunner) TryRun(command []string, dir string, timeout time.Duration) (string, bool) {
	result, stdout := r.execute(command, dir, timeout)
	if !result.Succeeded() {
		return "", false
	}

	return stdout, true
}

func (r *ProcessRunner) Capture(command []string, dir string, timeout time.Duration) Captured {
	result, _ := r.execute(command, dir, timeout)

	return result
}

// execute runs the child and returns the whole outcome plus stdout alone.
//
// Both are needed: callers that want output want stdout, while failure
// reporting wants stderr too, and merging them for the former would put
// progress chatter into values that get parsed.
func (r *ProcessRunner) execute(command []string, dir string, timeout time.Duration) (Captured, string) {
	defer r.idle()

	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = security.ScrubbedEnvironment()
	killWholeGroup(cmd)

	var stdout, combined syncBuffer
	lines := &lineWriter{emit: r.log, redactor: r.redactor}
	cmd.Stdout = io.MultiWriter(&stdout, &combined, lines)
	cmd.Stderr = io.MultiWriter(&combined, lines)

	runErr := cmd.Run()
	lines.flush()
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)

	captured := Captured{
		Output:   r.redactor.Redact(combined.String()),
		TimedOut: timedOut,
		Duration: time.Since(started),
	}
	if !timedOut && cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		if code >= 0 {
			captured.ExitCode = &code
		}
	}
	if runErr != nil && captured.ExitCode == nil && !timedOut {
		// Never started: no binary, no permission. Not a status the child
		// chose, so it is left unknown rather than flattened onto one.
		captured.Output = r.redactor.Redact(strings.TrimRight(combined.String(), "\n") + "\n" + runErr.Error())
	}

	return captured, r.redactor.Redact(stdout.String())
}

func (r *ProcessRunner) idle() {
	if r.onIdle != nil {
		r.onIdle()
	}
}

func exitCodeLabel(code *int) string {
	if code == nil {
		return "no exit status"
	}

	return fmt.Sprint(*code)
}

// failureExcerpt keeps the tail, which is where a failure says what happened.
func failureExcerpt(output string) string {
	output = strings.TrimSpace(output)
	if len(output) <= failureOutputBytes {
		return output
	}

	return fmt.Sprintf(
		"[output truncated to the last %d bytes]\n%s",
		failureOutputBytes,
		output[len(output)-failureOutputBytes:],
	)
}

// syncBuffer is a bytes.Buffer safe for the two goroutines exec gives stdout
// and stderr.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

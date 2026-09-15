package proc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The streams are the caller's: nothing is buffered, captured or line-split,
// because the command being run is the operator's own and its output is the
// answer rather than progress.
func TestPassthroughWiresTheStreamsStraightThrough(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}

	code, err := Passthrough(
		[]string{"sh", "-c", "echo out; echo err >&2"}, "",
		strings.NewReader(""), stdout, stderr,
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code == nil || *code != 0 {
		t.Fatalf("code %v", code)
	}
	if strings.TrimSpace(stdout.String()) != "out" {
		t.Errorf("stdout %q", stdout)
	}
	if strings.TrimSpace(stderr.String()) != "err" {
		t.Errorf("stderr %q", stderr)
	}
	// Kept apart: merging them would put a command's diagnostics inside its
	// answer, which is the thing a caller is capturing.
	if strings.Contains(stdout.String(), "err") {
		t.Errorf("the streams were merged: %q", stdout)
	}
}

// Stdin reaches the child, so an interactive command works.
func TestStdinReachesTheChild(t *testing.T) {
	stdout := &bytes.Buffer{}

	if _, err := Passthrough(
		[]string{"cat"}, "", strings.NewReader("typed\n"), stdout, &bytes.Buffer{},
	); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "typed" {
		t.Errorf("stdout %q", stdout)
	}
}

// The child runs where it was told.
func TestPassthroughRunsInTheGivenDirectory(t *testing.T) {
	dir := t.TempDir()
	stdout := &bytes.Buffer{}

	if _, err := Passthrough([]string{"pwd"}, dir, nil, stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(stdout.String()), filepath.Base(dir)) {
		t.Errorf("it ran in %q, want %q", stdout, dir)
	}
}

// The credential is stripped here as it is everywhere else: this is the
// package that does it, which is why the passthrough lives here rather than in
// the command that needs it.
func TestPassthroughStripsTheCredential(t *testing.T) {
	t.Setenv("UPKEEP_GITLAB_TOKEN", "s3cr3t-token-value")
	stdout := &bytes.Buffer{}

	if _, err := Passthrough(
		[]string{"sh", "-c", "echo \"[${UPKEEP_GITLAB_TOKEN:-absent}]\""}, "",
		nil, stdout, &bytes.Buffer{},
	); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "[absent]") {
		t.Errorf("the child was handed the credential: %q", stdout)
	}
}

// And the rest of the environment is not: the command being wrapped is the
// operator's own and usually needs their PATH.
func TestPassthroughKeepsTheRestOfTheEnvironment(t *testing.T) {
	t.Setenv("UPKEEP_TEST_MARKER", "present")
	stdout := &bytes.Buffer{}

	if _, err := Passthrough(
		[]string{"sh", "-c", "echo $UPKEEP_TEST_MARKER"}, "", nil, stdout, &bytes.Buffer{},
	); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "present" {
		t.Errorf("the environment was emptied: %q", stdout)
	}
}

// The child's own exit code comes back, whatever it is: the caller decides what
// it means.
func TestTheChildsOwnExitCodeComesBack(t *testing.T) {
	for _, want := range []int{0, 1, 2, 42} {
		code, err := Passthrough(
			[]string{"sh", "-c", "exit " + itoa(want)}, "", nil, &bytes.Buffer{}, &bytes.Buffer{},
		)
		if err != nil {
			t.Fatalf("exit %d: %v", want, err)
		}
		if code == nil || *code != want {
			t.Errorf("a child exiting %d reported %v", want, code)
		}
	}
}

// A child that never ran has no code it chose, so none is invented — upkeep
// failing is a different thing from the command failing.
func TestAChildThatNeverRanHasNoCode(t *testing.T) {
	code, err := Passthrough(
		[]string{"no-such-binary-anywhere"}, "", nil, &bytes.Buffer{}, &bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("a missing binary reported success")
	}
	if code != nil {
		t.Errorf("it invented an exit code: %d", *code)
	}

	// And a directory that does not exist is the same kind of failure.
	if _, err := Passthrough(
		[]string{"pwd"}, filepath.Join(t.TempDir(), "nope"), nil, &bytes.Buffer{}, &bytes.Buffer{},
	); err == nil {
		t.Error("it ran in a directory that does not exist")
	}
}

// Nothing to run is refused rather than reaching past the end of the command.
func TestNothingToRunIsRefused(t *testing.T) {
	if _, err := Passthrough(nil, "", nil, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Error("an empty command line ran something")
	}
}

// A signal the child was sent is not a code it chose either.
func TestAKilledChildHasNoCodeItChose(t *testing.T) {
	if os.Getenv("CI") == "" && testing.Short() {
		t.Skip("short mode")
	}

	code, err := Passthrough(
		[]string{"sh", "-c", "kill -TERM $$"}, "", nil, &bytes.Buffer{}, &bytes.Buffer{},
	)
	if code != nil {
		t.Errorf("a killed child reported exit %d as its own choice", *code)
	}
	if err == nil {
		t.Error("a killed child reported success")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	return string(digits)
}

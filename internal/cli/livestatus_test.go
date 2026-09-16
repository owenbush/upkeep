package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// On a terminal, the latest line sits on one line that overwrites itself.
func TestOnATerminalTheLatestLineOverwritesTheLast(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, true, false)

	status.Line("Resolving dependencies")
	status.Line("Installing drupal/core")

	written := out.String()
	if strings.Count(written, "\r") != 2 {
		t.Errorf("each line did not return to the start:\n%q", written)
	}
	if !strings.Contains(written, "Installing drupal/core") {
		t.Errorf("the latest line is missing:\n%q", written)
	}
	// Erased rather than painted over, so a shorter line does not leave the
	// tail of a longer one behind it.
	if strings.Count(written, "\x1b[2K") != 2 {
		t.Errorf("the line was not erased before being rewritten:\n%q", written)
	}
}

// Anywhere that is not a terminal nothing extra is written: overwriting a line
// in a CI log produces escape codes, not progress.
func TestOffATerminalNothingExtraIsWritten(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, false, false)

	status.Line("Resolving dependencies")
	status.Clear()

	if out.Len() != 0 {
		t.Errorf("a redirected stream got escape codes: %q", out)
	}
}

// Under -v every line is kept, plainly, wherever it is going: the point of the
// flag is to have the transcript.
func TestUnderVerboseEveryLineIsKept(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		out := &bytes.Buffer{}
		status := NewLiveStatus(out, terminal, true)

		status.Line("Resolving dependencies")
		status.Line("Installing drupal/core")
		status.Clear()

		written := out.String()
		for _, line := range []string{"Resolving dependencies", "Installing drupal/core"} {
			if !strings.Contains(written, line) {
				t.Errorf("terminal=%v: %q was dropped:\n%q", terminal, line, written)
			}
		}
		if strings.Contains(written, "\x1b[") || strings.Contains(written, "\r") {
			t.Errorf("terminal=%v: a transcript carried escape codes:\n%q", terminal, written)
		}
	}
}

// The line is erased when the child exits — the one moment nothing can be
// mid-print, so a stage line or a results table never lands glued onto the end
// of one.
func TestClearingErasesWhatWasShowing(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, true, false)

	status.Line("Starting the environment")
	out.Reset()
	status.Clear()

	if !strings.Contains(out.String(), "\x1b[2K") {
		t.Errorf("nothing was erased: %q", out)
	}

	// And clearing again writes nothing: there is nothing showing, and a
	// stray escape sequence into a fresh line is litter.
	out.Reset()
	status.Clear()
	if out.Len() != 0 {
		t.Errorf("it erased a line that was not there: %q", out)
	}
}

// Clearing before anything has been shown writes nothing at all, so a command
// that starts no child leaves the terminal exactly as it found it.
func TestClearingBeforeAnythingIsShownIsSilent(t *testing.T) {
	out := &bytes.Buffer{}
	NewLiveStatus(out, true, false).Clear()

	if out.Len() != 0 {
		t.Errorf("got %q", out)
	}
}

// A long line is truncated rather than wrapped: a wrapped status line cannot
// be erased by the carriage return that erases the rest, so it would stay on
// the screen under whatever came next.
func TestALongLineIsTruncatedRatherThanWrapped(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, true, false)

	status.Line(strings.Repeat("x", 400))

	written := out.String()
	if len([]rune(written)) > statusWidth+16 {
		t.Errorf("a long line was not truncated: %d characters", len([]rune(written)))
	}
	if !strings.Contains(written, "…") {
		t.Errorf("the truncation was silent: %q", written)
	}
}

// Blank lines are not progress: a child that prints an empty line should not
// erase what it last said.
func TestABlankLineIsNotProgress(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, true, false)

	status.Line("Installing drupal/core")
	out.Reset()
	status.Line("")
	status.Line("   ")

	if out.Len() != 0 {
		t.Errorf("a blank line overwrote the last real one: %q", out)
	}
}

// A child's line endings are its own; the status line is one line.
func TestLineEndingsAreStripped(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, true, false)

	status.Line("Installing drupal/core\r\n")

	if strings.Contains(out.String(), "\n") {
		t.Errorf("the status line broke onto a second line: %q", out)
	}
}

// Whether a stream is a terminal is what decides between progress and escape
// code litter, so it is asked of the stream rather than assumed.
func TestOnlyACharacterDeviceCountsAsATerminal(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Error("a buffer reported itself as a terminal")
	}

	file, err := os.CreateTemp(t.TempDir(), "not-a-terminal")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer file.Close()

	if IsTerminal(file) {
		t.Error("a regular file reported itself as a terminal")
	}
	if !IsTerminal(os.Stdin) && isCharacterDevice(t, os.Stdin) {
		t.Error("a character device was not recognised")
	}
}

func isCharacterDevice(t *testing.T, file *os.File) bool {
	t.Helper()

	info, err := file.Stat()

	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

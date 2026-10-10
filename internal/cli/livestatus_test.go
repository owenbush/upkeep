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

// A child's colour never outlives the line it was on.
//
// The regression: composer paints a warning, the line is longer than the
// status line keeps, and the truncation lands before the reset — so the reset
// is never written, `\x1b[2K` erases the characters but not the attribute,
// and everything after it is red. Including the results table, which goes to
// stdout and never came through here: the run said "All checks green" in the
// colour of failure.
func TestAChildsColourNeverOutlivesTheStatusLine(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, true, false)

	status.Line("\x1b[31mDeprecated: " + strings.Repeat("warning ", 40) + "\x1b[0m")
	status.Clear()

	written := out.String()
	// Only upkeep's own erase sequence survives; nothing that sets a colour.
	for _, sequence := range []string{"\x1b[31m", "\x1b[0m", "\x1b[1m"} {
		if strings.Contains(written, sequence) {
			t.Errorf("a child's %q reached the terminal:\n%q", sequence, written)
		}
	}
	if !strings.Contains(written, "Deprecated: warning") {
		t.Errorf("the text went with the escape codes:\n%q", written)
	}
}

// Cursor moves, titles and bells go the same way: the status line is one line
// upkeep erases and rewrites, and a child cannot be allowed to scroll it,
// retitle the window or make the terminal beep once per package.
func TestAChildCannotDriveTheTerminal(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, true, false)

	// A bell, a window title, a cursor move, a backspace and a DEL — the last
	// two being how a child that thinks it has a terminal rubs out a character
	// it has already written.
	status.Line("\x1b]0;window title\x07\x1b[2AInstalling\x07 drupal/corex\x7f\x08\x1b[K")

	written := out.String()
	if !strings.Contains(written, "Installing drupal/corex") {
		t.Errorf("the text did not survive:\n%q", written)
	}
	for _, sequence := range []string{"\x1b]0;", "\x1b[2A", "\x07", "\x7f", "\x08", "window title"} {
		if strings.Contains(written, sequence) {
			t.Errorf("%q reached the terminal:\n%q", sequence, written)
		}
	}
}

// A carriage return inside the line is composer drawing download progress. Left
// in, it returns to column zero within the status line, so the rest of the
// line overwrites upkeep's own indent rather than following it.
func TestACarriageReturnInsideTheLineIsNotAnInstruction(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, true, false)

	status.Line("Downloading 40%\rDownloading 80%")

	// One carriage return, upkeep's own, at the front.
	if strings.Count(out.String(), "\r") != 1 {
		t.Errorf("a child's carriage return stayed in the line:\n%q", out)
	}
}

// Truncation counts characters, not bytes: cutting a multi-byte character in
// half puts a replacement character on the line instead of shortening it.
func TestTruncationCountsCharactersRatherThanBytes(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, true, false)

	status.Line(strings.Repeat("é", 400))

	written := out.String()
	if strings.Contains(written, "\ufffd") {
		t.Errorf("a character was cut in half:\n%q", written)
	}
	if visible := strings.Count(written, "é"); visible != statusWidth-1 {
		t.Errorf("kept %d characters, want %d:\n%q", visible, statusWidth-1, written)
	}
}

// The escape codes are what the width is spent on otherwise: a line that is
// mostly colour should be truncated on the text it shows, not on its bytes.
func TestTheWidthIsSpentOnVisibleCharacters(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, true, false)

	painted := ""
	for range 40 {
		painted += "\x1b[32mok\x1b[0m "
	}
	status.Line(painted)

	// 120 visible characters of a 400-byte line: truncated, but on what it
	// would have shown rather than on the colour around it.
	if !strings.Contains(out.String(), "…") {
		t.Errorf("the line was not truncated at all:\n%q", out)
	}
	if visible := strings.Count(out.String(), "ok"); visible < 30 {
		t.Errorf("escape codes ate the width: only %d of 40 marks kept:\n%q", visible, out)
	}
}

// A tab becomes a space rather than being dropped.
//
// phpcs and phpstan align their output with tabs, and the two other readings
// are both wrong: left in, a tab jumps to the next tab stop, so a line this
// file has measured at ninety-nine characters lands wider than the terminal
// and wraps — the one thing the truncation exists to prevent. Dropped, the
// words either side of it run together.
func TestATabBecomesASpace(t *testing.T) {
	out := &bytes.Buffer{}
	status := NewLiveStatus(out, true, false)

	status.Line("FILE\tERRORS\tWARNINGS")

	written := out.String()
	if strings.Contains(written, "\t") {
		t.Errorf("a tab stayed in the line:\n%q", written)
	}
	if !strings.Contains(written, "FILE ERRORS WARNINGS") {
		t.Errorf("the columns ran together:\n%q", written)
	}
}

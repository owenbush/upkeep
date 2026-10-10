package cli

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// LiveStatus is where a child process's output goes.
//
// On a terminal, the latest line sits on one line that overwrites itself and
// is erased when the child exits. Under -v every line is kept. Anywhere that
// is not a terminal nothing extra is written, because overwriting a line in a
// CI log produces escape codes rather than progress.
//
// It used to be all or nothing, and both ends were wrong for the same reason.
// All of it buried the few lines a command exists to print — a fresh provision
// is hundreds of lines and `dev`'s four-line answer scrolled off the top. None
// of it, the fix for that, made a wedged start indistinguishable from a slow
// one: after a reboot the engine stalled on its file sync and upkeep said
// "starting it" and then nothing.
//
// The indicator advances only when output arrives. Frozen is the point, since
// that is what waiting looks like; a spinner on a timer would keep turning
// while nothing happened.
type LiveStatus struct {
	out      io.Writer
	terminal bool
	verbose  bool
	width    int

	showing bool
}

// statusWidth is how much of a line is kept. Long enough to carry a composer
// package name or an engine step, short enough not to wrap on a narrow
// terminal — a wrapped status line cannot be erased by the carriage return
// that erases the rest.
const statusWidth = 100

// NewLiveStatus builds one for the given stream.
func NewLiveStatus(out io.Writer, terminal, verbose bool) *LiveStatus {
	return &LiveStatus{out: out, terminal: terminal, verbose: verbose, width: statusWidth}
}

// controlSequence matches an ANSI escape sequence: CSI, which is the colours
// and the cursor moves, OSC, which sets a window title and ends in BEL or ST,
// and the two-character escapes.
var controlSequence = regexp.MustCompile(
	`\x1b\[[0-9;:?]*[ -/]*[@-~]` +
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?` +
		`|\x1b[@-Z\\-_]`,
)

// plainText reduces a child's line to the text in it.
//
// The status line borrows one line of somebody else's output, and anything in
// that line which sets a colour or moves a cursor is an instruction about a
// line upkeep is about to erase and rewrite itself. Two of them actively
// break it.
//
// A colour whose reset falls past the truncation below leaves the terminal
// painted: `\x1b[2K` erases characters, not attributes, so the reset never
// arrives and every line after it comes out in the colour of some composer
// warning — including the results table, which goes to stdout and never went
// near this file. That is the bug this exists for.
//
// A carriage return mid-line, which is how composer draws download progress,
// returns to column zero inside the status line, so the rest of it overwrites
// upkeep's own indent instead of following it.
//
// Stripping rather than trying to balance the sequences also makes
// statusWidth mean what it says: a hundred visible columns, rather than a
// hundred bytes of which forty were escape codes.
func plainText(line string) string {
	line = controlSequence.ReplaceAllString(line, "")

	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}

		return r
	}, line)
}

// Line receives one line of a child's output.
func (s *LiveStatus) Line(line string) {
	line = plainText(strings.TrimRight(line, "\r\n"))

	if s.verbose {
		fmt.Fprintln(s.out, line)

		return
	}
	if !s.terminal || strings.TrimSpace(line) == "" {
		return
	}

	// Measured in runes rather than bytes: a composer package name or an
	// engine step can carry a multi-byte character, and cutting one in half
	// puts a replacement character on the line instead of shortening it.
	if runes := []rune(line); len(runes) > s.width {
		line = string(runes[:s.width-1]) + "…"
	}
	fmt.Fprintf(s.out, "\r\x1b[2K  %s", line)
	s.showing = true
}

// Clear erases the status line.
//
// Called as each child exits, however it exits — the one moment nothing can be
// mid-print, so a stage line or a results table never lands glued onto the end
// of one.
func (s *LiveStatus) Clear() {
	if !s.showing {
		return
	}
	fmt.Fprint(s.out, "\r\x1b[2K")
	s.showing = false
}

// IsTerminal reports whether a stream is a terminal, which is what decides
// whether a status line would be progress or escape-code litter.
func IsTerminal(stream io.Writer) bool {
	file, isFile := stream.(*os.File)
	if !isFile {
		return false
	}

	info, err := file.Stat()

	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

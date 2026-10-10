package cli

import (
	"strings"
	"testing"
)

// Off wherever the output is not a terminal: a table piped into a file or a
// pager should contain the table, not escape sequences around it.
func TestAnOffPaletteWritesNoEscapes(t *testing.T) {
	off := NewPalette(false)

	if got := off.Paint(Green, "pass"); got != "pass" {
		t.Errorf("got %q", got)
	}
	if got := off.Highlight(Yellow, "2 ↑", "↑"); got != "2 ↑" {
		t.Errorf("got %q", got)
	}
}

// On, it wraps and closes: an unterminated sequence bleeds into every cell
// after it.
func TestAnOnPaletteWrapsAndCloses(t *testing.T) {
	on := NewPalette(true)

	painted := on.Paint(Green, "pass")
	if !strings.HasPrefix(painted, string(Green)) {
		t.Errorf("not opened: %q", painted)
	}
	if !strings.HasSuffix(painted, "\x1b[0m") {
		t.Errorf("not closed: %q", painted)
	}
	if !strings.Contains(painted, "pass") {
		t.Errorf("the text was lost: %q", painted)
	}
}

// An empty cell is never painted: an escape sequence around nothing is
// invisible bytes in a cell that is meant to be blank — and the table measures
// the raw cell, so those bytes would push every column after it out of line.
func TestAnEmptyCellIsNeverPainted(t *testing.T) {
	if got := NewPalette(true).Paint(Red, ""); got != "" {
		t.Errorf("an empty cell became %q", got)
	}
}

// Highlighting marks one substring and leaves the rest alone, for a cell where
// the mark is the claim and the number around it is context.
func TestHighlightingMarksOnlyTheMark(t *testing.T) {
	on := NewPalette(true)

	painted := on.Highlight(Yellow, "2 ↑", "↑")
	if !strings.HasPrefix(painted, "2 ") {
		t.Errorf("the context was painted too: %q", painted)
	}
	if !strings.Contains(painted, string(Yellow)+"↑") {
		t.Errorf("the mark was not painted: %q", painted)
	}

	// A cell without the mark is untouched, and so is an empty mark.
	if got := on.Highlight(Yellow, "2", "↑"); got != "2" {
		t.Errorf("got %q", got)
	}
	if got := on.Highlight(Yellow, "2 ↑", ""); got != "2 ↑" {
		t.Errorf("got %q", got)
	}
}

// Painting does not change a cell's visible width, which is what the table
// measures with.
func TestPaintingDoesNotChangeVisibleWidth(t *testing.T) {
	on := NewPalette(true)

	for _, cell := range []string{"pass", "fail 10", "pass 11 · ? 10", "–"} {
		painted := on.Paint(Green, cell)
		if visible := stripEscapes(painted); visible != cell {
			t.Errorf("%q painted to a different visible string %q", cell, visible)
		}
	}
}

// stripEscapes removes ANSI sequences.
func stripEscapes(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\x1b' {
			for i < len(text) && text[i] != 'm' {
				i++
			}

			continue
		}
		out.WriteByte(text[i])
	}

	return out.String()
}

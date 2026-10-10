package cli

import "strings"

// Colour is how a cell is emphasised.
//
// ANSI written directly rather than through a library: the whole vocabulary is
// eight codes, and a dependency for that would be a dependency to keep.
type Colour string

const (
	Grey   Colour = "\x1b[90m"
	Red    Colour = "\x1b[31m"
	Green  Colour = "\x1b[32m"
	Yellow Colour = "\x1b[33m"
	Cyan   Colour = "\x1b[36m"

	reset = "\x1b[0m"
)

// Palette decides whether colour is written at all.
//
// Off wherever the output is not a terminal, which is the property that
// matters: a table piped into a file or a pager should contain the table,
// not escape sequences around it. Callers ask the palette rather than
// checking for themselves, so a command cannot acquire its own rule.
type Palette struct{ enabled bool }

// NewPalette builds one. Colour is written only when asked for.
func NewPalette(enabled bool) Palette { return Palette{enabled: enabled} }

// Paint wraps text, or returns it unchanged when colour is off.
//
// Empty text is never painted: an escape sequence around nothing is invisible
// bytes in a cell that is meant to be blank.
func (p Palette) Paint(colour Colour, text string) string {
	if !p.enabled || text == "" {
		return text
	}

	return string(colour) + text + reset
}

// Highlight paints one substring wherever it appears, leaving the rest alone.
//
// For a cell where the mark is the claim and the number around it is context —
// the patch arrow being the case that prompted it.
func (p Palette) Highlight(colour Colour, text, mark string) string {
	if !p.enabled || mark == "" || !strings.Contains(text, mark) {
		return text
	}

	return strings.ReplaceAll(text, mark, string(colour)+mark+reset)
}

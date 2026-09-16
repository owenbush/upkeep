package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// tableGap is the space between columns.
const tableGap = 4

// Table is the wide fixed-width listing the report commands render: a header
// row, columns padded to their widest cell, and per-cell colouring applied
// after the widths are measured so escape sequences never disturb the
// alignment.
//
// One implementation, because two hand-rolled copies had already begun to
// differ in how they measured and padded.
type Table struct {
	Headers []string
	Rows    [][]string

	// Colourise decorates one row's cells. It must not change their visible
	// length — the widths are measured from the raw cells, and a decorated
	// cell that is longer pushes every column after it out of line.
	Colourise func(cells []string) []string

	// GroupKeys is an optional per-row grouping key; a blank line separates
	// rows whose key differs from the one before.
	GroupKeys []string
}

// Render writes the table.
func (t Table) Render(out io.Writer) {
	widths := make([]int, len(t.Headers))
	for i, header := range t.Headers {
		widths[i] = utf8.RuneCountInString(header)
	}
	for _, cells := range t.Rows {
		for i, cell := range cells {
			for i >= len(widths) {
				widths = append(widths, 0)
			}
			if length := utf8.RuneCountInString(cell); length > widths[i] {
				widths[i] = length
			}
		}
	}

	var header strings.Builder
	for i, text := range t.Headers {
		header.WriteString(pad(text, widths[i]+tableGap))
	}
	fmt.Fprintln(out, strings.TrimRight(header.String(), " "))
	fmt.Fprintln(out)

	lastGroup, grouped := "", false
	for index, cells := range t.Rows {
		if index < len(t.GroupKeys) {
			group := t.GroupKeys[index]
			if grouped && group != lastGroup {
				fmt.Fprintln(out)
			}
			lastGroup, grouped = group, true
		}

		decorated := cells
		if t.Colourise != nil {
			decorated = t.Colourise(cells)
		}

		var line strings.Builder
		for i, cell := range decorated {
			line.WriteString(cell)
			if i < len(widths) {
				line.WriteString(strings.Repeat(
					" ", widths[i]-utf8.RuneCountInString(cells[i])+tableGap,
				))
			}
		}
		fmt.Fprintln(out, strings.TrimRight(line.String(), " "))
	}
}

// pad right-pads to a rune width.
func pad(text string, width int) string {
	if short := width - utf8.RuneCountInString(text); short > 0 {
		return text + strings.Repeat(" ", short)
	}

	return text
}

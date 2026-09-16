package command

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/workflow"
)

// wrapWidth is how wide a definition is allowed to run.
//
// Definitions are sentences, not cells, so they are wrapped under their term
// rather than squeezed into a column: a table would put a paragraph in a box
// eight characters wide on a narrow terminal.
const wrapWidth = 74

// NewExplain builds the explain command.
//
// Every other command answers a question about a module. This one answers a
// question about the output itself, which until it existed had no answer
// anywhere. It needs no cockpit, no token and no network — it is a glossary —
// so it works from anywhere, including in the middle of being confused by
// something.
func NewExplain() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explain [term]",
		Short: "Explain a term from upkeep's output",
		Long: "Explain a term from upkeep's output. Run it bare to list every term.\n\n" +
			"Matches on the term and on its meaning, so a half-remembered word still finds it.",
		Args: cobra.MaximumNArgs(1),
	}
	// The glossary is data on disk here, so completing it costs nothing and
	// turns "what was that word?" into two keystrokes.
	cmd.ValidArgsFunction = func(
		_ *cobra.Command, args []string, typed string,
	) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		terms := make([]string, 0, len(Glossary))
		for _, definition := range Glossary {
			if strings.HasPrefix(definition.Term, typed) {
				terms = append(terms, definition.Term)
			}
		}

		return terms, cobra.ShellCompDirectiveNoFileComp
	}
	cmd.RunE = cli.Run(runExplain)

	return cmd
}

func runExplain(cmd *cobra.Command, args []string) (int, error) {
	query := ""
	if len(args) == 1 {
		query = args[0]
	}

	matches := SearchGlossary(query)

	if len(matches) == 0 {
		cli.Printf(cmd, "Nothing in upkeep's output is called %q.\n\n", query)
		cli.Println(cmd, "upkeep explain — with no argument — lists every term.")

		// Not knowing a word is not a failure: the command did what was asked
		// and reported the answer, which happens to be "no such term". A
		// script asking about an unknown word wants to read that, not to
		// handle an error.
		return workflow.OK, nil
	}

	if query == "" {
		cli.Println(cmd, "Every term upkeep prints. Narrow it with: upkeep explain <term>")
		cli.Println(cmd, "")
	}

	for _, definition := range matches {
		cli.Printf(cmd, "%s  (%s)\n", definition.Term, definition.Where)
		for _, line := range wrapText(definition.Meaning, wrapWidth) {
			cli.Println(cmd, "    "+line)
		}
		cli.Println(cmd, "")
	}

	return workflow.OK, nil
}

// wrapText breaks a sentence at word boundaries.
//
// A word longer than the width is left whole rather than cut: the words that
// run long here are command names and file paths, and half of one is worse
// than a line that overhangs.
func wrapText(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}

	lines := []string{}
	current := words[0]
	for _, word := range words[1:] {
		if len(current)+1+len(word) > width {
			lines = append(lines, current)
			current = word

			continue
		}
		current += " " + word
	}

	return append(lines, current)
}

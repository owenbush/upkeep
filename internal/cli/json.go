package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// Machine-readable output.
//
// The reason this exists is narrower than "some people like JSON": the tables
// are built for a person and are actively hostile to a program. Titles are
// truncated to fit a column, an empty cell is rendered as an en dash, counts
// are padded, and a column's width is whatever the widest row needed. All of
// that is correct for reading and wrong for parsing, and none of it would
// announce a change — a consumer would simply start getting different answers.
//
// So `--json` is not the table serialised. It is a separate, declared payload
// (`internal/report`) carrying the untruncated values, and the table stays free
// to change shape.
//
// **One document on stdout and nothing else.** That is the contract the whole
// feature rests on, and it is cheap to hold because the split already existed:
// progress and diagnostics go to stderr (`Progressf`, `Warnf`), payload goes to
// stdout. A stray `Println` on the stdout path is the one way this breaks, so
// the command suite parses the whole of stdout for every command that offers
// the flag.

// AddJSON gives a command the --json flag.
func AddJSON(cmd *cobra.Command) {
	cmd.Flags().Bool("json", false,
		"Emit the result as a single JSON document on stdout instead of a table")
}

// WantsJSON reports whether this run should emit JSON.
func WantsJSON(cmd *cobra.Command) bool { return Switched(cmd, "json") }

// EmitDocument writes exactly one JSON document to stdout.
//
// Indented, because the first reader of any of this is a person checking what
// the shape is, and `jq` does not mind. HTML escaping is off: these payloads
// carry URLs and titles, and `&` in a web_url is noise for every consumer
// to undo.
//
// A trailing newline, so the output is a well-formed line on a terminal and
// concatenating two documents is still visibly two documents.
//
// It knows nothing about the envelope on purpose. What goes in the document is
// the command layer's business; what this owns is that stdout holds one JSON
// value and no prose.
func EmitDocument(cmd *cobra.Command, document any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("could not write the JSON document: %w", err)
	}

	return nil
}

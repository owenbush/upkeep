package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// failingWriter is a stdout that cannot be written to.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func aJSONCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "demo"}
	AddJSON(cmd)

	return cmd
}

// The flag is off unless asked for, because a table is what a person expects.
func TestJSONIsOptIn(t *testing.T) {
	cmd := aJSONCommand()

	if WantsJSON(cmd) {
		t.Error("JSON was on without the flag")
	}

	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("setting the flag: %v", err)
	}
	if !WantsJSON(cmd) {
		t.Error("the flag did not take")
	}
}

// A command with no such flag is not in JSON mode, rather than panicking.
//
// Worth holding: Switched reads a flag that may not exist, and every command
// shares the helper.
func TestACommandWithoutTheFlagIsNotInJSONMode(t *testing.T) {
	if WantsJSON(&cobra.Command{Use: "plain"}) {
		t.Error("a command with no --json flag claimed JSON mode")
	}
}

// One document, indented, newline-terminated, and with URLs left alone.
func TestEmitDocumentWritesOneReadableDocument(t *testing.T) {
	cmd := aJSONCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)

	payload := map[string]string{
		"web_url": "https://git.drupalcode.org/project/pathauto/-/merge_requests/40?a=1&b=2",
	}
	if err := EmitDocument(cmd, payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rendered := out.String()
	if !strings.HasSuffix(rendered, "\n") {
		t.Error("the document does not end in a newline")
	}
	if !strings.Contains(rendered, "\n  ") {
		t.Errorf("the document is not indented: %q", rendered)
	}
	// HTML escaping off: &amp; in a URL is noise every consumer has to undo.
	if strings.Contains(rendered, "\\u0026") {
		t.Errorf("the ampersand was escaped: %q", rendered)
	}

	decoder := json.NewDecoder(strings.NewReader(rendered))
	var back map[string]string
	if err := decoder.Decode(&back); err != nil {
		t.Fatalf("it does not parse: %v", err)
	}
	if decoder.More() {
		t.Error("more than one document was written")
	}
	if back["web_url"] != payload["web_url"] {
		t.Errorf("the URL did not round-trip: %q", back["web_url"])
	}
}

// A stdout that will not take it is an error, not a silent success.
//
// The caller turns this into exit 2. A consumer reading an empty stdout and a
// zero exit code would conclude the command found nothing.
func TestEmitDocumentReportsAWriteItCannotMake(t *testing.T) {
	cmd := aJSONCommand()
	cmd.SetOut(failingWriter{})

	err := EmitDocument(cmd, map[string]int{"n": 1})
	if err == nil {
		t.Fatal("a failed write was reported as success")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("the cause is missing: %v", err)
	}
}

// A payload JSON cannot represent is an error too, rather than a half-written
// document.
func TestEmitDocumentReportsAnUnencodablePayload(t *testing.T) {
	cmd := aJSONCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := EmitDocument(cmd, map[string]any{"fn": func() {}}); err == nil {
		t.Fatal("an unencodable payload was accepted")
	}
}

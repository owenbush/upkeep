package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/gitlab"
)

// Reading needs no credential: drupalcode serves a public project's merge
// requests, refs and raw files anonymously, and requiring a token to *look*
// was a restriction upkeep imposed rather than one GitLab does.
func TestAReadOnlyClientIsHandedOutWithNoToken(t *testing.T) {
	t.Setenv(gitlab.DefaultEnvVar, "")
	t.Setenv("HOME", t.TempDir())
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), ".config"))

	var noted []string
	clients := NewResolvedClients(nil)
	client := clients.ReadOnly(func(note string) { noted = append(noted, note) })

	if client == nil {
		t.Fatal("no client was handed out for a read")
	}
	// And the cost is said once: a private project answers an anonymous read
	// with 404 rather than 401, so a module you can see while signed in reads
	// as missing.
	said := strings.Join(noted, "\n")
	if !strings.Contains(said, "anonymously") {
		t.Errorf("the degraded mode was silent: %q", said)
	}
	if strings.Contains(said, "s3cr3t") {
		t.Errorf("the note carried token material: %q", said)
	}
}

// With a token it is used, and nothing is said: there is nothing degraded to
// report.
func TestAReadOnlyClientWithATokenSaysNothing(t *testing.T) {
	t.Setenv(gitlab.DefaultEnvVar, "a-token-long-enough")

	var noted []string
	NewResolvedClients(nil).ReadOnly(func(note string) { noted = append(noted, note) })

	if len(noted) != 0 {
		t.Errorf("a configured token still warned: %v", noted)
	}
}

// Writing without a credential is an infrastructure failure, not a degraded
// mode: there is no verdict to produce.
func TestWritingWithNoTokenIsARefusalNamingHowToFixIt(t *testing.T) {
	t.Setenv(gitlab.DefaultEnvVar, "")
	t.Setenv("HOME", t.TempDir())
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), ".config"))

	client, err := NewResolvedClients(nil).Authenticated(func(string) {})

	if err == nil {
		t.Fatal("a write was allowed with no credential")
	}
	if client != nil {
		t.Error("a client was handed out anyway")
	}
	if !strings.Contains(err.Error(), gitlab.DefaultEnvVar) {
		t.Errorf("the refusal does not say where to put one: %v", err)
	}
}

// With a token, a writing client is handed out.
func TestWritingWithATokenGetsAClient(t *testing.T) {
	t.Setenv(gitlab.DefaultEnvVar, "a-token-long-enough")

	client, err := NewResolvedClients(nil).Authenticated(func(string) {})
	if err != nil || client == nil {
		t.Errorf("got %v (%v)", client, err)
	}
}

// A token file readable by other accounts is worth saying, and the warning
// never carries the token.
func TestAWorldReadableTokenFileIsWarnedAbout(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root's file modes say nothing about exposure")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(gitlab.DefaultEnvVar, "")

	path := filepath.Join(home, ".config", "upkeep", "drupal-pat")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("a-token-long-enough\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var warnings []string
	NewResolvedClients(func(warning string) { warnings = append(warnings, warning) }).
		ReadOnly(func(string) {})

	said := strings.Join(warnings, "\n")
	if said == "" {
		t.Error("an exposed token file was not mentioned")
	}
	if strings.Contains(said, "a-token-long-enough") {
		t.Errorf("the warning carried the token: %q", said)
	}
}

// The two seams route to the two paths, and both report where the command's
// diagnostics go.
func TestTheClientSeamsReportWhereDiagnosticsGo(t *testing.T) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd := &cobra.Command{Use: "thing"}
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	clients := &recordingClients{}

	if ReadingClient(cmd, clients) == nil {
		t.Error("no read-only client")
	}
	if clients.asked != "read-only" {
		t.Errorf("a reading command asked for %q", clients.asked)
	}

	if _, err := WritingClient(cmd, clients); err != nil {
		t.Errorf("writing: %v", err)
	}
	if clients.asked != "authenticated" {
		t.Errorf("a writing command asked for %q", clients.asked)
	}

	clients.report("a note about how this is degraded")
	if out.Len() != 0 {
		t.Errorf("a note landed in the answer: %q", out)
	}
	if !strings.Contains(errOut.String(), "degraded") {
		t.Errorf("a note went nowhere: %q", errOut)
	}
}

// recordingClients records which path a command asked for.
type recordingClients struct {
	asked  string
	report gitlab.Report
}

func (c *recordingClients) ReadOnly(report gitlab.Report) *gitlab.Client {
	c.asked, c.report = "read-only", report

	return gitlab.NewClient(nil, "", "", "")
}

func (c *recordingClients) Authenticated(report gitlab.Report) (*gitlab.Client, error) {
	c.asked, c.report = "authenticated", report

	return gitlab.NewClient(nil, "", "", ""), nil
}

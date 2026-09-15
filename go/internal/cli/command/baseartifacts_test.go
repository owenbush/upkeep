package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/workflow"
)

// anArtifactSet writes a complete set for one core, or leaves pieces out.
func anArtifactSet(t *testing.T, where *cockpit.Cockpit, coreMajor string, omit ...string) {
	t.Helper()

	layout := baseartifact.NewLayout(where.BaseArtifactsPath())
	paths, err := layout.PathsFor(coreMajor)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	if err := os.MkdirAll(paths.VersionDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	skipped := map[string]bool{}
	for _, piece := range omit {
		skipped[piece] = true
	}

	if !skipped["tree"] {
		if err := os.MkdirAll(paths.Tree, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if !skipped["dump"] {
		if err := os.WriteFile(paths.Dump, []byte("-- a database"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if !skipped["meta"] {
		meta := baseartifact.Meta{
			CoreVersion: coreMajor + ".4.6", CoreMajor: coreMajor,
			PHPVersion: "8.3.14", DBEngine: "mariadb:10.11",
			BuiltAt: time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC),
		}
		contents, err := meta.ToYAML()
		if err != nil {
			t.Fatalf("meta: %v", err)
		}
		if err := os.WriteFile(paths.Meta, []byte(contents), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if !skipped["marker"] {
		if err := os.WriteFile(paths.CanonicalMarker, []byte("canonical\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

// A complete set reports what it was built from, which is what every later
// staleness decision is made against.
func TestBaseArtifactsStatusReportsWhatEachSetWasBuiltFrom(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}
	where, _ := cockpit.New(root)
	anArtifactSet(t, where, "11")
	anArtifactSet(t, where, "12")

	code, stdout, _ := invoke(t, "base-artifacts:status", "--cockpit="+root)
	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}

	for _, expected := range []string{
		"11", "12", "complete (canonical)", "11.4.6", "12.4.6",
		"2026-06-01", "8.3.14", "mariadb:10.11",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the listing is missing %q:\n%s", expected, stdout)
		}
	}
}

// An incomplete set names what is missing from it: the recovery differs — a
// missing dump is a rebuild, a missing marker is a file to touch — and an
// operator staring at a broken set needs to know which.
func TestAnIncompleteSetNamesWhatIsMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}
	where, _ := cockpit.New(root)
	anArtifactSet(t, where, "11", "dump", "marker")

	code, stdout, _ := invoke(t, "base-artifacts:status", "--cockpit="+root)
	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}

	if !strings.Contains(stdout, "incomplete: missing") {
		t.Errorf("an incomplete set read as usable:\n%s", stdout)
	}
	if !strings.Contains(stdout, baseartifact.DumpFilename) {
		t.Errorf("the missing dump was not named:\n%s", stdout)
	}
	if !strings.Contains(stdout, baseartifact.CanonicalMarker) {
		t.Errorf("the missing marker was not named:\n%s", stdout)
	}
}

// A set with no readable meta still occupies a row, with dashes rather than
// blanks: an empty cell reads as a field somebody forgot rather than one that
// cannot be known.
func TestASetWithNoMetaIsListedWithDashes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}
	where, _ := cockpit.New(root)
	anArtifactSet(t, where, "11", "meta")

	code, stdout, _ := invoke(t, "base-artifacts:status", "--cockpit="+root)
	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}

	line := lineContaining(stdout, "incomplete")
	if line == "" {
		t.Fatalf("the set was not listed:\n%s", stdout)
	}
	if strings.Count(line, "-") < 4 {
		t.Errorf("the unknowable fields were not marked as such: %q", line)
	}
}

// Nothing built yet says how to build something, rather than printing an empty
// table.
func TestNoArtifactsSaysHowToBuildOne(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}

	code, stdout, _ := invoke(t, "base-artifacts:status", "--cockpit="+root)
	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "base-artifacts:build") {
		t.Errorf("it did not say how to build one:\n%s", stdout)
	}
	if strings.Contains(stdout, "Core") {
		t.Errorf("it printed an empty table:\n%s", stdout)
	}
}

// A build in progress is not a core anybody can be offered, so it never shows
// up as one.
func TestABuildInProgressIsNotListed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}
	where, _ := cockpit.New(root)
	anArtifactSet(t, where, "11")
	if err := os.MkdirAll(
		filepath.Join(where.BaseArtifactsPath(), ".building-d12-deadbeef", "12"), 0o755,
	); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, stdout, _ := invoke(t, "base-artifacts:status", "--cockpit="+root)

	if strings.Contains(stdout, "12") {
		t.Errorf("a half-built set was offered as a core:\n%s", stdout)
	}
}

// The command name is the PHP's, because it is in scripts, in the docs, and in
// the recovery lines other commands print.
func TestTheBaseArtifactCommandKeepsItsColonName(t *testing.T) {
	root := NewRootFor(noVolumes{}, noSizer)

	found := false
	for _, command := range root.Commands() {
		if command.Name() == "base-artifacts:status" {
			found = true
		}
	}
	if !found {
		t.Error("base-artifacts:status was renamed")
	}
}

// A field the meta cannot supply is a dash, not a blank: an empty cell reads
// as a field somebody forgot rather than one that cannot be known.
func TestAnUnknowableArtifactFieldIsADash(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}
	where, _ := cockpit.New(root)

	// A meta that parses but carries no DB engine — which is what an install
	// whose runtime would not say produces.
	anArtifactSet(t, where, "11", "meta")
	layout := baseartifact.NewLayout(where.BaseArtifactsPath())
	paths, _ := layout.PathsFor("11")
	meta := baseartifact.Meta{
		CoreVersion: "11.4.6", CoreMajor: "11", PHPVersion: "8.3.14",
		DBEngine: "", BuiltAt: time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC),
	}
	contents, err := meta.ToYAML()
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if err := os.WriteFile(paths.Meta, []byte(contents), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, stdout, _ := invoke(t, "base-artifacts:status", "--cockpit="+root)

	if got := columnIn(stdout, "11.4.6", "DB engine"); got != "-" {
		t.Errorf("a field the meta does not carry rendered as %q:\n%s", got, stdout)
	}
	if got := columnIn(stdout, "11.4.6", "PHP"); got != "8.3.14" {
		t.Errorf("a field it does carry rendered as %q:\n%s", got, stdout)
	}
}

// A registry that exists but does not parse is reported the way every other
// command reports it, rather than being discovered later.
func TestBaseArtifactsStatusProvesTheRegistryParses(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}
	where, _ := cockpit.New(root)
	anArtifactSet(t, where, "11")
	if err := os.WriteFile(where.RegistryPath(), []byte("\tnot: [yaml"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, stdout, stderr := invoke(t, "base-artifacts:status", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if stdout != "" {
		t.Errorf("it listed from a cockpit it could not read: %q", stdout)
	}
	if stderr == "" {
		t.Error("it failed silently")
	}
}

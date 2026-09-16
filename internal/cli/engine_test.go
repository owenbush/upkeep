package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cockpit"
)

// The build's progress goes where the engine's does: stage lines to stderr,
// the child's own output through the status line.
func TestTheArtifactBuilderIsWiredToStderr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	where, err := cockpit.New(filepath.Join(home, "cockpit"))
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}

	factory := &stubFactory{}
	cmd := &cobra.Command{Use: "thing"}
	AddScratchDir(cmd)
	AddVerbose(cmd)
	cmd.SetErr(&bytes.Buffer{})

	builder, err := ArtifactBuilder(cmd, factory, where)
	if err != nil {
		t.Fatalf("builder: %v", err)
	}
	if builder == nil {
		t.Fatal("no builder")
	}
	if !strings.HasPrefix(factory.scratch, home) {
		t.Errorf("the scratch directory is %q, outside %q", factory.scratch, home)
	}
	for what, wired := range map[string]bool{
		"stage log":   factory.stageLog != nil,
		"process log": factory.processed != nil,
		"idle hook":   factory.idled != nil,
	} {
		if !wired {
			t.Errorf("the %s was not wired", what)
		}
	}

	// And a stage line actually lands on stderr: upkeep's own progress is
	// diagnostics, so a build piped somewhere keeps its payload clean.
	said := &bytes.Buffer{}
	cmd.SetErr(said)
	factory.stageLog("Resolving the base tree ...")
	if !strings.Contains(said.String(), "Resolving the base tree") {
		t.Errorf("the stage line did not reach stderr: %q", said)
	}
}

// The scratch directory is held to the same $HOME containment rule as the
// projects root, and the refusal names the flag rather than the path rule.
func TestTheArtifactBuilderRefusesAScratchDirOutsideHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	where, err := cockpit.New(t.TempDir())
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}

	cmd := &cobra.Command{Use: "thing"}
	AddScratchDir(cmd)
	AddVerbose(cmd)
	if err := cmd.ParseFlags([]string{"--scratch-dir=" + t.TempDir()}); err != nil {
		t.Fatalf("flags: %v", err)
	}

	builder, err := ArtifactBuilder(cmd, &stubFactory{}, where)
	if err == nil {
		t.Fatal("it accepted a scratch directory the container runtime cannot see")
	}
	if builder != nil {
		t.Error("it returned a builder alongside the refusal")
	}
	if !strings.Contains(err.Error(), "--scratch-dir") {
		t.Errorf("the refusal does not name the flag: %v", err)
	}
}

// The default is under $HOME, which is what makes it pass that rule without
// anybody having to know about it.
func TestTheDefaultScratchDirectoryIsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := DefaultScratchDir(); !strings.HasPrefix(got, home) {
		t.Errorf("the default is %q, outside %q", got, home)
	}

	// Nowhere right to put it is still somewhere: the containment rule
	// refuses it a moment later, naming the flag, which beats failing inside
	// a container.
	t.Setenv("HOME", "")
	if DefaultScratchDir() == "" {
		t.Error("it defaulted to nothing")
	}
}

// And the flag itself carries that default, so a bare run is a working run.
func TestTheScratchDirFlagDefaultsToTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cmd := &cobra.Command{Use: "thing"}
	AddScratchDir(cmd)

	if got := Flag(cmd, FlagScratchDir); !strings.HasPrefix(got, home) {
		t.Errorf("the flag defaults to %q", got)
	}
}

// A base-artifacts directory that cannot be read is no cores, not a crash:
// every caller of this treats the answer as "what is available", and an error
// means nothing is.
func TestCoresOnDiskIsEmptyWhenTheDirectoryCannotBeRead(t *testing.T) {
	where, err := cockpit.New(t.TempDir())
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}
	artifacts := where.BaseArtifactsPath()
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(artifacts, 0o000); err != nil {
		t.Skipf("cannot make the directory unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(artifacts, 0o700) })
	if _, err := os.ReadDir(artifacts); err == nil {
		t.Skip("the directory is still readable; probably running as root")
	}

	if cores := CoresOnDisk(where); len(cores) != 0 {
		t.Errorf("it read %v out of an unreadable directory", cores)
	}
}

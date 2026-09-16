package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/security"
)

// aCockpitAt is a cockpit rooted under the given directory.
func aCockpitAt(t *testing.T, root string) *cockpit.Cockpit {
	t.Helper()

	where, err := cockpit.New(root)
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}

	return where
}

// The factory resolves the projects root by the documented order, and the
// engine it builds is pointed at it.
func TestTheFactoryResolvesTheProjectsRootItWasGiven(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	where := aCockpitAt(t, filepath.Join(home, "cockpit"))
	explicit := filepath.Join(home, "elsewhere")
	if err := os.MkdirAll(explicit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	engine, err := NewDdevContribFactory(nil).Build(where, explicit, nil, nil, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	built, isOurs := engine.(*DdevContrib)
	if !isOurs {
		t.Fatalf("got %T", engine)
	}
	if built.projectsRoot != explicit {
		t.Errorf("projects root %q, want %q", built.projectsRoot, explicit)
	}
	// And the artifact layout comes from the cockpit it was handed.
	if built.layout.Dir != where.BaseArtifactsPath() {
		t.Errorf("layout %q", built.layout.Dir)
	}
}

// A projects root outside $HOME can never work: the tree is bind-mounted into
// the container runtime's VM, and the providers only share the home directory.
// Refused at construction, before anything is provisioned.
func TestTheFactoryRefusesAProjectsRootOutsideHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	where := aCockpitAt(t, filepath.Join(home, "cockpit"))

	_, err := NewDdevContribFactory(nil).Build(where, "/tmp/somewhere-else", nil, nil, nil)
	if err == nil {
		t.Fatal("it built an engine that could never start")
	}
	if !strings.Contains(err.Error(), home) {
		t.Errorf("the refusal does not say what the rule is: %v", err)
	}
}

// Every child a built engine starts goes through the runner the factory made,
// which is where the credential is stripped and the output redacted.
func TestTheFactoryWiresRedactionIntoEveryChild(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	where := aCockpitAt(t, filepath.Join(home, "cockpit"))

	var printed []string
	engine, err := NewDdevContribFactory(security.NewRedactor("s3cr3t-token-value")).Build(
		where, filepath.Join(home, "projects"),
		nil,
		func(line string) { printed = append(printed, line) },
		nil,
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	built := engine.(*DdevContrib)
	// A real child, because the redaction lives in the runner and a fake one
	// would be testing the fake.
	out, err := built.runner.Run(
		[]string{"sh", "-c", "echo token=s3cr3t-token-value"}, "", 0,
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out, "s3cr3t-token-value") {
		t.Errorf("a credential reached the caller unredacted: %q", out)
	}
	if strings.Contains(strings.Join(printed, "\n"), "s3cr3t-token-value") {
		t.Errorf("a credential reached the log unredacted: %v", printed)
	}
}

// A nil log is the caller not wanting progress, not a caller wanting a crash.
func TestABuiltEngineWithNoLogsStillWorks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	where := aCockpitAt(t, filepath.Join(home, "cockpit"))

	engine, err := NewDdevContribFactory(nil).Build(
		where, filepath.Join(home, "projects"), nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Reaches the log on its way through, which is what would panic.
	if got := engine.ResolveEnvPath("pathauto", "11"); got != "" {
		t.Errorf("got %q", got)
	}
}

// The artifact builder is assembled here for the same reason the engine is:
// it needs a throwaway project, which is engine knowledge, and it must filter
// child output through the same redactor.
func TestTheFactoryBuildsAnArtifactBuilderThatRedactsToo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	where := aCockpitAt(t, filepath.Join(home, "cockpit"))
	scratch := filepath.Join(home, ".upkeep", "scratch")

	var printed []string
	builder := NewDdevContribFactory(security.NewRedactor("s3cr3t-token-value")).BuildArtifacts(
		where, scratch,
		nil,
		func(line string) { printed = append(printed, line) },
		nil,
	)
	if builder == nil {
		t.Fatal("no builder")
	}

	// A stand-in composer that prints the credential, so redaction is
	// observable rather than merely absent: the real one is not here, and a
	// child that prints nothing would let an unredacted runner pass.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "composer"),
		[]byte("#!/bin/sh\necho token=s3cr3t-token-value\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	// The resolve is the build's first child, so a failing one is the
	// cheapest way to reach the runner the factory wired in.
	if _, err := builder.Build("11", false, ""); err == nil {
		t.Error("a build over a failing composer succeeded")
	}
	if printed == nil {
		t.Fatal("the child's output never reached the log")
	}
	if strings.Contains(strings.Join(printed, "\n"), "s3cr3t-token-value") {
		t.Errorf("a credential reached the log unredacted: %v", printed)
	}
}

// A nil log is the caller not wanting progress here too.
func TestABuiltArtifactBuilderWithNoLogsStillWorks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// HOME alone does not isolate the token file: the resolver prefers
	// XDG_CONFIG_HOME, so a machine that sets it — every GitHub runner —
	// would read the developer's own config instead of this one.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	where := aCockpitAt(t, filepath.Join(home, "cockpit"))

	builder := NewDdevContribFactory(nil).BuildArtifacts(
		where, filepath.Join(home, "scratch"), nil, nil, nil,
	)

	// An impossible core major refuses before anything runs, which is enough
	// to reach the log on the way past.
	if _, err := builder.Build("eleven", false, ""); err == nil {
		t.Error("it built artifacts for a core major that cannot name a directory")
	}
}

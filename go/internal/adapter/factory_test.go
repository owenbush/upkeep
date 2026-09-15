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

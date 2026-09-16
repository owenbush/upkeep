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

// aCompletableCockpit is a cockpit with a watchlist, artifacts, fixtures and a
// provisioned environment for a module nobody registered.
func aCompletableCockpit(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	write := func(path, contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	where, err := cockpit.New(root)
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}

	write(where.RegistryPath(), "modules:\n"+
		"  token:\n    project: project/token\n    core_versions: [\"10\", \"11\"]\n"+
		"  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\"]\n")
	write(filepath.Join(where.FixturesPath(), "sample-content.sql.gz"), "dump")
	write(filepath.Join(where.FixturesPath(), "baseline.sql.gz"), "dump")
	write(filepath.Join(where.FixturesPath(), "notes.txt"), "not a fixture")
	if err := os.MkdirAll(filepath.Join(where.BaseArtifactsPath(), "12"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// An environment for a module the registry does not carry.
	if err := os.MkdirAll(
		filepath.Join(where.ProjectsPath(), "upkeep-field-visibility-conditions-d11"), 0o755,
	); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	return root
}

// completing runs a completion function against a command with the cockpit
// already given.
func completing(
	t *testing.T,
	complete func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective),
	root string, args []string, typed string,
) []string {
	t.Helper()

	cmd := &cobra.Command{Use: "thing"}
	AddCockpit(cmd)
	AddProjectsRoot(cmd)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--cockpit=" + root})
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	values, directive := complete(cmd, args, typed)

	// Never a file listing: without this the shell falls back to the current
	// directory, so a completion that knows nothing looks like upkeep
	// suggesting file names.
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Errorf("the completion allowed a file listing (directive %d)", directive)
	}

	return values
}

// The watchlist and the environments on disk, because the registry is a
// watchlist rather than a gate: a module somebody has already worked on is
// worth offering even if nobody added it.
func TestModuleCompletionOffersWatchedAndProvisionedModules(t *testing.T) {
	root := aCompletableCockpit(t)

	got := completing(t, CompleteModule, root, nil, "")

	want := []string{"field_visibility_conditions", "pathauto", "token"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A project directory's hyphens go back to underscores, which is the one lossy
// step in the other direction — and lossless in practice, because a hyphenated
// machine name is not legal.
func TestAProjectDirectoryNameBecomesAMachineNameAgain(t *testing.T) {
	for directory, want := range map[string]string{
		"upkeep-pathauto-d11":                    "pathauto",
		"upkeep-field-visibility-conditions-d11": "field_visibility_conditions",
		"upkeep-a-b-d9":                          "a_b",
		"upkeep-pathauto":                        "",
		"upkeep--d11":                            "",
		"pathauto-d11":                           "",
		"":                                       "",
	} {
		if got := moduleInProjectName(directory); got != want {
			t.Errorf("%q gave %q, want %q", directory, got, want)
		}
	}
}

// Completion filters to what has been typed, which is what makes it feel like
// completion rather than a menu.
func TestCompletionFiltersToWhatIsTyped(t *testing.T) {
	root := aCompletableCockpit(t)

	if got := completing(t, CompleteModule, root, nil, "pa"); strings.Join(got, ",") != "pathauto" {
		t.Errorf("got %v", got)
	}
	if got := completing(t, CompleteModule, root, nil, "zz"); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// The cores the named module tracks, so it offers 10 and 11 rather than every
// version any module uses.
func TestCoreCompletionNarrowsToTheNamedModule(t *testing.T) {
	root := aCompletableCockpit(t)

	if got := completing(t, CompleteTargetCore, root, []string{"pathauto"}, ""); strings.Join(got, ",") != "11" {
		t.Errorf("pathauto got %v", got)
	}
	if got := completing(t, CompleteTargetCore, root, []string{"token"}, ""); strings.Join(got, ",") != "10,11" {
		t.Errorf("token got %v", got)
	}
}

// With no module named yet, everything a run could use is fair game — what is
// built here, plus what the watchlist tracks.
func TestCoreCompletionWithNoModuleOffersEverythingUsable(t *testing.T) {
	root := aCompletableCockpit(t)

	got := completing(t, CompleteTargetCore, root, nil, "")

	if strings.Join(got, ",") != "10,11,12" {
		t.Errorf("got %v — expected the tracked cores and the one built here", got)
	}
}

// A module nobody registered falls back to the same wide list, rather than to
// nothing: its cores are a fact about the disk, which the wide list already
// carries.
func TestCoreCompletionForAnUnregisteredModuleStillOffersSomething(t *testing.T) {
	root := aCompletableCockpit(t)

	if got := completing(t, CompleteTargetCore, root, []string{"webform"}, ""); len(got) == 0 {
		t.Error("an unregistered module completed to nothing")
	}
}

// Fixtures come from the shared library, and only the dumps in it.
func TestFixtureCompletionOffersTheLibrary(t *testing.T) {
	root := aCompletableCockpit(t)

	got := completing(t, CompleteFixture, root, nil, "")

	if strings.Join(got, ",") != "baseline,sample-content" {
		t.Errorf("got %v", got)
	}
}

// Nothing here may fail loudly: this runs on every press of TAB, and an error
// across somebody's prompt is worse than no suggestions.
func TestCompletionIsSilentWhateverIsWrong(t *testing.T) {
	broken := map[string]string{
		"no cockpit at all": filepath.Join(t.TempDir(), "nowhere"),
		"an empty one":      t.TempDir(),
	}

	unparseable := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(unparseable, cockpit.RegistryFilename), []byte("\tnot: [yaml"), 0o644,
	); err != nil {
		t.Fatalf("write: %v", err)
	}
	broken["an unparseable registry"] = unparseable

	for name, root := range broken {
		for what, complete := range map[string]func(
			*cobra.Command, []string, string,
		) ([]string, cobra.ShellCompDirective){
			"modules":  CompleteModule,
			"cores":    CompleteTargetCore,
			"fixtures": CompleteFixture,
		} {
			got := completing(t, complete, root, nil, "")
			if len(got) != 0 && name != "an empty one" {
				t.Errorf("%s: completing %s suggested %v", name, what, got)
			}
		}
	}
}

// An unreadable directory is the same as one that is not there: no
// suggestions, no complaint.
func TestCompletionIsSilentOnAnUnreadableCockpit(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory regardless of its mode")
	}

	root := aCompletableCockpit(t)
	where, _ := cockpit.New(root)
	if err := os.Chmod(where.FixturesPath(), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(where.FixturesPath(), 0o755) })

	if got := completing(t, CompleteFixture, root, nil, ""); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// Values nothing local can enumerate are deliberately not completed: a merge
// request IID would be a network round trip per keystroke, for a number the
// operator is copying from a page they already have open.
func TestASecondArgumentIsNeverCompleted(t *testing.T) {
	root := aCompletableCockpit(t)
	cmd := &cobra.Command{Use: "check"}
	AddCockpit(cmd)
	AddProjectsRoot(cmd)
	AddTargetCore(cmd)
	AddModuleCompletion(cmd)
	cmd.SetArgs([]string{"--cockpit=" + root})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	values, directive := cmd.ValidArgsFunction(cmd, []string{"pathauto"}, "")
	if len(values) != 0 {
		t.Errorf("a merge request IID was completed: %v", values)
	}
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Error("it fell back to listing files")
	}

	// And the first argument still completes.
	if values, _ := cmd.ValidArgsFunction(cmd, nil, "pa"); len(values) != 1 {
		t.Errorf("the module argument stopped completing: %v", values)
	}
}

// Registering completion for a flag a command does not have is a mistake worth
// nothing at a TAB press, rather than a panic.
func TestRegisteringCompletionForAnAbsentFlagIsHarmless(t *testing.T) {
	cmd := &cobra.Command{Use: "thing"}

	AddFixtureCompletion(cmd)
	AddModuleCompletion(cmd)

	if cmd.ValidArgsFunction == nil {
		t.Error("the argument completion was not wired")
	}
}

// A cockpit that cannot be resolved at all suggests nothing — the same silence
// as one that is merely empty, because at a TAB press the difference is not
// worth an error across the prompt.
func TestCompletionIsSilentWhenTheCockpitCannotBeResolved(t *testing.T) {
	cmd := &cobra.Command{Use: "thing"}
	AddCockpit(cmd)
	AddProjectsRoot(cmd)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--cockpit="})
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	for what, complete := range map[string]func(
		*cobra.Command, []string, string,
	) ([]string, cobra.ShellCompDirective){
		"modules":  CompleteModule,
		"cores":    CompleteTargetCore,
		"fixtures": CompleteFixture,
	} {
		values, directive := complete(cmd, nil, "")
		if len(values) != 0 {
			t.Errorf("completing %s suggested %v", what, values)
		}
		if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
			t.Errorf("completing %s fell back to listing files", what)
		}
	}
}

// A file in the projects root is not an environment, whatever it is called.
func TestAFileInTheProjectsRootIsNotAModule(t *testing.T) {
	root := aCompletableCockpit(t)
	where, _ := cockpit.New(root)
	if err := os.WriteFile(
		filepath.Join(where.ProjectsPath(), "upkeep-webform-d11"), []byte("not a directory"), 0o644,
	); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, name := range completing(t, CompleteModule, root, nil, "") {
		if name == "webform" {
			t.Error("a file was offered as a provisioned module")
		}
	}
}

// The survey commands take the module as a flag, so it completes there too —
// same values, different place on the line, and wired by its own call because
// AddModuleCompletion wires an *argument*.
func TestAModuleFlagCompletesLikeAModuleArgument(t *testing.T) {
	root := aCompletableCockpit(t)

	cmd := &cobra.Command{Use: "survey"}
	AddCockpit(cmd)
	AddProjectsRoot(cmd)
	cmd.Flags().String("module", "", "")
	AddModuleFlagCompletion(cmd)

	complete, registered := completionForFlag(cmd, "module")
	if !registered {
		t.Fatal("the --module flag has no completion")
	}

	if suggestions := completing(t, complete, root, nil, ""); len(suggestions) == 0 {
		t.Fatal("no module was suggested")
	}
}

// completionForFlag is the function cobra will call for a flag's values.
func completionForFlag(cmd *cobra.Command, flag string) (
	func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective), bool,
) {
	complete := cmd.GetFlagCompletionFunc
	if complete == nil {
		return nil, false
	}

	return complete(flag)
}

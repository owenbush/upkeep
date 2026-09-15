package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cockpit"
)

// withFlags is a command carrying the given shared flags, already parsed.
func withFlags(t *testing.T, add func(*cobra.Command), args ...string) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{Use: "thing", RunE: func(*cobra.Command, []string) error { return nil }}
	add(cmd)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	return cmd
}

// aCockpit is a cockpit on disk with a registry and some artifacts.
func aCockpit(t *testing.T, registry string, cores ...string) *cockpit.Cockpit {
	t.Helper()

	where, err := cockpit.New(t.TempDir())
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}
	if err := os.WriteFile(where.RegistryPath(), []byte(registry), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, core := range cores {
		if err := os.MkdirAll(filepath.Join(where.BaseArtifactsPath(), core), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	return where
}

const oneModule = "modules:\n  pathauto:\n    project: project/pathauto\n    core_versions: [\"11\", \"10\"]\n"

// --version means the target core on the commands that act, and a filter on
// the ones that report. Same spelling, different meaning, each with one
// canonical wording — so the two must not describe themselves the same way.
func TestTheTwoMeaningsOfTheVersionFlagAreWordedApart(t *testing.T) {
	selector := withFlags(t, AddTargetCore)
	filter := withFlags(t, AddCoreFilter)

	selecting := selector.Flags().Lookup(FlagVersion).Usage
	filtering := filter.Flags().Lookup(FlagVersion).Usage

	if selecting == filtering {
		t.Errorf("both meanings describe themselves identically: %q", selecting)
	}
	if !strings.Contains(selecting, "Target core") {
		t.Errorf("the selector does not say it selects: %q", selecting)
	}
	if !strings.Contains(filtering, "Only show") {
		t.Errorf("the filter does not say it filters: %q", filtering)
	}
	// Neither may read as an application-version flag.
	for _, usage := range []string{selecting, filtering} {
		if strings.Contains(strings.ToLower(usage), "upkeep version") {
			t.Errorf("--version reads as the application version: %q", usage)
		}
	}
}

// The default is to update the base branch, because not updating is what
// produced a green local check and a red pipeline.
func TestTheBaseIsRefreshedUnlessAskedNotTo(t *testing.T) {
	if got := BaseRefresh(withFlags(t, AddNoUpdate)); got != adapter.RefreshUpdate {
		t.Errorf("the default is %v, want an update", got)
	}
	if got := BaseRefresh(withFlags(t, AddNoUpdate, "--no-update")); got != adapter.RefreshSkip {
		t.Errorf("--no-update gave %v", got)
	}
}

// The cores a derived module can use are the base artifacts on this machine.
func TestTheUsableCoresAreWhatIsBuiltHere(t *testing.T) {
	where := aCockpit(t, oneModule, "10", "11", "12")

	cores := CoresOnDisk(where)
	if len(cores) != 3 || cores[0] != "10" || cores[2] != "12" {
		t.Errorf("got %v — expected ascending majors", cores)
	}

	// An unreadable or absent artifacts directory yields none rather than
	// failing: a module with a registry entry does not need this at all, and
	// refusing there would turn a directory problem into a refusal to work on
	// a registered module.
	empty, err := cockpit.New(t.TempDir())
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}
	if got := CoresOnDisk(empty); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// The registry is a watchlist, not a gate: an unregistered module resolves
// from the disk, and a registered one's own list wins.
func TestASubjectModuleResolvesRegisteredOrNot(t *testing.T) {
	where := aCockpit(t, oneModule, "10", "11", "12")
	modules, err := Modules(where)
	if err != nil {
		t.Fatalf("modules: %v", err)
	}

	registered, err := ResolveModule(where, modules, "pathauto")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// A maintainer's core_versions is a deliberate statement and outranks
	// anything inferred — 12 is on disk and not in the entry.
	if len(registered.CoreVersions) != 2 || !registered.Watched {
		t.Errorf("got %+v", registered)
	}

	derived, err := ResolveModule(where, modules, "token")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if derived.Watched {
		t.Error("an unregistered module reported itself as watched")
	}
	// Newest first, because the first entry is the default.
	if len(derived.CoreVersions) == 0 || derived.CoreVersions[0] != "12" {
		t.Errorf("got %v — expected newest first", derived.CoreVersions)
	}

	// A name that cannot be a Drupal machine name is refused outright.
	if _, err := ResolveModule(where, modules, "../escape"); err == nil {
		t.Error("an impossible name resolved")
	}
}

// The target core: --version when given and tracked, otherwise the first in
// the module's list.
func TestTheTargetCoreComesFromTheFlagOrTheModulesOwnOrder(t *testing.T) {
	module := cockpit.Module{
		Name: "pathauto", CoreVersions: []string{"11", "10"}, Watched: true,
	}

	if got, err := TargetCore(withFlags(t, AddTargetCore), module); err != nil || got != "11" {
		t.Errorf("default is %q (%v)", got, err)
	}
	if got, err := TargetCore(withFlags(t, AddTargetCore, "--version=10"), module); err != nil || got != "10" {
		t.Errorf("explicit is %q (%v)", got, err)
	}
	if _, err := TargetCore(withFlags(t, AddTargetCore, "--version=9"), module); err == nil {
		t.Error("an untracked core was accepted")
	}
}

// A command that needs a cockpit but not its contents still reports a
// malformed registry the way every other command does.
func TestRequiringACockpitProvesItsRegistryParses(t *testing.T) {
	good := aCockpit(t, oneModule)
	cmd := withFlags(t, AddCockpit, "--cockpit="+good.Root)
	if _, err := RequireCockpit(cmd); err != nil {
		t.Errorf("a usable cockpit was refused: %v", err)
	}

	bad := aCockpit(t, "\tnot: [yaml")
	cmd = withFlags(t, AddCockpit, "--cockpit="+bad.Root)
	if _, err := RequireCockpit(cmd); err == nil {
		t.Error("a cockpit with an unparseable registry was accepted")
	}
}

// The table measures from the raw cells and decorates afterwards, so colour
// never disturbs the alignment.
func TestTheTableAlignsOnVisibleWidthNotDecoratedWidth(t *testing.T) {
	out := &bytes.Buffer{}

	Table{
		Headers: []string{"MODULE", "STATUS"},
		Rows: [][]string{
			{"pathauto", "pass"},
			{"field_visibility_conditions", "fail"},
		},
		Colourise: func(cells []string) []string {
			// Escape sequences of exactly the kind that would break naive
			// padding: they add bytes and no visible width.
			return []string{"\x1b[32m" + cells[0] + "\x1b[0m", cells[1]}
		},
	}.Render(out)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines:\n%s", len(lines), out)
	}

	// The status column starts at the same offset on both rows, measured with
	// the decoration stripped.
	first := strings.Index(stripColour(lines[2]), "pass")
	second := strings.Index(stripColour(lines[3]), "fail")
	if first != second {
		t.Errorf("columns are not aligned: %d vs %d\n%s", first, second, out)
	}
	// And wide enough for the widest cell plus the gap.
	if first != len("field_visibility_conditions")+tableGap {
		t.Errorf("column offset %d\n%s", first, out)
	}
}

// Rows are separated into groups when a key is given, and not when it is not.
func TestGroupedRowsAreSeparatedAndUngroupedOnesAreNot(t *testing.T) {
	rows := [][]string{{"a"}, {"b"}, {"c"}}

	grouped := &bytes.Buffer{}
	Table{
		Headers: []string{"X"}, Rows: rows, GroupKeys: []string{"one", "one", "two"},
	}.Render(grouped)

	ungrouped := &bytes.Buffer{}
	Table{Headers: []string{"X"}, Rows: rows}.Render(ungrouped)

	if strings.Count(grouped.String(), "\n\n") != 2 {
		t.Errorf("groups were not separated:\n%q", grouped)
	}
	if strings.Count(ungrouped.String(), "\n\n") != 1 {
		t.Errorf("ungrouped rows were separated anyway:\n%q", ungrouped)
	}
}

// Nothing to show is a header and no rows, rather than a crash.
func TestATableWithNoRowsStillRendersItsHeader(t *testing.T) {
	out := &bytes.Buffer{}
	Table{Headers: []string{"MODULE", "STATUS"}}.Render(out)

	if !strings.Contains(out.String(), "MODULE") {
		t.Errorf("got %q", out)
	}
}

// A row with more cells than the header has still renders, rather than
// reaching past the end of the widths.
func TestARowWiderThanItsHeaderDoesNotReachPastTheWidths(t *testing.T) {
	out := &bytes.Buffer{}
	Table{Headers: []string{"A"}, Rows: [][]string{{"one", "two", "three"}}}.Render(out)

	if !strings.Contains(out.String(), "three") {
		t.Errorf("the extra cells were dropped: %q", out)
	}
}

// stripColour removes ANSI sequences, for measuring visible width in a test.
func stripColour(line string) string {
	var out strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\x1b' {
			for i < len(line) && line[i] != 'm' {
				i++
			}

			continue
		}
		out.WriteByte(line[i])
	}

	return out.String()
}

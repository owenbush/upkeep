package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/maintenance"
	"github.com/owenbush/upkeep/internal/workflow"
)

// someVolumes stands in for the engine's volume listing.
type someVolumes []adapter.ProjectVolume

func (v someVolumes) Volumes() []adapter.ProjectVolume { return v }

// aStatusCockpit lays out a cockpit with one environment, an artifact set and
// a library dump, under a home directory the containment rule accepts.
func aStatusCockpit(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	root := filepath.Join(home, "cockpit")
	if code, _, stderr := invoke(t, "init", root); code != workflow.OK {
		t.Fatalf("init: %s", stderr)
	}

	where, err := cockpit.New(root)
	if err != nil {
		t.Fatalf("cockpit: %v", err)
	}

	project := filepath.Join(where.ProjectsPath(), "upkeep-pathauto-d11")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := adapter.EnvironmentMeta{
		ModuleName: "pathauto", CoreMajor: "11", SeedCoreVersion: "11.4.6",
		AddOnVersion: adapter.EngineAddOnVersion,
		CreatedAt:    time.Now().Add(-30 * 24 * time.Hour),
		LastUsedAt:   time.Now().Add(-5 * 24 * time.Hour),
	}
	if err := meta.WriteTo(project); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(where.BaseArtifactsPath(), "11"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(where.FixturesPath(), "baseline.sql.gz"), []byte("dump"), 0o644,
	); err != nil {
		t.Fatalf("write: %v", err)
	}

	return root
}

// aStatus runs status with a scripted disk and volume listing.
func aStatus(t *testing.T, volumes someVolumes, sizes map[string]int64, args ...string) (int, string, string) {
	t.Helper()

	sizer := func(path string) int64 { return sizes[filepath.Base(path)] }

	return invokeWith(t, NewRootFor(volumes, sizer), args...)
}

// The summary answers the three questions somebody runs it for.
func TestStatusSummarisesTheCockpit(t *testing.T) {
	root := aStatusCockpit(t)

	code, stdout, _ := aStatus(t, nil, map[string]int64{
		"upkeep-pathauto-d11": 2_000_000_000,
		"11":                  4_000_000_000,
		"baseline.sql.gz":     50_000_000,
	}, "status", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "1 environment(s)") {
		t.Errorf("the environment count is wrong:\n%s", stdout)
	}
	if !strings.Contains(stdout, "0 registered module(s)") {
		t.Errorf("the module count is wrong:\n%s", stdout)
	}
	// The total is real measured usage, not a count.
	if !strings.Contains(stdout, "5.6 GiB") {
		t.Errorf("the total is wrong:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--disk") {
		t.Errorf("it did not say how to see the breakdown:\n%s", stdout)
	}
}

// The breakdown itemises everything and totals it by category.
func TestStatusDiskItemisesAndTotals(t *testing.T) {
	root := aStatusCockpit(t)

	code, stdout, _ := aStatus(t,
		someVolumes{{
			Name:        "upkeep-pathauto-d11-mariadb",
			ProjectName: "upkeep-pathauto-d11", SizeBytes: 700_000_000,
		}},
		map[string]int64{
			"upkeep-pathauto-d11": 2_000_000_000,
			"11":                  4_000_000_000,
			"baseline.sql.gz":     50_000_000,
		},
		"status", "--disk", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}
	for _, expected := range []string{
		"project tree", "base artifact", "fixture dump", "project volume",
		"upkeep-pathauto-d11-mariadb", "total:",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the breakdown is missing %q:\n%s", expected, stdout)
		}
	}
	// A volume is attributed to the environment it belongs to, because it is
	// reclaimed with it.
	volumeLine := lineContaining(stdout, "upkeep-pathauto-d11-mariadb")
	if !strings.Contains(volumeLine, "pathauto") || !strings.Contains(volumeLine, "11") {
		t.Errorf("the volume was not attributed: %q", volumeLine)
	}
}

// Canonical things say so, so an operator reading the table knows what prune
// will not touch before running it.
func TestTheBreakdownSaysWhatIsProtected(t *testing.T) {
	root := aStatusCockpit(t)

	where, _ := cockpit.New(root)
	kept := filepath.Join(where.ProjectsPath(), "upkeep-pathauto-d11", maintenance.KeepMarker)
	if err := os.WriteFile(kept, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, stdout, _ := aStatus(t, nil, nil, "status", "--disk", "--cockpit="+root)

	// Read out of the protection column rather than searched for in the line:
	// every one of these paths contains "upkeep", which contains "keep", so a
	// substring test passes whatever the column says.
	if got := columnIn(stdout, "upkeep-pathauto-d11", "Protection"); got != "keep" {
		t.Errorf("a keep-marked environment is protected by %q:\n%s", got, stdout)
	}
	for _, canonical := range []string{"baseline.sql.gz", "base-artifacts/11"} {
		if got := columnIn(stdout, canonical, "Protection"); got != "canonical" {
			t.Errorf("%s is protected by %q:\n%s", canonical, got, stdout)
		}
	}
}

// An unknown age is a question mark and never a number: the selector reads it
// as "cannot tell", so a 0d would show the one thing prune will not act on as
// the oldest thing on the disk.
func TestAnUnknownAgeIsNeverANumber(t *testing.T) {
	root := aStatusCockpit(t)

	where, _ := cockpit.New(root)
	// A partial provision: no meta, so no age.
	if err := os.MkdirAll(filepath.Join(where.ProjectsPath(), "upkeep-token-d11"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, stdout, _ := aStatus(t, nil, nil, "status", "--disk", "--cockpit="+root)

	line := lineContaining(stdout, "upkeep-token-d11")
	if got := columnIn(stdout, "upkeep-token-d11", "Age"); got != "?" {
		t.Errorf("an unknown age was rendered as %q: %q", got, line)
	}
	// And its attribution is a dash rather than a blank: an empty cell reads
	// as a field somebody forgot rather than one that cannot be known.
	if got := columnIn(stdout, "upkeep-token-d11", "Module"); got != "-" {
		t.Errorf("an unattributed module rendered as %q", got)
	}
	// And it is still reported: a partial provision is disk usage, and it is
	// exactly what prune exists to collect.
	if line == "" {
		t.Errorf("a partial provision was left out of the inventory:\n%s", stdout)
	}
}

// Age is reported at the resolution a prune decision is made at: days once
// there is a day, and hours below that. An environment used this morning
// reading as "0d" would look untouched.
func TestAgeIsHoursUnderADayAndDaysAboveIt(t *testing.T) {
	root := aStatusCockpit(t)
	where, _ := cockpit.New(root)

	fresh := filepath.Join(where.ProjectsPath(), "upkeep-token-d11")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := adapter.EnvironmentMeta{
		ModuleName: "token", CoreMajor: "11", SeedCoreVersion: "11.4.6",
		AddOnVersion: adapter.EngineAddOnVersion,
		CreatedAt:    time.Now().Add(-3 * time.Hour),
		LastUsedAt:   time.Now().Add(-3 * time.Hour),
	}
	if err := meta.WriteTo(fresh); err != nil {
		t.Fatalf("meta: %v", err)
	}

	_, stdout, _ := aStatus(t, nil, nil, "status", "--disk", "--cockpit="+root)

	if got := columnIn(stdout, "upkeep-token-d11", "Age"); got != "3h" {
		t.Errorf("an environment used three hours ago reads as %q:\n%s", got, stdout)
	}
	if got := columnIn(stdout, "upkeep-pathauto-d11", "Age"); got != "5d" {
		t.Errorf("an environment used five days ago reads as %q:\n%s", got, stdout)
	}
}

// Unattributed items sort last, because they are the ones somebody has to
// decide about by hand.
func TestUnattributedItemsSortLast(t *testing.T) {
	root := aStatusCockpit(t)

	_, stdout, _ := aStatus(t, nil, nil, "status", "--disk", "--cockpit="+root)

	attributed := strings.Index(stdout, "upkeep-pathauto-d11")
	unattributed := strings.Index(stdout, "baseline.sql.gz")
	if attributed < 0 || unattributed < 0 {
		t.Fatalf("missing rows:\n%s", stdout)
	}
	if attributed > unattributed {
		t.Errorf("the unattributed rows came first:\n%s", stdout)
	}
}

// A directory that could not be read is reported as such, not folded into the
// totals as if it were empty — under-reporting a total is how somebody
// concludes there is nothing to reclaim.
func TestAnUnreadableDirectoryIsWarnedAboutRatherThanCountedAsEmpty(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory regardless of its mode")
	}

	root := aStatusCockpit(t)
	where, _ := cockpit.New(root)
	if err := os.Chmod(where.FixturesPath(), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(where.FixturesPath(), 0o755) })

	code, _, stderr := aStatus(t, nil, nil, "status", "--cockpit="+root)

	if code != workflow.OK {
		t.Errorf("a warning became a failure: exit %d", code)
	}
	if !strings.Contains(stderr, "not readable") {
		t.Errorf("it was counted as empty: %q", stderr)
	}
}

// A registry that exists but does not parse is reported here, with the reason,
// rather than further down.
func TestStatusReportsAMalformedRegistryUpFront(t *testing.T) {
	root := aStatusCockpit(t)
	where, _ := cockpit.New(root)
	if err := os.WriteFile(where.RegistryPath(), []byte("\tnot: [yaml"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, stdout, stderr := aStatus(t, nil, nil, "status", "--cockpit="+root)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if stdout != "" {
		t.Errorf("it reported on a cockpit it could not read: %q", stdout)
	}
	if stderr == "" {
		t.Error("it failed silently")
	}
}

// A projects root that can never work is refused rather than reported on: the
// tree is bind-mounted into the container runtime's VM, and one outside the
// home directory can never start.
func TestStatusRefusesAProjectsRootThatCouldNeverWork(t *testing.T) {
	root := aStatusCockpit(t)
	outside := t.TempDir()

	code, _, stderr := aStatus(t, nil, nil,
		"status", "--cockpit="+root, "--projects-root="+outside)

	if code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr, "home directory") {
		t.Errorf("the refusal does not say the rule: %q", stderr)
	}
}

// columnIn reads one column of the row holding a fragment, by the offset its
// header sits at — so an assertion is about the cell it names rather than
// about the line happening to contain a word.
func columnIn(output, fragment, header string) string {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return ""
	}

	at := strings.Index(lines[0], header)
	row := lineContaining(output, fragment)
	if at < 0 || row == "" || at >= len(row) {
		return ""
	}

	return strings.TrimSpace(strings.SplitN(row[at:], "    ", 2)[0])
}

// lineContaining is the first output line holding a fragment.
func lineContaining(output, fragment string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, fragment) {
			return line
		}
	}

	return ""
}

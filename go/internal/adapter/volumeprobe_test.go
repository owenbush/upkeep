package adapter

import (
	"strings"
	"testing"
)

// Only volumes whose compose-project label carries the engine's prefix are
// engine volumes; everything else belongs to something that is none of our
// business, and prune deletes what the inventory reports.
func TestOnlyLabelledEngineVolumesAreRead(t *testing.T) {
	byName := ParseVolumeList(strings.Join([]string{
		"upkeep-pathauto-d11-mariadb\tddev-upkeep-pathauto-d11",
		"upkeep-token-d12-mariadb\tddev-upkeep-token-d12",
		"somebody-elses\tddev-somebody-elses",
		"unlabelled\t",
		"other-tooling\tcompose-something",
		"no-tab-at-all",
		"",
	}, "\n"))

	if len(byName) != 3 {
		t.Fatalf("got %v", byName)
	}
	if byName["upkeep-pathauto-d11-mariadb"] != "upkeep-pathauto-d11" {
		t.Errorf("the project was not read off the label: %v", byName)
	}
	// Another project's engine volume is still an engine volume; whether it is
	// ours is a question about an inventory, which the caller holds.
	if byName["somebody-elses"] != "somebody-elses" {
		t.Errorf("got %v", byName)
	}
	for _, absent := range []string{"unlabelled", "other-tooling", "no-tab-at-all"} {
		if _, present := byName[absent]; present {
			t.Errorf("%q was read as an engine volume", absent)
		}
	}
}

// The runtime reports in SI notation, never binary: "1.5GB" is 1.5e9 bytes and
// not 1.5 * 2^30. Getting that wrong overstates a reclaim by 7% at GB and more
// above it.
func TestVolumeSizesAreReadAsSiNotBinary(t *testing.T) {
	sizes := ParseVolumeSizes(`Images space usage:

REPOSITORY   TAG   IMAGE ID   SIZE
drud/ddev    v1    abc123     900MB

Local Volumes space usage:

VOLUME NAME                   LINKS     SIZE
upkeep-pathauto-d11-mariadb   1         1.5GB
upkeep-token-d12-mariadb      0         512.4MB
tiny-volume                   1         0B
big-one                       2         1.25TB
kilo                          1         4kB

Build cache usage: 0B
`)

	for name, want := range map[string]int64{
		"upkeep-pathauto-d11-mariadb": 1_500_000_000,
		"upkeep-token-d12-mariadb":    512_400_000,
		"tiny-volume":                 0,
		"big-one":                     1_250_000_000_000,
		"kilo":                        4_000,
	} {
		if sizes[name] != want {
			t.Errorf("%s is %d bytes, want %d", name, sizes[name], want)
		}
	}
	// Nothing outside the volume section.
	if _, leaked := sizes["drud/ddev"]; leaked {
		t.Errorf("a row from another section was read as a volume: %v", sizes)
	}
	if len(sizes) != 5 {
		t.Errorf("got %v", sizes)
	}
}

// A report with no volume section at all yields nothing rather than guesses.
func TestADiskReportWithNoVolumeSectionYieldsNothing(t *testing.T) {
	if sizes := ParseVolumeSizes("Images space usage:\n\nREPOSITORY TAG\nx y\n"); len(sizes) != 0 {
		t.Errorf("got %v", sizes)
	}
	if sizes := ParseVolumeSizes(""); len(sizes) != 0 {
		t.Errorf("got %v", sizes)
	}
}

// The probe is best-effort: when the runtime is unavailable it yields nothing,
// because a status listing must still render and a prune must still run over
// the rest of the disk.
func TestAnUnavailableRuntimeProbesToNothing(t *testing.T) {
	runner := newRunner()
	runner.fails("docker volume ls")

	if volumes := NewVolumeProbe(runner).Volumes(); volumes != nil {
		t.Errorf("got %+v", volumes)
	}
}

// And a runtime with no engine volumes is not asked the expensive question:
// the disk report walks every volume on the machine.
func TestTheExpensiveProbeIsSkippedWhenThereIsNothingToSize(t *testing.T) {
	runner := newRunner().answer("docker volume ls", "other-tooling\tcompose-something\n")

	if volumes := NewVolumeProbe(runner).Volumes(); volumes != nil {
		t.Errorf("got %+v", volumes)
	}
	if runner.didRun("system df") {
		t.Errorf("it walked every volume for an answer nobody would read:\n%s", runner.transcript())
	}
}

// Sizes are best-effort too: a volume the report did not mention is reported
// at zero rather than left out, because it still exists and still gets
// reclaimed.
func TestAVolumeTheReportDidNotMentionIsStillReported(t *testing.T) {
	runner := newRunner().
		answer("docker volume ls", strings.Join([]string{
			"upkeep-token-d12-mariadb\tddev-upkeep-token-d12",
			"upkeep-pathauto-d11-mariadb\tddev-upkeep-pathauto-d11",
		}, "\n")).
		answer("system df", "Local Volumes space usage:\n\nVOLUME NAME   LINKS   SIZE\n"+
			"upkeep-pathauto-d11-mariadb   1   1.5GB\n")

	volumes := NewVolumeProbe(runner).Volumes()

	if len(volumes) != 2 {
		t.Fatalf("got %+v", volumes)
	}
	// In name order, so a listing does not reshuffle between runs.
	if volumes[0].Name != "upkeep-pathauto-d11-mariadb" {
		t.Errorf("the listing is not ordered: %+v", volumes)
	}
	if volumes[0].SizeBytes != 1_500_000_000 {
		t.Errorf("size %d", volumes[0].SizeBytes)
	}
	if volumes[1].SizeBytes != 0 || volumes[1].ProjectName != "upkeep-token-d12" {
		t.Errorf("got %+v", volumes[1])
	}
}

// A disk report that cannot be had costs the sizes and not the listing.
func TestAFailedDiskReportCostsTheSizesNotTheVolumes(t *testing.T) {
	runner := newRunner().
		answer("docker volume ls", "upkeep-pathauto-d11-mariadb\tddev-upkeep-pathauto-d11\n")
	runner.fails("system df")

	volumes := NewVolumeProbe(runner).Volumes()

	if len(volumes) != 1 || volumes[0].SizeBytes != 0 {
		t.Errorf("got %+v", volumes)
	}
}

// materialized/<name>.sql maps to the project's snapshots/<name>.meta, and
// anything that is not a materialised snapshot maps to nothing.
func TestASnapshotArtifactMapsToItsSidecar(t *testing.T) {
	meta, ok := SnapshotMetaPathFor("/projects/upkeep-pathauto-d11/.ddev/upkeep/materialized/baseline.sql")
	if !ok {
		t.Fatal("a materialised snapshot mapped to nothing")
	}
	if meta != "/projects/upkeep-pathauto-d11/.ddev/upkeep/snapshots/baseline.meta" {
		t.Errorf("got %q", meta)
	}

	for _, elsewhere := range []string{
		"/projects/upkeep-pathauto-d11/module/tests/fixtures/baseline.sql",
		"/projects/upkeep-pathauto-d11/.ddev/upkeep/snapshots/baseline.meta",
		"baseline.sql",
	} {
		if _, ok := SnapshotMetaPathFor(elsewhere); ok {
			t.Errorf("%q was taken for a materialised snapshot", elsewhere)
		}
	}
}

// The volume section ends where the next one starts, even when the next
// section's rows have the same shape — otherwise a build-cache or container
// row would be offered to a prune as a volume to delete.
func TestTheVolumeSectionEndsWhereTheNextOneStarts(t *testing.T) {
	sizes := ParseVolumeSizes(`Local Volumes space usage:

VOLUME NAME                   LINKS     SIZE
upkeep-pathauto-d11-mariadb   1         1.5GB

Build cache usage: 2.1GB

CACHE ID       USAGE     SIZE
abc123def456   3         900MB
`)

	if len(sizes) != 1 {
		t.Fatalf("a row from the next section was read as a volume: %v", sizes)
	}
	if _, leaked := sizes["abc123def456"]; leaked {
		t.Errorf("a build-cache row was offered as a volume: %v", sizes)
	}
}

// Engine project names are injective over machine names, which is what lets a
// reverse lookup stop at its first match: the only transformation is
// underscore to hyphen, and a hyphen is not a legal machine name, so no two
// distinct modules can produce the same project.
func TestTwoModulesCanNeverShareAnEngineProjectName(t *testing.T) {
	// The collision that would exist if hyphens were legal.
	if _, err := EngineProjectName("field-tokens", "11"); err == nil {
		t.Fatal("a hyphenated name was accepted — project names are no longer injective")
	}

	seen := map[string]string{}
	for _, name := range []string{
		"pathauto", "token", "field_tokens", "fieldtokens", "field_visibility_conditions",
		"a", "a_b", "ab", "a_b_c", "a1_b2",
	} {
		project, err := EngineProjectName(name, "11")
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if clash, taken := seen[project]; taken {
			t.Errorf("%q and %q both produce %q", clash, name, project)
		}
		seen[project] = name
	}
}

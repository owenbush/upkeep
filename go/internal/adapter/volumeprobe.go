package adapter

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/proc"
)

// ProjectVolume is one of an engine project's named volumes, as the container
// runtime reports it.
//
// Deliberately not an inventory item. The PHP's VolumeProbe builds
// Maintenance\InventoryItem directly, which Go will not take — Maintenance's
// scanner needs the snapshot layout, which lives here, and that is a cycle.
// Breaking it the way the check models and the local evidence were broken out
// leaves a better split anyway: the adapter reports engine facts, and the
// maintenance layer decides what they mean for an inventory.
type ProjectVolume struct {
	// Name is the volume's name, which is also how it is deleted.
	Name string
	// ProjectName is the engine project it belongs to.
	ProjectName string
	// SizeBytes is what the runtime says it holds, or 0 when it did not say.
	SizeBytes int64
}

const (
	// volumeProjectLabel is the label the engine's compose-managed volumes
	// carry, verified live.
	volumeProjectLabel = "com.docker.compose.project"

	// composeProjectPrefix is what the engine prefixes its compose project
	// with, so a volume's label maps back to a project name.
	composeProjectPrefix = "ddev-"

	// volumeProbeTimeout bounds the two probes. Both are local queries, and a
	// probe that hangs must not hold up a status listing.
	volumeProbeTimeout = 2 * time.Minute
)

// siMultipliers is how the runtime reports sizes.
//
// SI notation (go-units), never binary — "1.5GB" is 1.5e9 bytes and not
// 1.5 * 2^30. The table is total over the units the size pattern admits, which
// is why the lookup needs no fallback: the pattern and this table are the two
// halves of one decision and have to change together.
var siMultipliers = map[string]int64{
	"B":  1,
	"KB": 1000,
	"MB": 1000 * 1000,
	"GB": 1000 * 1000 * 1000,
	"TB": 1000 * 1000 * 1000 * 1000,
}

var volumeSizeLine = regexp.MustCompile(`^(\S+)\s+\d+\s+([\d.]+)\s*([kKMGT]?B)\s*$`)

// VolumeProbe is a best-effort read of the engine projects' named volumes.
//
// Best-effort throughout: when the container runtime is unavailable the probe
// yields nothing rather than failing, because a status listing must still
// render and a prune must still be able to run over the rest of the disk.
// Reclaiming a volume is always the adapter teardown's job and never this
// type's.
type VolumeProbe struct {
	runner proc.Runner
}

// NewVolumeProbe builds a probe over the given runner.
func NewVolumeProbe(runner proc.Runner) *VolumeProbe { return &VolumeProbe{runner: runner} }

// Volumes is every engine volume the runtime reports, with its size when it
// reports one.
//
// Unfiltered: which of them belong to environments this cockpit knows about is
// a question about an inventory, and the caller holds that.
func (p *VolumeProbe) Volumes() []ProjectVolume {
	listing, listed := p.runner.TryRun([]string{
		"docker", "volume", "ls", "--format",
		"{{.Name}}\t{{.Label \"" + volumeProjectLabel + "\"}}",
	}, "", volumeProbeTimeout)
	if !listed {
		return nil
	}

	byName := ParseVolumeList(listing)
	if len(byName) == 0 {
		return nil
	}

	// Asked for only once there is something to ask about: `docker system df`
	// walks every volume on the machine, which is slow enough to be worth not
	// doing for an answer nobody will read.
	usage, _ := p.runner.TryRun([]string{"docker", "system", "df", "-v"}, "", volumeProbeTimeout)
	sizes := ParseVolumeSizes(usage)

	volumes := make([]ProjectVolume, 0, len(byName))
	for _, name := range sortedKeys(byName) {
		volumes = append(volumes, ProjectVolume{
			Name: name, ProjectName: byName[name], SizeBytes: sizes[name],
		})
	}

	return volumes
}

// ParseVolumeList reads the volume listing: volume name => engine project.
//
// Only volumes whose compose-project label carries the engine's prefix are
// engine volumes; everything else belongs to something that is none of our
// business, and prune deletes what the inventory reports.
func ParseVolumeList(output string) map[string]string {
	volumes := map[string]string{}

	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 2 || parts[0] == "" || !strings.HasPrefix(parts[1], composeProjectPrefix) {
			continue
		}
		volumes[parts[0]] = strings.TrimPrefix(parts[1], composeProjectPrefix)
	}

	return volumes
}

// ParseVolumeSizes reads the "Local Volumes space usage" section of the
// runtime's disk-usage report: volume name => size in bytes.
func ParseVolumeSizes(output string) map[string]int64 {
	sizes := map[string]int64{}
	inSection := false

	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "Local Volumes space usage") {
			inSection = true

			continue
		}
		if !inSection {
			continue
		}

		trimmed := strings.TrimSpace(line)
		if match := volumeSizeLine.FindStringSubmatch(trimmed); match != nil {
			sizes[match[1]] = siBytes(match[2], match[3])

			continue
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "VOLUME NAME") {
			// A non-matching, non-empty line is the next section's header, so
			// the volume section has ended — unless nothing has matched yet,
			// in which case this is still the header block above the rows.
			inSection = len(sizes) == 0
		}
	}

	return sizes
}

// siBytes turns a reported size and its unit into bytes.
func siBytes(amount, unit string) int64 {
	value, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return 0
	}

	return int64(value*float64(siMultipliers[strings.ToUpper(unit)]) + 0.5)
}

// sortedKeys is the map keys in order, so a listing is stable rather than
// however the runtime happened to answer.
func sortedKeys(byName map[string]string) []string {
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	slices.Sort(names)

	return names
}

package maintenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/proc"
	"github.com/owenbush/upkeep/internal/security"
)

// realSizer measures with a real child process, which is the only way to know
// the command and the parse agree.
func realSizer(t *testing.T) Sizer {
	t.Helper()

	return DiskSizer(proc.New(nil, nil, security.NewRedactor()))
}

// The measurement is real bytes off a real `du`, because a parse that agrees
// with a fake says nothing about the flag it was asked with: -sk is the
// spelling both BSD and GNU accept, and getting it wrong is silent.
func TestADirectoryMeasuresAsRealBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "payload"), make([]byte, 200_000), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	size := realSizer(t)(dir)

	if size < 200_000 {
		t.Errorf("measured %d bytes for a 200 KB file — real usage is never less than the content", size)
	}
	// Blocks, so a little above is expected and a lot above is not the same
	// unit.
	if size > 4_000_000 {
		t.Errorf("measured %d bytes — that is not kilobyte blocks", size)
	}
}

// A path with a space in it measures as one path: `du -sk` prints
// "<blocks>\t<path>", so only the first field may ever be read.
func TestAPathWithSpacesMeasuresAsOnePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a directory with spaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload"), make([]byte, 100_000), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if size := realSizer(t)(dir); size < 100_000 {
		t.Errorf("measured %d bytes", size)
	}
}

// Something that is not there measures as zero rather than taking a listing
// down: the scanner's warnings are where "I could not look" is reported.
func TestSomethingThatIsNotThereMeasuresAsZero(t *testing.T) {
	if size := realSizer(t)(filepath.Join(t.TempDir(), "never-existed")); size != 0 {
		t.Errorf("measured %d bytes for a path that does not exist", size)
	}
}

// answering is a runner that answers one scripted thing.
type answering struct {
	output string
	ok     bool
}

func (a answering) Run(command []string, dir string, timeout time.Duration) (string, error) {
	return a.output, nil
}

func (a answering) TryRun([]string, string, time.Duration) (string, bool) { return a.output, a.ok }

func (a answering) Capture([]string, string, time.Duration) proc.Captured { return proc.Captured{} }

// Output that is not a block count measures as zero rather than as whatever a
// partial parse produced — a wrong size is worse than no size, because it goes
// into the total a prune promises to free.
func TestOutputThatIsNotABlockCountMeasuresAsZero(t *testing.T) {
	for _, output := range []string{
		"",
		"\n",
		"du: cannot read directory\n",
		"?\t/some/path\n",
		"-4\t/some/path\n",
		"1e6\t/some/path\n",
	} {
		if size := DiskSizer(answering{output: output, ok: true})(("/some/path")); size != 0 {
			t.Errorf("%q measured as %d bytes", output, size)
		}
	}
}

// Blocks are kilobytes, so the reported number is multiplied.
func TestBlocksAreKilobytes(t *testing.T) {
	size := DiskSizer(answering{output: "1500000\t/some/path\n", ok: true})("/some/path")

	if size != 1_500_000*1024 {
		t.Errorf("measured %d bytes", size)
	}
}

// A measurement that failed is zero, not the output it failed with.
func TestAFailedMeasurementIsZero(t *testing.T) {
	if size := DiskSizer(answering{output: "1500000\t/x\n", ok: false})("/x"); size != 0 {
		t.Errorf("measured %d bytes from a command that failed", size)
	}
}

// Every category renders a label, because the disk table prints one per row
// and an unlabelled row is a blank cell.
func TestEveryCategoryHasALabel(t *testing.T) {
	for _, category := range []Category{
		BaseArtifact, FixtureDump, ProjectTree, ProjectVolume, Snapshot,
	} {
		if label := category.Label(); label == "" {
			t.Errorf("%q renders no label", category)
		} else if strings.TrimSpace(label) != label {
			t.Errorf("%q renders %q", category, label)
		}
	}
}

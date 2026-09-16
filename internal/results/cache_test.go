package results

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/check"
)

func exit(code int) *int { return &code }

func passingRun() check.RunResult {
	return check.RunResult{Results: []check.Result{
		{Type: check.PhpUnit, Status: check.Passed, ExitCode: exit(0), Output: "OK (12 tests)", Duration: 3 * time.Second},
		{Type: check.PhpCs, Status: check.Passed, ExitCode: exit(0)},
	}}
}

func mrKey(t *testing.T, iid int) Key {
	t.Helper()

	key, err := MergeRequestKey(iid)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	return key
}

// Both subjects are identified by a number and nothing keeps the ranges apart,
// so the separation is structural. A patch result read as a merge-request
// result would put patch evidence in front of the fast-lane gate.
func TestAMergeRequestAndAPatchWithTheSameNumberDoNotCollide(t *testing.T) {
	cache := NewCache(t.TempDir())

	mr, err := MergeRequestKey(3597808)
	if err != nil {
		t.Fatalf("merge request key: %v", err)
	}
	patch, err := PatchKey(3597808)
	if err != nil {
		t.Fatalf("patch key: %v", err)
	}
	if mr.Segment == patch.Segment {
		t.Fatalf("both keyed to %q", mr.Segment)
	}

	if err := cache.Store("pathauto", mr, "11", "abc1234", passingRun(), time.Now()); err != nil {
		t.Fatalf("store: %v", err)
	}

	if found := cache.Find("pathauto", patch, "11", "abc1234"); found != nil {
		t.Error("a merge-request result was answered for a patch")
	}
	if found := cache.Find("pathauto", mr, "11", "abc1234"); found == nil {
		t.Error("the merge-request result was not found")
	}
}

func TestAKeyMustBeAPositiveNumber(t *testing.T) {
	for _, value := range []int{0, -1} {
		if _, err := MergeRequestKey(value); err == nil {
			t.Errorf("merge request %d was accepted", value)
		}
		if _, err := PatchKey(value); err == nil {
			t.Errorf("issue %d was accepted", value)
		}
	}
}

func TestAStoredResultComesBackWhole(t *testing.T) {
	cache := NewCache(t.TempDir())
	recorded := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	if err := cache.Store("pathauto", mrKey(t, 12), "11", "abc1234", passingRun(), recorded); err != nil {
		t.Fatalf("store: %v", err)
	}

	found := cache.Find("pathauto", mrKey(t, 12), "11", "abc1234")
	if found == nil {
		t.Fatal("not found")
	}
	if found.SHA != "abc1234" {
		t.Errorf("sha %q", found.SHA)
	}
	if !found.RecordedAt.Equal(recorded) {
		t.Errorf("recorded at %v, want %v", found.RecordedAt, recorded)
	}
	if len(found.Result.Results) != 2 {
		t.Fatalf("got %d checks", len(found.Result.Results))
	}
	first := found.Result.Results[0]
	if first.Type != check.PhpUnit || first.Status != check.Passed {
		t.Errorf("first check %+v", first)
	}
	if first.ExitCode == nil || *first.ExitCode != 0 {
		t.Errorf("exit code %v", first.ExitCode)
	}
	if first.Duration != 3*time.Second {
		t.Errorf("duration %v", first.Duration)
	}
	if !strings.Contains(first.Output, "OK (12 tests)") {
		t.Errorf("output %q", first.Output)
	}
}

// The payload embeds raw check output — host paths, source fragments, stack
// traces — which has no business being world-readable.
func TestResultsAreOwnerOnly(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)

	if err := cache.Store("pathauto", mrKey(t, 12), "11", "abc1234", passingRun(), time.Now()); err != nil {
		t.Fatalf("store: %v", err)
	}

	file := filepath.Join(root, "pathauto", "12", "11", "abc1234.json")
	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("result file is %04o, want 0600", info.Mode().Perm())
	}

	dir, err := os.Stat(filepath.Dir(file))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("result directory is %04o, want 0700", dir.Mode().Perm())
	}
}

// Cached output is an excerpt for reporting, not a full log archive.
func TestStoredOutputIsCappedAtTheExcerpt(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	run := check.RunResult{Results: []check.Result{
		{Type: check.PhpStan, Status: check.Failed, ExitCode: exit(1), Output: strings.Repeat("x", 10000)},
	}}

	if err := cache.Store("pathauto", mrKey(t, 12), "11", "abc1234", run, time.Now()); err != nil {
		t.Fatalf("store: %v", err)
	}

	found := cache.Find("pathauto", mrKey(t, 12), "11", "abc1234")
	if found == nil {
		t.Fatal("not found")
	}
	if got := len(found.Result.Results[0].Output); got != outputExcerptBytes {
		t.Errorf("stored %d bytes of output, want %d", got, outputExcerptBytes)
	}
}

// A SHA may be an unvalidated remote value from the GitLab API, so it has to
// look like one before it becomes a filename.
func TestAShaMustLookLikeARevision(t *testing.T) {
	cache := NewCache(t.TempDir())

	for _, sha := range []string{"", "abc", "../../etc/passwd", "ABC1234", "zzzzzzz", strings.Repeat("a", 65)} {
		if err := cache.Store("pathauto", mrKey(t, 12), "11", sha, passingRun(), time.Now()); err == nil {
			t.Errorf("stored a result keyed %q", sha)
		}
		if found := cache.Find("pathauto", mrKey(t, 12), "11", sha); found != nil {
			t.Errorf("found a result keyed %q", sha)
		}
	}
}

func TestPathComponentsAreValidated(t *testing.T) {
	cache := NewCache(t.TempDir())

	for _, tc := range []struct{ module, core string }{
		{"../escape", "11"},
		{"Path-Auto", "11"},
		{"", "11"},
		{"pathauto", "11.2"},
		{"pathauto", "../11"},
		{"pathauto", ""},
	} {
		err := cache.Store(tc.module, mrKey(t, 12), tc.core, "abc1234", passingRun(), time.Now())
		if err == nil {
			t.Errorf("stored under module %q core %q", tc.module, tc.core)
		}
		// A read for an impossible identity is a miss, not a crash.
		if found := cache.Find(tc.module, mrKey(t, 12), tc.core, "abc1234"); found != nil {
			t.Errorf("found something under module %q core %q", tc.module, tc.core)
		}
	}
}

func TestLatestTakesTheNewestRecordingRegardlessOfRevision(t *testing.T) {
	cache := NewCache(t.TempDir())
	key := mrKey(t, 12)

	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := cache.Store("pathauto", key, "11", "bbb2222", passingRun(), newer); err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := cache.Store("pathauto", key, "11", "aaa1111", passingRun(), older); err != nil {
		t.Fatalf("store: %v", err)
	}

	latest, err := cache.Latest("pathauto", key, "11")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest == nil {
		t.Fatal("nothing found")
	}
	if latest.SHA != "bbb2222" {
		t.Errorf("got %q, want the newest recording", latest.SHA)
	}
}

func TestLatestIsNothingWhenNothingWasEverChecked(t *testing.T) {
	cache := NewCache(t.TempDir())

	latest, err := cache.Latest("pathauto", mrKey(t, 12), "11")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest != nil {
		t.Errorf("got %+v, want nothing", latest)
	}
}

// Reporting an unreadable directory as "never checked" would let the gate deny
// a merge for a reason that is invisible to the operator.
func TestAnUnreadableDirectoryIsSaidRatherThanReadAsEmpty(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 directory regardless of its mode")
	}

	root := t.TempDir()
	cache := NewCache(root)
	key := mrKey(t, 12)
	if err := cache.Store("pathauto", key, "11", "abc1234", passingRun(), time.Now()); err != nil {
		t.Fatalf("store: %v", err)
	}

	dir := filepath.Join(root, "pathauto", "12", "11")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	latest, err := cache.Latest("pathauto", key, "11")
	if err == nil {
		t.Fatalf("an unreadable directory read as %v with no complaint", latest)
	}
	if !strings.Contains(err.Error(), "not the same as never checked") {
		t.Errorf("error %q does not say what it is not", err)
	}
}

// A malformed cache file is a miss, never a crash.
func TestABadCacheFileIsAMiss(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	dir := filepath.Join(root, "pathauto", "12", "11")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for name, contents := range map[string]string{
		"aaa1111.json": `{ this is not json`,
		"bbb2222.json": `{"sha":"bbb2222"}`,
		"ccc3333.json": `{"sha":"ccc3333","recorded_at":"not a date","results":[]}`,
		"ddd4444.json": `"a bare string"`,
		// A check type this build does not know is a miss rather than a result
		// with an invented type.
		"eee5555.json": `{"sha":"eee5555","recorded_at":"2026-01-01T00:00:00Z",
			"results":[{"type":"quantum_lint","status":"passed"}]}`,
		"fff6666.json": `{"sha":"fff6666","recorded_at":"2026-01-01T00:00:00Z",
			"results":[{"type":"phpcs","status":"probably-fine"}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	key := mrKey(t, 12)
	latest, err := cache.Latest("pathauto", key, "11")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest != nil {
		t.Errorf("a malformed file was read as a result: %+v", latest)
	}

	sha := strings.TrimSuffix("aaa1111.json", ".json")
	if found := cache.Find("pathauto", key, "11", sha); found != nil {
		t.Error("a malformed file was found")
	}
}

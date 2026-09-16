package results

import (
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/check"
)

const head = "abc1234"

func passedAt(sha string) *CachedResult {
	return &CachedResult{SHA: sha, RecordedAt: time.Now(), Result: passingRun()}
}

func failedAt(sha string, failed ...check.Type) *CachedResult {
	results := []check.Result{{Type: check.PhpUnit, Status: check.Passed}}
	for _, one := range failed {
		results = append(results, check.Result{Type: one, Status: check.Failed})
	}

	return &CachedResult{SHA: sha, RecordedAt: time.Now(), Result: check.RunResult{Results: results}}
}

// The actionable fact is that something is broken and where. A cell reading
// "pass" because two of three cores were green would be worse than useless.
func TestTheWorstCaseWinsAndNamesItsCore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		byCore map[string]*CachedResult
		want   string
	}{
		{
			"a failure outranks two passes",
			map[string]*CachedResult{"10": failedAt(head, check.PhpCs), "11": passedAt(head), "12": passedAt(head)},
			"fail 10",
		},
		{
			"stale evidence outranks a pass",
			map[string]*CachedResult{"10": passedAt("older11"), "11": passedAt(head)},
			"stale 10",
		},
		{
			"a failure outranks stale evidence",
			map[string]*CachedResult{"10": passedAt("older11"), "11": failedAt(head, check.PhpStan)},
			"fail 11",
		},
		{
			"green where checked, with the gap named",
			map[string]*CachedResult{"10": nil, "11": passedAt(head)},
			"pass 11 · ? 10",
		},
		{
			"green everywhere says so plainly",
			map[string]*CachedResult{"10": passedAt(head), "11": passedAt(head)},
			"pass 10,11",
		},
		{
			"nothing checked anywhere is a dash",
			map[string]*CachedResult{"10": nil, "11": nil},
			"–",
		},
	} {
		if got := Evidence(tc.byCore, head).Cell(); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNoEvidenceAtAllIsADash(t *testing.T) {
	if got := NoEvidence().Cell(); got != "–" {
		t.Errorf("got %q", got)
	}
	if got := NoEvidence().Describe(); got != "–" {
		t.Errorf("describe: got %q", got)
	}
}

// The cell and the NEXT command must not name different cores.
func TestTheAttentionCoreIsTheOneTheCellNames(t *testing.T) {
	for _, tc := range []struct {
		byCore map[string]*CachedResult
		want   string
	}{
		{map[string]*CachedResult{"10": passedAt(head), "11": failedAt(head, check.PhpCs)}, "11"},
		{map[string]*CachedResult{"10": passedAt("older11"), "11": passedAt(head)}, "10"},
		{map[string]*CachedResult{"10": nil, "11": passedAt(head)}, "10"},
		{map[string]*CachedResult{"10": passedAt(head), "11": passedAt(head)}, ""},
	} {
		if got := Evidence(tc.byCore, head).AttentionCore(); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.byCore, got, tc.want)
		}
	}
}

// Empty is the safe direction: evidence about an unknown revision is evidence
// about another tree.
func TestWithNoCurrentRevisionEverythingIsStale(t *testing.T) {
	evidence := Evidence(map[string]*CachedResult{"10": passedAt(head), "11": passedAt(head)}, "")

	if !evidence.AnyStale() {
		t.Error("evidence against an unknown revision read as fresh")
	}
	if evidence.AllGreen() {
		t.Error("evidence against an unknown revision read as green")
	}
}

// A core that applies and has no fresh pass denies the fast lane. The old
// model let a merge request green on 11 and unchecked on 10 present a ready
// row.
func TestAllGreenNeedsEveryApplicableCore(t *testing.T) {
	green := Evidence(map[string]*CachedResult{"10": passedAt(head), "11": passedAt(head)}, head)
	if !green.AllGreen() {
		t.Error("two fresh passes were not green")
	}

	gap := Evidence(map[string]*CachedResult{"10": nil, "11": passedAt(head)}, head)
	if gap.AllGreen() {
		t.Error("a pass covering half the cores read as green")
	}

	if NoEvidence().AllGreen() {
		t.Error("no evidence at all read as green")
	}
}

// phpcs failing on 10 and on 11 is one thing wrong with the branch, not two.
func TestFailedChecksAreDeduplicatedAcrossCores(t *testing.T) {
	evidence := Evidence(map[string]*CachedResult{
		"10": failedAt(head, check.PhpCs, check.PhpStan),
		"11": failedAt(head, check.PhpCs),
	}, head)

	failed := evidence.FailedChecks()
	if len(failed) != 2 {
		t.Fatalf("got %v, want two distinct checks", failed)
	}
	if failed[0] != "phpcs" || failed[1] != "phpstan" {
		t.Errorf("got %v, want them in recorded order", failed)
	}
}

// Stale evidence is not evidence of a failure: it is about another revision.
func TestAStaleFailureIsNotReportedAsAFailedCheck(t *testing.T) {
	evidence := Evidence(map[string]*CachedResult{"10": failedAt("older11", check.PhpCs)}, head)

	if failed := evidence.FailedChecks(); len(failed) != 0 {
		t.Errorf("got %v, want nothing — that failure is about another tree", failed)
	}
	if evidence.AnyFailed() {
		t.Error("a stale failure read as a fresh one")
	}
	if !evidence.AnyStale() {
		t.Error("it did not read as stale either")
	}
}

func TestCoresAreOrderedNumericallyNotAsStrings(t *testing.T) {
	evidence := Evidence(map[string]*CachedResult{"9": nil, "10": nil, "11": nil}, head)

	cores := evidence.Cores()
	if len(cores) != 3 || cores[0] != "9" || cores[1] != "10" || cores[2] != "11" {
		t.Errorf("got %v, want 9, 10, 11", cores)
	}
}

// The detail half of worst-case-plus-detail: the cell says what is wrong, this
// says where.
func TestDescribeNamesEveryCoreWithItsState(t *testing.T) {
	evidence := Evidence(map[string]*CachedResult{
		"10": failedAt(head, check.PhpCs),
		"11": passedAt(head),
		"12": nil,
		"13": passedAt("older11"),
	}, head)

	if got := evidence.Describe(); got != "10:fail 11:pass 12:unchecked 13:stale" {
		t.Errorf("got %q", got)
	}
}

func TestLatestRecordedAtIsTheNewestOfWhatIsKnown(t *testing.T) {
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	evidence := Evidence(map[string]*CachedResult{
		"10": {SHA: head, RecordedAt: older, Result: passingRun()},
		"11": {SHA: head, RecordedAt: newer, Result: passingRun()},
		"12": nil,
	}, head)

	if got := evidence.LatestRecordedAt(); !got.Equal(newer) {
		t.Errorf("got %v, want %v", got, newer)
	}

	if got := NoEvidence().LatestRecordedAt(); !got.IsZero() {
		t.Errorf("got %v, want the zero time", got)
	}
}

func TestCoversAsksAboutEveryCoreGiven(t *testing.T) {
	evidence := Evidence(map[string]*CachedResult{"10": nil, "11": passedAt(head)}, head)

	if !evidence.Covers([]string{"10", "11"}) {
		t.Error("an unchecked core still counts as covered — it is a known gap, not an absence")
	}
	if evidence.Covers([]string{"10", "11", "12"}) {
		t.Error("a core the evidence says nothing about read as covered")
	}
	if !evidence.Covers(nil) {
		t.Error("nothing to cover was not covered")
	}
}

package check

import (
	"strings"
	"testing"
	"time"
)

func exit(code int) *int { return &code }

// PHPUnit 11.5 exits 0 when it discovers nothing, so a silent pass would hide
// the fact that nothing ran.
func TestPhpunitFindingNoTestsIsNotAPass(t *testing.T) {
	for _, output := range []string{"No tests executed!", "No tests found in class…", "NO TESTS EXECUTED"} {
		result := FromProcess(PhpUnit, exit(0), output, time.Second)
		if result.Status != NoTests {
			t.Errorf("%q: status %q, want no-tests", output, result.Status)
		}
		// Still an honest non-failure: it does not block an all-green verdict.
		if !result.Passed() {
			t.Errorf("%q: no-tests blocked a green verdict", output)
		}
	}
}

// The classification is about phpunit's own reporting, not about any check
// whose output happens to contain the phrase.
func TestOnlyPhpunitReportsNoTests(t *testing.T) {
	result := FromProcess(PhpCs, exit(0), "No tests executed", time.Second)
	if result.Status != Passed {
		t.Errorf("status %q, want passed", result.Status)
	}
}

// A child that reported no status at all never ran cleanly, and only exit 0 is
// a pass — so "no status" must never be mistaken for one.
func TestNoExitStatusIsAFailure(t *testing.T) {
	result := FromProcess(PhpStan, nil, "killed", time.Second)
	if result.Status != Failed {
		t.Errorf("status %q, want failed", result.Status)
	}
	if result.Passed() {
		t.Error("a child with no exit status was treated as a pass")
	}
}

func TestNonZeroIsAFailureAndZeroIsAPass(t *testing.T) {
	if got := FromProcess(PhpCs, exit(2), "", time.Second).Status; got != Failed {
		t.Errorf("status %q, want failed", got)
	}
	if got := FromProcess(PhpCs, exit(0), "", time.Second).Status; got != Passed {
		t.Errorf("status %q, want passed", got)
	}
}

// Two non-failure states must stay visible rather than be folded away.
func TestTheTwoHonestNonFailuresDoNotBlockGreen(t *testing.T) {
	for _, status := range []Status{NoTests, Unavailable, Passed} {
		if !status.Passed() {
			t.Errorf("%q blocked a green verdict", status)
		}
	}
	if Failed.Passed() {
		t.Error("a failure did not block a green verdict")
	}
}

// A check the engine cannot run is recorded explicitly, never silently
// omitted.
func TestAnUnavailableCheckKeepsItsReason(t *testing.T) {
	result := NotAvailable(Deprecation, "upgrade_status has no build for core 13")

	if result.Status != Unavailable {
		t.Errorf("status %q", result.Status)
	}
	if result.ExitCode != nil {
		t.Error("an unavailable check carries an exit code for a run that never happened")
	}
	if !strings.Contains(result.Output, "upgrade_status") {
		t.Errorf("output %q does not carry the reason", result.Output)
	}
}

// The reason goes ahead of the partial output, because that is what says why
// the output stops where it does.
func TestATimeoutRecordsTheReasonFirst(t *testing.T) {
	result := TimedOut(PhpUnit, "....F", 30*time.Second, 25*time.Second)

	if result.Status != Failed {
		t.Errorf("status %q, want failed", result.Status)
	}
	if !strings.HasPrefix(result.Output, "Check timed out after 25s.") {
		t.Errorf("output %q", result.Output)
	}
	if !strings.Contains(result.Output, "....F") {
		t.Error("the partial output was dropped")
	}
}

// The tail is where the failure is.
func TestTheExcerptKeepsTheTailNotTheHead(t *testing.T) {
	result := Result{Output: "  " + strings.Repeat("a", 100) + "THE FAILURE  "}

	excerpt := result.OutputExcerpt(20)
	if len(excerpt) > 20 {
		t.Errorf("excerpt is %d bytes", len(excerpt))
	}
	if !strings.HasSuffix(excerpt, "THE FAILURE") {
		t.Errorf("excerpt %q dropped the end", excerpt)
	}
}

func TestAShortOutputIsReturnedWholeAndTrimmed(t *testing.T) {
	result := Result{Output: "\n  all good  \n"}

	if got := result.OutputExcerpt(0); got != "all good" {
		t.Errorf("got %q", got)
	}
}

func TestARunIsGreenOnlyWhenNothingFailed(t *testing.T) {
	green := RunResult{Results: []Result{
		{Type: PhpUnit, Status: NoTests},
		{Type: PhpCs, Status: Passed},
		{Type: Deprecation, Status: Unavailable},
	}}
	if !green.AllPassed() {
		t.Error("a run of honest non-failures was not green")
	}
	if len(green.Failures()) != 0 {
		t.Errorf("failures: %+v", green.Failures())
	}

	red := RunResult{Results: []Result{
		{Type: PhpUnit, Status: Passed},
		{Type: PhpCs, Status: Failed},
		{Type: PhpStan, Status: Failed},
	}}
	if red.AllPassed() {
		t.Error("a run with failures was green")
	}
	failures := red.Failures()
	if len(failures) != 2 || failures[0].Type != PhpCs || failures[1].Type != PhpStan {
		t.Errorf("failures %+v, want phpcs then phpstan in run order", failures)
	}
}

func TestAnEmptyRunIsGreen(t *testing.T) {
	if !(RunResult{}).AllPassed() {
		t.Error("a run of nothing was not green")
	}
}

// The closed set, enumerated once because three consumers have to agree on
// it: the results cache refuses a status not on this list, the merge-request
// comment gives each one a word, and the console report colours them.
func TestStatusesIsEveryStatusAndOnlyOpenOnes(t *testing.T) {
	statuses := Statuses()

	for _, status := range []Status{Passed, Failed, NoTests, Unavailable} {
		found := false
		for _, known := range statuses {
			found = found || known == status
		}
		if !found {
			t.Errorf("%q is missing from Statuses()", status)
		}
	}
	if len(statuses) != 4 {
		t.Errorf("Statuses() has %d entries: %v", len(statuses), statuses)
	}
	// Only Failed blocks: NoTests and Unavailable are honest, visible
	// non-answers rather than failures.
	for _, status := range statuses {
		if status.Passed() != (status != Failed) {
			t.Errorf("%q passes: %v", status, status.Passed())
		}
	}
}

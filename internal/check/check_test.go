package check

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

// The excerpt never begins in the middle of a line.
//
// It used to: a raw byte slice off the tail opened the report on the back half
// of whatever line the budget landed in, reported from the wild as output
// beginning "ssertSame() when expecting". A reader cannot tell that from a
// check whose output is genuinely strange, so they doubt the tool first.
func TestTheOutputTailStartsOnALineBoundary(t *testing.T) {
	var builder strings.Builder
	for line := 0; line < 200; line++ {
		fmt.Fprintf(&builder, "%03d    You should use assertTrue() instead of assertSame()\n", line)
	}
	result := Result{Output: builder.String()}

	// Every budget in the range, so this cannot pass by landing luckily.
	for budget := 500; budget <= 3000; budget += 7 {
		excerpt, _ := result.OutputTail(budget)
		first := strings.SplitN(excerpt, "\n", 2)[0]

		if !strings.HasSuffix(first, "assertSame()") {
			t.Fatalf("budget %d opened mid-line: %q", budget, first)
		}
		if len(first) != len("000    You should use assertTrue() instead of assertSame()") {
			t.Fatalf("budget %d produced a partial first line: %q", budget, first)
		}
	}
}

// It says how many lines it left out, so a surface can tell the reader rather
// than letting them assume that was all the output.
func TestTheOutputTailCountsWhatItLeftOut(t *testing.T) {
	var builder strings.Builder
	for line := 0; line < 100; line++ {
		fmt.Fprintf(&builder, "line %d padded out to make the budget bite\n", line)
	}
	result := Result{Output: builder.String()}

	excerpt, dropped := result.OutputTail(600)

	if dropped == 0 {
		t.Fatal("it dropped lines and reported none")
	}
	kept := strings.Count(excerpt, "\n") + 1
	if total := kept + dropped; total != 100 {
		t.Errorf("kept %d + dropped %d = %d, want 100", kept, dropped, total)
	}
}

// Nothing is claimed to be missing when nothing is.
func TestShortOutputIsWholeAndReportsNothingDropped(t *testing.T) {
	result := Result{Output: "  phpcs found three things\nand that is all of it  "}

	excerpt, dropped := result.OutputTail(ConsoleExcerptBytes)

	if dropped != 0 {
		t.Errorf("it reported %d dropped lines from output that fits", dropped)
	}
	if excerpt != "phpcs found three things\nand that is all of it" {
		t.Errorf("got %q", excerpt)
	}
}

// The excerpt is always valid UTF-8, including when the budget lands inside a
// multi-byte character.
//
// These checks emit them: phpstan labels every finding with an emoji
// identifier. A byte cut inside one put a replacement character at the front
// of the report. The case with no newline in the tail at all is the one the
// line-boundary rule cannot cover, so it is the one tested here.
func TestTheOutputTailIsAlwaysValidUtf8(t *testing.T) {
	oneLongLine := "preamble\n" + strings.Repeat("🪪", 500)
	result := Result{Output: oneLongLine}

	// Every offset through the emoji run, so a cut lands at each of the four
	// byte positions of a rune.
	for budget := 100; budget < 400; budget++ {
		excerpt, _ := result.OutputTail(budget)

		if !utf8.ValidString(excerpt) {
			t.Fatalf("budget %d produced invalid UTF-8: %q", budget, excerpt)
		}
		if strings.ContainsRune(excerpt, utf8.RuneError) {
			t.Fatalf("budget %d produced a replacement character: %q", budget, excerpt)
		}
	}
}

// A line-aligned cut is still bounded: the excerpt never exceeds the budget.
func TestTheOutputTailStaysWithinItsBudget(t *testing.T) {
	var builder strings.Builder
	for line := 0; line < 300; line++ {
		fmt.Fprintf(&builder, "a line of output number %d\n", line)
	}
	result := Result{Output: builder.String()}

	for _, budget := range []int{200, 1000, 4000} {
		excerpt, _ := result.OutputTail(budget)

		if len(excerpt) > budget {
			t.Errorf("budget %d produced %d bytes", budget, len(excerpt))
		}
	}
}

// The console shows more than a merge-request comment quotes, deliberately.
//
// A comment is a pointer for somebody who can open the pipeline; the console
// output is what the person who just ran the check diagnoses from, and nothing
// persists the full output for them to go back to.
func TestTheConsoleBudgetIsLargerThanACommentsQuote(t *testing.T) {
	if ConsoleExcerptBytes <= ExcerptBytes {
		t.Errorf("console %d is not larger than a comment's %d",
			ConsoleExcerptBytes, ExcerptBytes)
	}
}

// OutputExcerpt is the same truncation, so no two surfaces disagree about what
// the last N bytes means.
func TestOutputExcerptIsTheTailWithoutTheCount(t *testing.T) {
	var builder strings.Builder
	for line := 0; line < 80; line++ {
		fmt.Fprintf(&builder, "output line %d with some padding on it\n", line)
	}
	result := Result{Output: builder.String()}

	tail, _ := result.OutputTail(900)
	if excerpt := result.OutputExcerpt(900); excerpt != tail {
		t.Errorf("the two disagree:\n%q\n%q", excerpt, tail)
	}
}

// A zero or negative budget takes the comment default rather than returning
// nothing.
func TestANonPositiveBudgetTakesTheDefault(t *testing.T) {
	result := Result{Output: strings.Repeat("padding line\n", 500)}

	for _, budget := range []int{0, -1} {
		excerpt, _ := result.OutputTail(budget)

		if excerpt == "" {
			t.Errorf("budget %d returned nothing", budget)
		}
		if len(excerpt) > ExcerptBytes {
			t.Errorf("budget %d returned %d bytes, over the default", budget, len(excerpt))
		}
	}
}

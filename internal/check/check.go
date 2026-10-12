// Package check holds the outcome of running a check suite against a module.
//
// These models live beside the engine adapter in the PHP tree, but they carry
// no engine knowledge: a check is named by upkeep's own identifier, and how
// each maps onto tooling is adapter-private. They are a package of their own
// here so the results cache and the exit-code contract can depend on them
// without depending on the adapter.
package check

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Type is a check suite a maintenance environment can run against the module
// under maintenance.
type Type string

const (
	PhpUnit   Type = "phpunit"
	PhpCs     Type = "phpcs"
	PhpStan   Type = "phpstan"
	EsLint    Type = "eslint"
	StyleLint Type = "stylelint"
	// ModuleInstall is the module installing and enabling cleanly.
	ModuleInstall Type = "module_install"
	// FunctionalSmoke is the front page returning HTTP 200 with the module
	// enabled.
	FunctionalSmoke Type = "functional_smoke"
	// Deprecation is the deprecation/upgrade-status report for the target
	// core, when the engine provides one.
	Deprecation Type = "deprecation"
)

// Status is the outcome of one check.
//
// Richer than pass/fail because two non-failure states must stay visible in
// reports instead of being silently folded away: a module legitimately having
// no tests, and a check the engine cannot provide for the target core.
type Status string

const (
	Passed Status = "passed"
	Failed Status = "failed"
	// NoTests is the check having run and discovered nothing to run.
	NoTests Status = "no-tests"
	// Unavailable is the engine providing no way to run this check for the
	// target core.
	Unavailable Status = "unavailable"
)

// Passed reports whether this status permits an all-green verdict. Only a real
// failure blocks one: NoTests and Unavailable are honest, visible
// non-failures.
func (s Status) Passed() bool { return s != Failed }

// ExcerptBytes is how much of a check's output tail a *merge-request comment*
// quotes. Child output is unbounded; the tail is where the failure is.
const ExcerptBytes = 2000

// ConsoleExcerptBytes is how much the console report echoes.
//
// Six times a comment's quote, because the two are read for different reasons.
// A comment is a pointer for somebody else, who can open the pipeline. The
// console output is what the person who just ran the check diagnoses *from*,
// and there is nowhere else for them to look — nothing persists the full
// output, so whatever is cut here is gone.
//
// 2000 was both budgets, and it is far too small for a linter: phpstan
// reporting 32 findings in its boxed layout, or phpcs printing a full report
// plus two summary tables, runs to several times that. So the cut landed in
// the middle of the findings — and said nothing about having done so, which is
// the part that cost somebody an afternoon.
const ConsoleExcerptBytes = 12000

// Result is the outcome of one check suite run inside an environment.
type Result struct {
	Type   Type
	Status Status
	// ExitCode is nil when the check never ran, or when the child reported no
	// status at all.
	ExitCode *int
	// Output is the combined stdout and stderr of the run, for reporting and
	// triage.
	Output   string
	Duration time.Duration
}

// noTestsRan is PHPUnit's way of saying it discovered nothing.
var noTestsRan = regexp.MustCompile(`(?i)No tests (executed|found)`)

// FromProcess classifies a finished check process into a result.
//
// A phpunit run that discovered no tests is the distinguishable NoTests
// outcome regardless of exit code — PHPUnit 11.5 exits 0 for it, and a silent
// pass would hide the fact that nothing ran. Otherwise a zero exit is a pass
// and any non-zero exit a failure.
//
// A nil exitCode is the child having reported no status at all. Only exit 0 is
// a pass, so "no status" is a failure, never mistaken for one.
func FromProcess(checkType Type, exitCode *int, output string, duration time.Duration) Result {
	status := Failed
	switch {
	case checkType == PhpUnit && noTestsRan.MatchString(output):
		status = NoTests
	case exitCode != nil && *exitCode == 0:
		status = Passed
	}

	return Result{Type: checkType, Status: status, ExitCode: exitCode, Output: output, Duration: duration}
}

// TimedOut is a check that exceeded its timebox: a failure with the reason
// recorded ahead of whatever partial output the run produced.
func TimedOut(checkType Type, partialOutput string, duration, timeout time.Duration) Result {
	return Result{
		Type:     checkType,
		Status:   Failed,
		Output:   fmt.Sprintf("Check timed out after %ds.\n%s", int(timeout.Seconds()), partialOutput),
		Duration: duration,
	}
}

// NotAvailable is a check the engine cannot run for this environment —
// recorded explicitly in the result set, never silently omitted.
func NotAvailable(checkType Type, reason string) Result {
	return Result{Type: checkType, Status: Unavailable, Output: reason}
}

// Passed reports whether this result permits an all-green verdict.
func (r Result) Passed() bool { return r.Status.Passed() }

// Statuses is every status a check can hold.
//
// Enumerated here rather than at each consumer, because there are three that
// have to agree: the cache refuses a status not on this list, the
// merge-request comment gives each one a word, and the console report colours
// them. A status added without this list would be accepted by none of them.
func Statuses() []Status { return []Status{Passed, Failed, NoTests, Unavailable} }

// OutputExcerpt is the trailing excerpt of this check's output — what the
// merge-request comment quotes.
//
// One truncation rule, so no two surfaces disagree about what "the last N
// bytes" means. A maxBytes of 0 or less takes the default.
func (r Result) OutputExcerpt(maxBytes int) string {
	excerpt, _ := r.OutputTail(maxBytes)

	return excerpt
}

// OutputTail is the trailing excerpt and how many whole lines it left out.
//
// **It cuts on a line boundary.** It used to slice raw bytes off the tail, so
// the excerpt opened on the back half of whatever line the budget landed in —
// reported from the wild as output beginning `ssertSame() when expecting` and
// `tion extends to column 143`. A reader cannot tell that from a check whose
// output is genuinely strange, so the first thing they do is doubt the tool.
//
// Byte slicing was also not UTF-8 safe, and these checks emit multi-byte
// characters: phpstan labels every finding with an emoji identifier. A cut
// inside one of those put a replacement character at the front of the report.
// A newline is never part of a multi-byte rune, so cutting after one lands on
// a rune boundary by construction; the loop afterwards covers the single line
// too long to hold a newline at all.
//
// The dropped count is returned rather than rendered, so each surface words it
// for its own reader, and so no surface can print a number this did not
// measure.
func (r Result) OutputTail(maxBytes int) (excerpt string, droppedLines int) {
	if maxBytes <= 0 {
		maxBytes = ExcerptBytes
	}

	output := strings.TrimSpace(r.Output)
	if len(output) <= maxBytes {
		return output, 0
	}

	tail := output[len(output)-maxBytes:]
	if newline := strings.IndexByte(tail, '\n'); newline >= 0 {
		tail = tail[newline+1:]
	}
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}

	return strings.TrimSpace(tail), strings.Count(output[:len(output)-len(tail)], "\n")
}

// RunResult is the aggregate outcome of running a set of checks: one entry per
// requested check, in run order.
type RunResult struct {
	Results []Result
}

// AllPassed reports whether nothing in the run failed.
func (r RunResult) AllPassed() bool {
	for _, result := range r.Results {
		if !result.Passed() {
			return false
		}
	}

	return true
}

// Failures is every result that blocks an all-green verdict, in run order.
func (r RunResult) Failures() []Result {
	failures := []Result{}
	for _, result := range r.Results {
		if !result.Passed() {
			failures = append(failures, result)
		}
	}

	return failures
}

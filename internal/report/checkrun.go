package report

import (
	"github.com/owenbush/upkeep/internal/check"
)

// CheckPayload is `check` and `patch:check`.
//
// The one payload where an agent can act on the envelope alone: Passed answers
// the question, and the per-check detail is there for the agent that wants to
// say *what* failed without re-running anything.
type CheckPayload struct {
	Subject Subject       `json:"subject"`
	Passed  bool          `json:"passed"`
	Checks  []CheckResult `json:"checks"`
	// Fixture is the fixture loaded before the run, empty when none was.
	Fixture string `json:"fixture,omitempty"`
}

// Subject is what was checked.
//
// Kind separates the three things that can be: a merge request, a patch, or
// whatever the working copy holds. An agent reading a cached verdict needs to
// know which, because only the first two have a revision anything can be
// matched against — `--working-copy` deliberately caches nothing.
type Subject struct {
	Kind   string `json:"kind"`
	Module string `json:"module"`
	Core   string `json:"core"`
	// IID is the merge request, 0 otherwise.
	IID int `json:"iid,omitempty"`
	// Nid is the patch's issue, 0 otherwise.
	Nid int `json:"nid,omitempty"`
	// Revision is the SHA or patch revision the verdict is about, empty for a
	// working copy.
	Revision string `json:"revision,omitempty"`
	// Branch is what the working copy was on, for a working-copy check. Empty
	// otherwise, because the other two kinds are checked on a disposable
	// branch whose name says nothing a consumer wants.
	Branch string `json:"branch,omitempty"`
}

// CheckResult is one suite's outcome.
type CheckResult struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	// ExitCode is null for a check that never ran, which is different from one
	// that ran and returned zero.
	ExitCode *int    `json:"exit_code"`
	Seconds  float64 `json:"seconds"`
	// Output is the tail of what the check printed, bounded exactly as the
	// console report bounds it, with OmittedLines saying how much came before.
	// A consumer that needs all of it re-runs with -v; this is not a log
	// archive.
	Output       string `json:"output,omitempty"`
	OmittedLines int    `json:"omitted_lines,omitempty"`
}

// CheckRun builds the payload for a completed run.
//
// The excerpt comes from check.Result.OutputTail, the same call the console
// report makes, so the JSON and the terminal quote the same bytes and agree
// about how much they left out.
func CheckRun(run check.RunResult, subject Subject, fixture string) CheckPayload {
	results := make([]CheckResult, 0, len(run.Results))
	for _, result := range run.Results {
		output, omitted := result.OutputTail(check.ConsoleExcerptBytes)

		results = append(results, CheckResult{
			Check:        string(result.Type),
			Status:       string(result.Status),
			ExitCode:     result.ExitCode,
			Seconds:      result.Duration.Seconds(),
			Output:       output,
			OmittedLines: omitted,
		})
	}

	return CheckPayload{
		Subject: subject,
		Passed:  run.AllPassed(),
		Checks:  results,
		Fixture: fixture,
	}
}

package workflow

import (
	"testing"

	"github.com/owenbush/upkeep/internal/check"
)

func code(value int) *int { return &value }

// The distinction a script relies on: 1 means "look at the subject", 2 means
// "look at your setup".
func TestEveryNonZeroChildCodeCollapsesToFailed(t *testing.T) {
	// 2 especially: passed through, a child exiting 2 would be read as an
	// upkeep infrastructure failure.
	for _, childCode := range []int{1, 2, 3, 127, 255} {
		if got := ForChildProcess(code(childCode)); got != Failed {
			t.Errorf("child exit %d mapped to %d, want %d", childCode, got, Failed)
		}
	}
}

func TestAChildThatSucceededIsOK(t *testing.T) {
	if got := ForChildProcess(code(0)); got != OK {
		t.Errorf("got %d, want %d", got, OK)
	}
}

// A process that produced no exit code at all never ran, which is a statement
// about the setup rather than a verdict about the command.
func TestAChildThatNeverRanIsInfrastructure(t *testing.T) {
	if got := ForChildProcess(nil); got != Infrastructure {
		t.Errorf("got %d, want %d", got, Infrastructure)
	}
}

// The honest non-failures are non-failures here too.
func TestACheckRunMapsToItsVerdict(t *testing.T) {
	green := check.RunResult{Results: []check.Result{
		{Type: check.PhpUnit, Status: check.NoTests},
		{Type: check.Deprecation, Status: check.Unavailable},
	}}
	if got := ForRun(green); got != OK {
		t.Errorf("got %d, want %d", got, OK)
	}

	red := check.RunResult{Results: []check.Result{{Type: check.PhpCs, Status: check.Failed}}}
	if got := ForRun(red); got != Failed {
		t.Errorf("got %d, want %d", got, Failed)
	}
}

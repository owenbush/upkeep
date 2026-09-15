// Package workflow holds the CLI-wide contracts a command answers with.
package workflow

import "github.com/owenbush/upkeep/internal/check"

// The exit-code contract, kept in one place so scripting can rely on it. Every
// upkeep command answers with one of exactly three codes:
//
//	0 — OK: the command did what was asked. (check.NoTests and
//	    check.Unavailable are honest non-failures, matching Status.Passed; so
//	    is a documented degraded mode, such as the patch surface
//	    cross-referencing fewer merge requests because no token is
//	    configured.)
//	1 — Failed: upkeep worked, but the work it supervised reported failure — a
//	    red check, a merge GitLab refused, or a non-zero exit from a command
//	    run through `upkeep exec`.
//	2 — Infrastructure: upkeep could not do the job at all, so there is no
//	    verdict to report — bad usage, no cockpit or registry, no GitLab
//	    token, an unregistered module, an untracked core version, an
//	    unresolvable merge request, or an engine/API failure.
//
// The distinction that matters to a script: 1 means "look at the subject", 2
// means "look at your setup". A command never returns any other value —
// notably, `upkeep exec` collapses every non-zero child code to 1 rather than
// passing it through, so a child exiting 2 can never be mistaken for an upkeep
// infrastructure failure.
const (
	OK             = 0
	Failed         = 1
	Infrastructure = 2
)

// ForRun is the code a completed check run answers with.
func ForRun(run check.RunResult) int {
	if run.AllPassed() {
		return OK
	}

	return Failed
}

// ForChildProcess is a wrapped command's outcome.
//
// Every non-zero code collapses to Failed; a process that produced no exit
// code at all never ran, which is an infrastructure failure rather than a
// verdict about the command.
func ForChildProcess(childExitCode *int) int {
	switch {
	case childExitCode == nil:
		return Infrastructure
	case *childExitCode == 0:
		return OK
	default:
		return Failed
	}
}

package proc

import (
	"io"
	"os/exec"

	"github.com/owenbush/upkeep/internal/security"
)

// Passthrough runs a command with its streams wired straight to the caller's,
// and reports the exit code the child chose.
//
// The seam `upkeep exec` needs, and it lives here for the same reason
// everything else does: this is the package that strips the credential from a
// child's environment, and a child started anywhere else would inherit it.
//
// Unlike Run and its siblings, nothing here buffers, captures, or line-splits.
// The command being run is the operator's own, its output is the answer rather
// than progress, and an interactive one — a shell, a REPL, a pager — needs its
// streams unmediated. That also means the output is *not* redacted: a redactor
// has to see whole lines, which would break exactly those cases, and the
// credential is already absent from the environment the child was given. What
// the operator's own command chooses to print is theirs.
//
// There is no timeout, deliberately. This wraps a command somebody typed, and
// a shell that gets killed after an hour of being useful is not a safety
// feature.
//
// A nil exit code means the child never ran at all — no such binary, no
// permission — which is upkeep failing rather than a verdict about the
// command.
func Passthrough(
	command []string, dir string, stdin io.Reader, stdout, stderr io.Writer,
) (*int, error) {
	if len(command) == 0 {
		return nil, errNothingToRun
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = security.ScrubbedEnvironment()
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// Deliberately *not* put in a process group of its own, which is what the
	// timed calls do so a cancel can kill a launcher's whole tree. There is
	// nothing to cancel here, and a child in its own group would stop
	// receiving the terminal's signals: Ctrl-C at an `upkeep exec … -- …`
	// would reach upkeep and leave the command somebody was trying to stop
	// running. Sharing the group is what makes an interactive wrapper behave
	// like no wrapper at all.

	runErr := cmd.Run()

	if cmd.ProcessState != nil {
		if code := cmd.ProcessState.ExitCode(); code >= 0 {
			return &code, nil
		}
	}

	return nil, runErr
}

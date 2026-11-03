package proc

import (
	"context"
	"os/exec"
	"slices"
	"strings"

	"github.com/owenbush/upkeep/internal/security"
)

// Outcome is one child's result with its streams kept apart, for callers that
// parse stdout and diagnose from stderr.
type Outcome struct {
	// Stdout and Stderr are redacted.
	Stdout []byte
	Stderr []byte
	// ExitCode is nil when the child produced no status of its own: it never
	// started, or it was killed (including by ctx).
	ExitCode *int
	// Err is set whenever the child did not exit zero; redacted.
	Err error
}

// Exec runs one child under ctx, with the scrubbed environment plus
// credentials: variables the child is deliberately handed, each by name.
//
// This is the one sanctioned way a child receives a credential, and it is
// explicit at the call site rather than inherited: the inherited
// UPKEEP_GITLAB_TOKEN is still removed, an inherited variable of the same name
// as a handed-over one is replaced, and every handed-over value is redacted
// from everything returned, alongside the usual credential values.
// drupalorg-cli is the caller it exists for — it authenticates to GitLab from
// DRUPALORG_GITLAB_TOKEN and offers no other way that keeps the token out of
// argv.
func Exec(ctx context.Context, command []string, credentials map[string]string) Outcome {
	if len(command) == 0 {
		return Outcome{Err: errNothingToRun}
	}

	env := security.ScrubbedEnvironment()
	secrets := security.CredentialValues()
	for name, value := range credentials {
		env = slices.DeleteFunc(env, func(entry string) bool { return strings.HasPrefix(entry, name+"=") })
		env = append(env, name+"="+value)
		secrets = append(secrets, value)
	}
	redactor := security.NewRedactor(secrets...)

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = env
	killWholeGroup(cmd)

	var stdout, stderr syncBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	outcome := Outcome{
		Stdout: []byte(redactor.Redact(stdout.String())),
		Stderr: []byte(redactor.Redact(stderr.String())),
	}
	if cmd.ProcessState != nil && ctx.Err() == nil {
		if code := cmd.ProcessState.ExitCode(); code >= 0 {
			outcome.ExitCode = &code
		}
	}
	if runErr != nil {
		outcome.Err = redactedError{redactor.Redact(runErr.Error())}
	}

	return outcome
}

type redactedError struct{ message string }

func (e redactedError) Error() string { return e.message }

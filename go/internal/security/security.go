// Package security keeps credentials out of child processes and out of output.
package security

import (
	"os"
	"sort"
	"strings"
)

// CredentialVars are the environment variables that may hold a credential.
var CredentialVars = []string{"UPKEEP_GITLAB_TOKEN"}

// Mask replaces a credential wherever it would otherwise be printed.
const Mask = "[REDACTED]"

// minSecretLength ignores values shorter than this. Masking them would corrupt
// far more output than it protects, and no credential upkeep handles is that
// short.
const minSecretLength = 8

// Redactor removes credential material from text before it is logged,
// rendered or cached.
type Redactor struct {
	secrets []string // longest first, so a secret containing another still masks fully
}

// NewRedactor builds a redactor for the given secrets, ignoring any too short
// to be one.
func NewRedactor(secrets ...string) *Redactor {
	seen := map[string]bool{}
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if len(secret) >= minSecretLength {
			seen[secret] = true
		}
	}

	usable := make([]string, 0, len(seen))
	for secret := range seen {
		usable = append(usable, secret)
	}
	sort.Slice(usable, func(i, j int) bool {
		if len(usable[i]) != len(usable[j]) {
			return len(usable[i]) > len(usable[j])
		}

		return usable[i] < usable[j] // stable, so the result does not vary run to run
	})

	return &Redactor{secrets: usable}
}

// RedactorFromEnvironment builds one seeded from the credentials this process
// was given.
func RedactorFromEnvironment() *Redactor {
	return NewRedactor(CredentialValues()...)
}

// Redact masks every occurrence of every secret.
func (r *Redactor) Redact(text string) string {
	for _, secret := range r.secrets {
		text = strings.ReplaceAll(text, secret, Mask)
	}

	return text
}

// ScrubbedEnvironment is this process's environment with every credential
// removed — not blanked, removed.
//
// Every child upkeep spawns is arbitrary and could echo its environment into
// output that gets logged, so none of them gets the token. There are no
// exceptions; the one that existed went with the browser UI.
func ScrubbedEnvironment() []string {
	return scrub(os.Environ())
}

func scrub(environ []string) []string {
	drop := map[string]bool{}
	for _, name := range CredentialVars {
		drop[name] = true
	}

	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, found := strings.Cut(entry, "=")
		if found && drop[name] {
			continue
		}
		kept = append(kept, entry)
	}

	return kept
}

// CredentialValues are the credentials present in this process's environment,
// for redaction. Never printed, never persisted.
func CredentialValues() []string {
	var values []string
	for _, name := range CredentialVars {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			values = append(values, value)
		}
	}

	return values
}

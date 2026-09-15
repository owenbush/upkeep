package security

import (
	"strings"
	"testing"
)

func TestReplacesEveryOccurrenceOfEachSecret(t *testing.T) {
	r := NewRedactor("glpat-aaaaaaaaaaaa", "second-secret-value")

	got := r.Redact("using glpat-aaaaaaaaaaaa and second-secret-value and glpat-aaaaaaaaaaaa again")

	if strings.Contains(got, "glpat-") || strings.Contains(got, "second-secret-value") {
		t.Fatalf("a secret survived redaction: %q", got)
	}
	if strings.Count(got, Mask) != 3 {
		t.Errorf("masked %d times, want 3: %q", strings.Count(got, Mask), got)
	}
}

func TestLeavesTextWithoutSecretsUntouched(t *testing.T) {
	r := NewRedactor("glpat-aaaaaaaaaaaa")

	if got := r.Redact("nothing to see"); got != "nothing to see" {
		t.Errorf("got %q", got)
	}
}

func TestWithNoSecretsIsAnIdentityFunction(t *testing.T) {
	if got := NewRedactor().Redact("glpat-aaaaaaaaaaaa"); got != "glpat-aaaaaaaaaaaa" {
		t.Errorf("got %q, want the text unchanged", got)
	}
}

// Masking a short value corrupts far more output than it protects, and no
// credential upkeep handles is that short.
func TestIgnoresEmptyAndImplausiblyShortSecrets(t *testing.T) {
	r := NewRedactor("", "   ", "abc", "1234567")

	if got := r.Redact("abc 1234567 and more"); got != "abc 1234567 and more" {
		t.Errorf("got %q, want the text unchanged", got)
	}
}

// A secret that contains another must still mask fully, so the longer one is
// replaced first.
func TestLongestSecretIsMaskedFirst(t *testing.T) {
	r := NewRedactor("glpat-aaaaaaaaaaaa", "glpat-aaaaaaaaaaaa-extended")

	got := r.Redact("token glpat-aaaaaaaaaaaa-extended here")

	if got != "token "+Mask+" here" {
		t.Errorf("got %q, want the whole longer secret masked", got)
	}
}

func TestFromEnvironmentPicksUpTheCredentialVariable(t *testing.T) {
	t.Setenv(CredentialVars[0], "glpat-from-the-environment")

	if got := RedactorFromEnvironment().Redact("saw glpat-from-the-environment"); !strings.Contains(got, Mask) {
		t.Errorf("got %q, want the environment credential masked", got)
	}
}

// Removed, not blanked: an empty credential is still a credential the child
// can read, and upkeep's own token resolution treats present-but-empty
// differently from absent.
func TestScrubbedEnvironmentRemovesEveryCredentialVariable(t *testing.T) {
	environ := []string{"PATH=/usr/bin", CredentialVars[0] + "=glpat-secret", "HOME=/home/someone"}

	got := scrub(environ)

	for _, entry := range got {
		if strings.HasPrefix(entry, CredentialVars[0]+"=") {
			t.Fatalf("the credential survived: %v", got)
		}
	}
	if len(got) != 2 {
		t.Errorf("kept %d variables, want the other 2: %v", len(got), got)
	}
}

func TestCredentialValuesReadsWhatIsSetAndIgnoresBlanks(t *testing.T) {
	t.Setenv(CredentialVars[0], "  glpat-padded  ")
	if got := CredentialValues(); len(got) != 1 || got[0] != "glpat-padded" {
		t.Errorf("got %v, want the trimmed value", got)
	}

	t.Setenv(CredentialVars[0], "   ")
	if got := CredentialValues(); len(got) != 0 {
		t.Errorf("got %v, want nothing for a blank variable", got)
	}
}

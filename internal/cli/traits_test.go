package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// What a command declares comes back unchanged.
func TestTraitsRoundTrip(t *testing.T) {
	cmd := &cobra.Command{Use: "check"}
	want := Traits{
		Changes:    ChangesEnvironment,
		Credential: CredentialRequired,
		Destroys:   "force-updates a branch it owns",
		Fixture:    true,
		JSON:       true,
	}

	Describe(cmd, want)

	got, declared := TraitsOf(cmd)
	if !declared {
		t.Fatal("nothing was declared")
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A command nobody described reports false, which is what the invariant suite
// keys on — the whole point is that a new command cannot slip through with a
// blank row in a table about destructiveness.
func TestAnUndescribedCommandReportsFalse(t *testing.T) {
	if traits, declared := TraitsOf(&cobra.Command{Use: "new"}); declared {
		t.Errorf("an undescribed command declared %+v", traits)
	}
}

// Annotations cobra or anything else set are kept: Describe adds to the map
// rather than replacing it.
func TestDescribeKeepsOtherAnnotations(t *testing.T) {
	cmd := &cobra.Command{Use: "check", Annotations: map[string]string{"group": "checks"}}

	Describe(cmd, Traits{Changes: ChangesNothing, Credential: CredentialNone})

	if cmd.Annotations["group"] != "checks" {
		t.Errorf("it dropped an annotation it did not own: %v", cmd.Annotations)
	}
}

// Not declaring a fixture is not the same as declaring it absent, so the
// round trip has to report false rather than an empty string.
func TestAbsentTraitsReadAsTheirZeroValue(t *testing.T) {
	cmd := &cobra.Command{Use: "dashboard"}
	Describe(cmd, Traits{Changes: ChangesCockpit, Credential: CredentialNone})

	traits, _ := TraitsOf(cmd)
	if traits.Fixture {
		t.Error("it invented a fixture flag")
	}
	if traits.Destroys != "" {
		t.Errorf("it invented a destroys phrase: %q", traits.Destroys)
	}
}

// The vocabularies are the enumerated sets the reference renders and the
// invariant suite checks against.
func TestTheVocabulariesAreEnumerated(t *testing.T) {
	if len(KnownChanges()) != 5 {
		t.Errorf("KnownChanges has %d entries", len(KnownChanges()))
	}
	if len(KnownCredentials()) != 2 {
		t.Errorf("KnownCredentials has %d entries", len(KnownCredentials()))
	}

	seen := map[Changes]bool{}
	for _, change := range KnownChanges() {
		if change == "" {
			t.Error("an empty value is in the vocabulary")
		}
		if seen[change] {
			t.Errorf("%q is listed twice", change)
		}
		seen[change] = true
	}
}

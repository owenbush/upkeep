package command

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
)

// documentedCommands is every command the reference describes.
func documentedCommands(t *testing.T) []*cobra.Command {
	t.Helper()

	var documented []*cobra.Command
	for _, child := range NewRootFor(noVolumes{}, noSizer).Commands() {
		if child.Name() == "help" || child.Hidden {
			continue
		}
		documented = append(documented, child)
	}
	if len(documented) < 25 {
		t.Fatalf("only %d commands found; the tree is not being built", len(documented))
	}

	return documented
}

// Every command says what it does to the world.
//
// This is the check that matters, because the failure it prevents is silent:
// a command added without traits renders a blank row in the reference, and a
// blank row in a table about *destructiveness* reads as "this one is safe".
func TestEveryCommandDeclaresItsTraits(t *testing.T) {
	for _, cmd := range documentedCommands(t) {
		if _, declared := cli.TraitsOf(cmd); !declared {
			t.Errorf("%s declares no traits — add cli.Describe to its constructor", cmd.Name())
		}
	}
}

// And the values come from the enumerated vocabulary, because a value outside
// it renders as a word nobody defined.
func TestDeclaredTraitsUseTheKnownVocabulary(t *testing.T) {
	changes := map[cli.Changes]bool{}
	for _, known := range cli.KnownChanges() {
		changes[known] = true
	}
	credentials := map[cli.Credential]bool{}
	for _, known := range cli.KnownCredentials() {
		credentials[known] = true
	}

	for _, cmd := range documentedCommands(t) {
		traits, declared := cli.TraitsOf(cmd)
		if !declared {
			continue
		}
		if !changes[traits.Changes] {
			t.Errorf("%s declares Changes %q, which is not in the vocabulary", cmd.Name(), traits.Changes)
		}
		if !credentials[traits.Credential] {
			t.Errorf("%s declares Credential %q, which is not in the vocabulary",
				cmd.Name(), traits.Credential)
		}
	}
}

// The declared fixture trait and the actual flag agree.
//
// The cross-check is the point: the reference is only worth having if it
// cannot say a command takes --fixture when it does not, or stay silent when a
// command gains one. Either direction is caught here, which is why this is
// asserted against cobra's own flag set rather than against a second list.
func TestTheFixtureTraitMatchesTheFlag(t *testing.T) {
	for _, cmd := range documentedCommands(t) {
		traits, declared := cli.TraitsOf(cmd)
		if !declared {
			continue
		}

		hasFlag := cmd.Flags().Lookup("fixture") != nil
		if traits.Fixture != hasFlag {
			t.Errorf("%s: declared Fixture=%t but the --fixture flag is %s",
				cmd.Name(), traits.Fixture,
				map[bool]string{true: "present", false: "absent"}[hasFlag])
		}
	}
}

// A command that needs a credential is one that writes somewhere upkeep does
// not own.
//
// Not a style rule — it is the stance the whole read surface rests on: reading
// git.drupalcode.org is anonymous, so a command demanding a token while only
// touching local state would be imposing a restriction GitLab does not. The
// one exception is declared here rather than waived silently.
func TestOnlyWritingCommandsRequireACredential(t *testing.T) {
	// modules:add reads *your* GitLab memberships to find what you maintain,
	// which is a read that cannot be anonymous — there is no "you" without a
	// credential. It writes only the registry.
	allowed := map[string]bool{"modules:add": true}

	for _, cmd := range documentedCommands(t) {
		traits, declared := cli.TraitsOf(cmd)
		if !declared || traits.Credential != cli.CredentialRequired {
			continue
		}
		if traits.Changes == cli.ChangesRemote || allowed[cmd.Name()] {
			continue
		}
		t.Errorf("%s requires a credential but changes %q; reading needs none",
			cmd.Name(), traits.Changes)
	}
}

// A destroys phrase completes "it ...", so it reads as a sentence wherever it
// is rendered.
func TestDestroysReadsAsAPhrase(t *testing.T) {
	for _, cmd := range documentedCommands(t) {
		traits, declared := cli.TraitsOf(cmd)
		if !declared || traits.Destroys == "" {
			continue
		}
		if strings.HasSuffix(traits.Destroys, ".") {
			t.Errorf("%s: Destroys ends in a full stop: %q", cmd.Name(), traits.Destroys)
		}
		first := rune(traits.Destroys[0])
		if first >= 'A' && first <= 'Z' {
			t.Errorf("%s: Destroys starts with a capital, so it will not read as a phrase: %q",
				cmd.Name(), traits.Destroys)
		}
	}
}

// Nothing that only reads claims to destroy anything.
func TestAReadOnlyCommandDestroysNothing(t *testing.T) {
	for _, cmd := range documentedCommands(t) {
		traits, declared := cli.TraitsOf(cmd)
		if !declared {
			continue
		}
		if traits.Changes == cli.ChangesNothing && traits.Destroys != "" {
			t.Errorf("%s changes nothing but declares it destroys %q",
				cmd.Name(), traits.Destroys)
		}
	}
}

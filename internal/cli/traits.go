package cli

import "github.com/spf13/cobra"

// What a command does to the world, declared at the command itself.
//
// Thirty commands is enough that "which one do I want, and what will it do to
// me" stopped being answerable by reading the list. The generated reference
// already carries every flag and help string; what it could not say is the
// part people actually hesitate over — whether a command writes anything,
// whether it can lose work, and whether it needs a credential.
//
// **Declared, not inferred.** The alternative was a hand-written table, and
// the whole reason `docs/commands.md` is generated is that a second copy of
// the truth loses: the site's own table sat at 11 of 27 commands for months.
// A trait table written by hand would drift the first time a command gained a
// flag. So each command says what it is, docgen renders it, and
// `internal/invariant` fails the build when a command declares nothing —
// which is what stops the next command added from being the one with a blank
// row.
//
// Carried in cobra's own Annotations rather than a parallel registry, so a
// command and its traits cannot be registered in two places and disagree.
type Traits struct {
	// Changes is the most consequential thing this command touches.
	Changes Changes

	// Credential is whether a GitLab token is needed. Reading is anonymous
	// across upkeep's whole read surface, so this is mostly "none".
	Credential Credential

	// Destroys names what running this can overwrite or delete, as a phrase
	// completing "it ...". Empty means nothing is lost by running it, which is
	// the answer for most of the surface and worth being able to state.
	//
	// Free text rather than a boolean because "destructive" on its own tells
	// somebody to be careful without telling them of what: `check` resetting a
	// branch it owns and `prune` deleting environments are both "yes" and want
	// different reactions.
	Destroys string

	// Fixture is whether the command honours --fixture.
	Fixture bool

	// JSON is whether the command honours --json.
	//
	// Declared as well as flagged so the reference cannot claim structured
	// output a command does not have, which for an agent reading the
	// reference to decide what to call is worse than no reference at all.
	JSON bool
}

// Changes names what a command writes to.
type Changes string

// The vocabulary. Deliberately short: these are rendered in a table column and
// read at a glance, so five values that mean different things beat a precise
// taxonomy nobody scans.
const (
	// ChangesNothing is a command that only reads and prints.
	ChangesNothing Changes = "nothing"
	// ChangesCockpit writes inside the cockpit — the registry, base artifacts,
	// cached results, snapshots.
	ChangesCockpit Changes = "the cockpit"
	// ChangesEnvironment writes inside a provisioned environment: its
	// codebase, its database, or the disposable branches upkeep owns there.
	ChangesEnvironment Changes = "an environment"
	// ChangesWorkingCopy writes to the module git checkout — a branch you
	// keep, rather than one upkeep resets.
	ChangesWorkingCopy Changes = "your working copy"
	// ChangesRemote writes to drupal.org: a push, a merge request, a comment,
	// a merge.
	ChangesRemote Changes = "drupal.org"
)

// Credential is whether a GitLab token is required.
type Credential string

const (
	// CredentialNone needs no token at all.
	CredentialNone Credential = "none"
	// CredentialRequired refuses without one, before doing any work.
	CredentialRequired Credential = "required"
)

// The annotation keys. Namespaced, because cobra uses Annotations itself.
const (
	annotationChanges    = "upkeep.changes"
	annotationCredential = "upkeep.credential"
	annotationDestroys   = "upkeep.destroys"
	annotationFixture    = "upkeep.fixture"
	annotationJSON       = "upkeep.json"
)

// Describe records what a command does, for the generated reference.
//
// Called by every command's constructor. The invariant suite checks that it
// was, so this cannot be the thing somebody forgets.
func Describe(cmd *cobra.Command, traits Traits) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[annotationChanges] = string(traits.Changes)
	cmd.Annotations[annotationCredential] = string(traits.Credential)
	cmd.Annotations[annotationDestroys] = traits.Destroys
	if traits.Fixture {
		cmd.Annotations[annotationFixture] = "yes"
	}
	if traits.JSON {
		cmd.Annotations[annotationJSON] = "yes"
	}
}

// TraitsOf reads back what a command declared, reporting false when it
// declared nothing.
func TraitsOf(cmd *cobra.Command) (Traits, bool) {
	changes, declared := cmd.Annotations[annotationChanges]
	if !declared {
		return Traits{}, false
	}

	return Traits{
		Changes:    Changes(changes),
		Credential: Credential(cmd.Annotations[annotationCredential]),
		Destroys:   cmd.Annotations[annotationDestroys],
		Fixture:    cmd.Annotations[annotationFixture] == "yes",
		JSON:       cmd.Annotations[annotationJSON] == "yes",
	}, true
}

// KnownChanges and KnownCredentials are the accepted values, enumerated here
// because the invariant suite checks against them and a value outside the set
// would render as a word nobody defined.
func KnownChanges() []Changes {
	return []Changes{
		ChangesNothing, ChangesCockpit, ChangesEnvironment,
		ChangesWorkingCopy, ChangesRemote,
	}
}

// KnownCredentials are the accepted credential values.
func KnownCredentials() []Credential {
	return []Credential{CredentialNone, CredentialRequired}
}

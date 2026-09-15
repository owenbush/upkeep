package adapter

import (
	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
)

// Environment is a provisioned (module x core-version) maintenance
// environment.
//
// Callers treat it as an opaque handle plus display data — the project layout
// inside it is adapter-private.
type Environment struct {
	ModuleName  string
	CoreMajor   string
	ProjectName string
	ProjectPath string
	PrimaryURL  string
	// Reused is true when ensuring reused an existing healthy environment
	// instead of provisioning one.
	Reused bool
}

// ServeResult is where a human can open the environment in a browser.
type ServeResult struct {
	// URL is the environment's primary URL, engine-assigned.
	URL string
	// LoginURL is a one-time authenticated login URL, when the engine can mint
	// one; empty otherwise.
	LoginURL string
}

// Engine is upkeep's single structural boundary around the environment engine.
//
// All knowledge of engine command names, project layout, and engine behaviour
// lives behind this interface — nothing outside this package may mention the
// engine, which an invariant test enforces by scanning the source. If a later
// feature needs an engine detail, it gets a new method here, never a shell-out
// from command code.
//
// Environment identity is always the (module x core-major) pair; the adapter
// owns how that maps to concrete projects on disk.
type Engine interface {
	// EnsureEnv provisions the environment for (module, core major), or reuses
	// the existing one when it is present and healthy — the engine reports the
	// project, and the environment meta matches module, core, seed identity
	// and pinned add-on version.
	//
	// Provisioning seeds the codebase from the canonical base tree, installs
	// the pinned engine add-on, wires the module working copy in such that no
	// composer operation can clobber it, and restores the clean-install
	// database snapshot.
	EnsureEnv(module cockpit.Module, coreMajor string) (Environment, error)

	// ApplyMr checks out the merge request's code in the environment's module
	// working copy.
	ApplyMr(environment Environment, mergeRequest gitlab.MergeRequest) error

	// ApplyPatch applies an already-downloaded patch file onto a branch off
	// the module working copy's base, and commits it so the checks that follow
	// run against a clean tree.
	//
	// A patch that does not apply is reported as an error naming the patch and
	// the base it was tried against — for a maintainer that is a review
	// finding ("needs a re-roll"), not a tool malfunction.
	ApplyPatch(environment Environment, patch PatchApplication, refresh BaseRefresh) error

	// StartWork cuts the issue's work branch from a freshly fetched base, or
	// resumes it when it already exists.
	//
	// It never resets: a work branch may hold the only copy of something a
	// human wrote.
	StartWork(environment Environment, branch IssueBranch, baseBranch string, refresh BaseRefresh) error

	// PromotePatch applies a patch onto the issue's work branch and commits it
	// under the patch author's attribution.
	//
	// partial takes every hunk that still fits and leaves the rest as .rej
	// files, committing nothing — half a patch plus rejects is not what the
	// author wrote.
	PromotePatch(
		environment Environment,
		patch PatchApplication,
		branch IssueBranch,
		message string,
		refresh BaseRefresh,
		partial bool,
	) (PatchPromotion, error)

	// PushWork pushes the work branch to the given remote and returns the
	// branch name as pushed. It never forces.
	PushWork(environment Environment, branch IssueBranch, remote GitRemote) (string, error)

	// RecordedBaseBranch is the base branch a previous apply recorded, or ""
	// when none was.
	RecordedBaseBranch(environment Environment) string

	// LoadFixture restores a named fixture into the environment's database.
	LoadFixture(environment Environment, fixtureName string) error

	// RunChecks runs the named checks, or every check when none are named.
	RunChecks(environment Environment, checks []check.Type) (check.RunResult, error)

	// Serve returns where a human can open the environment.
	Serve(environment Environment) (ServeResult, error)

	// ResolveEnvPath is the project path for a (module, core) pair, or "" when
	// there is no environment for it.
	ResolveEnvPath(moduleName, coreMajor string) string

	// InspectWorkingCopy reads the git state of the module working copy, or
	// reports false when there is no environment to read.
	InspectWorkingCopy(moduleName, coreMajor string) (WorkingCopyStatus, bool)

	// CheckoutBranch puts the module working copy on a branch.
	CheckoutBranch(environment Environment, branch string) error

	// Teardown disposes of the environment for (module, core major),
	// containers and volumes included.
	Teardown(module cockpit.Module, coreMajor string) error
}

// Factory builds an engine bound to a projects root.
//
// Commands receive one of these and never construct an engine themselves,
// which is what keeps engine selection to a single composition root.
type Factory interface {
	ForProjectsRoot(projectsRoot string) Engine
}

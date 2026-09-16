package workflow

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
)

// MrReader is the GitLab surface a merge-request resolution needs.
//
// An interface rather than the client, so a resolution can be exercised
// without a network and so the four calls it makes are visible in one place —
// this runs before every check and review, and each call is a round trip
// somebody waits for.
type MrReader interface {
	Project(moduleOrPath string) (*gitlab.Project, *gitlab.Failure)
	MergeRequest(project gitlab.Project, iid int) (*gitlab.MergeRequest, *gitlab.Failure)
	MergeRefSHA(project gitlab.Project, iid int) string
	FileContents(project gitlab.Project, path, ref string) string
}

// MrResolver is the shared context resolution for the single-merge-request
// commands: module lookup, core selection, the merge request itself, and the
// open-state and declared-core checks.
//
// Cheap local validation — the module is known, the core is one it tracks —
// runs before any network request. The native-base rule (no backport testing)
// needs the environment's working copy and so stays in the adapter's apply;
// its rejection is an infrastructure outcome, not a check verdict.
type MrResolver struct {
	modules     map[string]cockpit.Module
	client      MrReader
	coresOnDisk []string
}

// NewMrResolver builds one.
//
// coresOnDisk is the base artifact versions present, ascending, for a module
// the registry does not carry. Empty means the caller has no cockpit to ask,
// in which case only registered modules resolve — the behaviour that predates
// the watchlist split rather than a new refusal.
func NewMrResolver(
	modules map[string]cockpit.Module, client MrReader, coresOnDisk []string,
) *MrResolver {
	return &MrResolver{modules: modules, client: client, coresOnDisk: coresOnDisk}
}

// Resolve produces the context, or says why it cannot.
func (r *MrResolver) Resolve(moduleName string, iid int, requestedCore string) (MrContext, error) {
	module, err := cockpit.ResolveModule(r.modules, moduleName, r.coresOnDisk)
	if err != nil {
		return MrContext{}, err
	}

	coreMajor, err := SelectCoreVersion(module, requestedCore)
	if err != nil {
		return MrContext{}, err
	}

	project, failure := r.client.Project(module.Project)
	if failure != nil {
		return MrContext{}, fmt.Errorf(
			"%s", cockpit.ProjectFailure(r.modules, module, failure.Message),
		)
	}

	mergeRequest, err := r.openMergeRequest(module, *project, iid)
	if err != nil {
		return MrContext{}, err
	}

	if err := r.assertBranchDeclares(
		*project, module, mergeRequest.TargetBranch, coreMajor,
	); err != nil {
		return MrContext{}, err
	}

	return MrContext{
		Module:       module,
		Project:      *project,
		MergeRequest: mergeRequest,
		CoreMajor:    coreMajor,
		MergeRefSHA:  r.client.MergeRefSHA(*project, iid),
	}, nil
}

// SelectCoreVersion is the core the run targets.
//
// An explicit request must be one the module tracks. Omitted, the default is
// the first core version in the module's list — the registry order is the
// maintainer's priority order, and a derived module's list is newest first.
func SelectCoreVersion(module cockpit.Module, requestedCore string) (string, error) {
	if requestedCore == "" {
		if len(module.CoreVersions) == 0 {
			return "", fmt.Errorf(
				"no core versions are available for module %q, so there is nothing to check against",
				module.Name,
			)
		}

		return module.CoreVersions[0], nil
	}

	if !slices.Contains(module.CoreVersions, requestedCore) {
		return "", untrackedCore(module, requestedCore)
	}

	return requestedCore, nil
}

// untrackedCore is why a requested core is not available — a different
// sentence depending on where the module's core list came from.
//
// A watched module's core_versions is a line somebody wrote in registry.yml,
// so that is the thing to edit. A derived module has no entry at all: its list
// is the base artifacts on this machine, and telling a maintainer to "add it
// to core_versions in registry.yml" sends them to edit a file that does not
// mention their module. Reported from a real run — `--version=12` on an
// unregistered module answered "Its registry entry tracks: 11, 10", naming a
// registry entry that does not exist and listing the contents of a directory.
func untrackedCore(module cockpit.Module, requestedCore string) error {
	// Listed ascending here whatever the internal order: newest-first exists
	// so that the first entry is the default, and it reads as a mistake in
	// prose.
	available := slices.Clone(module.CoreVersions)
	sort.Slice(available, func(a, b int) bool {
		left, _ := strconv.Atoi(available[a])
		right, _ := strconv.Atoi(available[b])

		return left < right
	})

	if module.Watched {
		return fmt.Errorf(
			"module %q does not track core version %q. Its registry entry tracks: %s. "+
				"Add it to core_versions in registry.yml to check against it",
			module.Name, requestedCore, strings.Join(available, ", "),
		)
	}

	return fmt.Errorf(
		"no base artifacts for core %s, so %q cannot be checked against it.\n"+
			"Built here: %s.\nBuild another with: upkeep base-artifacts:build --version=%s",
		requestedCore, module.Name, strings.Join(available, ", "), requestedCore,
	)
}

// openMergeRequest fetches the merge request and refuses one that is not open.
func (r *MrResolver) openMergeRequest(
	module cockpit.Module, project gitlab.Project, iid int,
) (gitlab.MergeRequest, error) {
	mergeRequest, failure := r.client.MergeRequest(project, iid)
	if failure != nil {
		return gitlab.MergeRequest{}, fmt.Errorf(
			"MR !%d of module %q could not be resolved (not found or inaccessible): %s",
			iid, module.Name, failure.Message,
		)
	}

	if mergeRequest.State != "opened" {
		return gitlab.MergeRequest{}, fmt.Errorf(
			"MR !%d (%q) is %s — only open MRs can be checked or reviewed. %s",
			mergeRequest.IID, mergeRequest.Title, mergeRequest.State, mergeRequest.WebURL,
		)
	}

	return *mergeRequest, nil
}

// assertBranchDeclares refuses a core the merge request's target branch does
// not declare.
//
// Checking a branch on a core it never claimed produces a failure that says
// nothing about the module — composer refuses to resolve, and the report reads
// as though the contribution is broken. The same reasoning removed the core
// multiplier from the dashboard: evidence gathered against a core the branch
// does not support is not evidence.
//
// It matters more now the core can be inferred. A module the registry does not
// carry takes the newest core with base artifacts on this machine, which is a
// fact about the disk and knows nothing about the branch — so without this,
// upkeep would pick a core and then blame the module for it.
//
// Silence is the answer whenever the branch cannot be read. No info.yml at
// that path, a constraint nobody can parse, a closed endpoint: each of them
// means upkeep does not know, and refusing on not-knowing would block work
// over a file it merely failed to fetch.
func (r *MrResolver) assertBranchDeclares(
	project gitlab.Project, module cockpit.Module, branch, core string,
) error {
	constraint := drupal.ConstraintIn(r.client.FileContents(project, module.Name+".info.yml", branch))
	if constraint == "" {
		return nil
	}

	// The cores worth *suggesting* are the ones this machine could run: what
	// is built, or failing that what the registry entry tracks. Naming a core
	// with no base artifacts would answer one refusal with another.
	usable := r.coresOnDisk
	if len(usable) == 0 {
		usable = module.CoreVersions
	}

	declared, known := drupal.CompatibilityFrom(constraint, usable)
	if !known || declared.Declares(core) {
		return nil
	}

	remedy := "Build base artifacts for a core it declares: " +
		"upkeep base-artifacts:build --version=<core>"
	if len(declared.Cores) > 0 {
		remedy = "Pass --version=" + strings.Join(declared.Cores, " or --version=") + "."
	}

	return fmt.Errorf(
		"%s %s declares core_version_requirement %q, which does not include core %s.\n"+
			"Checking it there would fail for reasons that say nothing about the module.\n%s",
		module.Name, branch, constraint, core, remedy,
	)
}

// RequireModule is the one strict registry lookup: every command that narrows
// into the watchlist resolves a name here, so "not registered" has a single
// wording — and one that lists what is registered.
func RequireModule(
	modules map[string]cockpit.Module, moduleName string,
) (cockpit.Module, error) {
	if module, registered := modules[moduleName]; registered {
		return module, nil
	}

	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)

	registered := "(none)"
	if len(names) > 0 {
		registered = strings.Join(names, ", ")
	}

	return cockpit.Module{}, fmt.Errorf(
		"module %q is not registered in the cockpit. Registered modules: %s",
		moduleName, registered,
	)
}

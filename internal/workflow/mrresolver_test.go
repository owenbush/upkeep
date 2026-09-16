package workflow

import (
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/patches"
)

// fakeGitlab answers the four calls a resolution makes, and records them.
type fakeGitlab struct {
	asked []string

	project        *gitlab.Project
	projectFailure *gitlab.Failure
	mergeRequest   *gitlab.MergeRequest
	mrFailure      *gitlab.Failure
	mergeRefSHA    string
	infoYAML       string
}

func aGitlab() *fakeGitlab {
	return &fakeGitlab{
		project: &gitlab.Project{
			ID: 1, Path: "pathauto", PathWithNamespace: "project/pathauto",
			WebURL: "https://git.drupalcode.org/project/pathauto",
		},
		mergeRequest: &gitlab.MergeRequest{
			IID: 12, Title: "Automated Project Update Bot fixes", State: "opened",
			SourceBranch: "project-update-bot-only", TargetBranch: "2.0.x",
			WebURL: "https://git.drupalcode.org/project/pathauto/-/merge_requests/12",
		},
		mergeRefSHA: "merge222",
	}
}

func (f *fakeGitlab) Project(moduleOrPath string) (*gitlab.Project, *gitlab.Failure) {
	f.asked = append(f.asked, "project "+moduleOrPath)

	return f.project, f.projectFailure
}

func (f *fakeGitlab) MergeRequest(_ gitlab.Project, iid int) (*gitlab.MergeRequest, *gitlab.Failure) {
	f.asked = append(f.asked, "merge request")

	return f.mergeRequest, f.mrFailure
}

func (f *fakeGitlab) MergeRefSHA(gitlab.Project, int) string {
	f.asked = append(f.asked, "merge ref")

	return f.mergeRefSHA
}

func (f *fakeGitlab) FileContents(_ gitlab.Project, path, ref string) string {
	f.asked = append(f.asked, "file "+path+"@"+ref)

	return f.infoYAML
}

func (f *fakeGitlab) didAsk(fragment string) bool {
	for _, asked := range f.asked {
		if strings.Contains(asked, fragment) {
			return true
		}
	}

	return false
}

var watched = map[string]cockpit.Module{
	"pathauto": {
		Name: "pathauto", Project: "project/pathauto",
		CoreVersions: []string{"11", "10"}, Watched: true,
	},
}

// The whole context, in one resolution.
func TestAResolutionCarriesEverythingACheckNeeds(t *testing.T) {
	client := aGitlab()

	context, err := NewMrResolver(watched, client, []string{"10", "11"}).Resolve("pathauto", 12, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if context.Module.Name != "pathauto" || context.Project.PathWithNamespace != "project/pathauto" {
		t.Errorf("got %+v", context)
	}
	if context.MergeRequest.IID != 12 {
		t.Errorf("merge request %+v", context.MergeRequest)
	}
	// The default core is the first in the module's list, which the registry
	// order makes the maintainer's priority order.
	if context.CoreMajor != "11" {
		t.Errorf("core %q", context.CoreMajor)
	}
	// What the check is actually about: the merge ref, not the branch.
	if context.MergeRefSHA != "merge222" {
		t.Errorf("merge ref %q", context.MergeRefSHA)
	}
}

// No merge ref is a state, not a failure: GitLab computes none for a merge
// request that conflicts with its target, and the adapter falls back to the
// branch there too, so both halves fall back together.
func TestNoMergeRefIsAStateRatherThanARefusal(t *testing.T) {
	client := aGitlab()
	client.mergeRefSHA = ""

	context, err := NewMrResolver(watched, client, nil).Resolve("pathauto", 12, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if context.MergeRefSHA != "" {
		t.Errorf("merge ref %q", context.MergeRefSHA)
	}
}

// Cheap local validation runs before any network request: this happens before
// every check and review, and each call is a round trip somebody waits for.
func TestAnUntrackedCoreIsRefusedBeforeAnyRequest(t *testing.T) {
	client := aGitlab()

	_, err := NewMrResolver(watched, client, nil).Resolve("pathauto", 12, "9")
	if err == nil {
		t.Fatal("it checked against a core the module does not track")
	}
	if len(client.asked) != 0 {
		t.Errorf("it went to the network anyway: %v", client.asked)
	}
}

// A watched module's core list is a line somebody wrote, so that is what to
// edit; a derived module has no entry at all and being told to edit one sends
// a maintainer to a file that does not mention their module.
func TestAnUntrackedCoreSaysTheRightThingForEachKindOfModule(t *testing.T) {
	watchedErr := SelectCoreVersionError(t, watched["pathauto"], "9")
	if !strings.Contains(watchedErr, "registry.yml") {
		t.Errorf("a watched module was not told where its list lives: %s", watchedErr)
	}
	if !strings.Contains(watchedErr, "tracks: 10, 11") {
		t.Errorf("the list is not ascending: %s", watchedErr)
	}

	derived := cockpit.Module{
		Name: "token", Project: "project/token", CoreVersions: []string{"11", "10"}, Watched: false,
	}
	derivedErr := SelectCoreVersionError(t, derived, "12")
	if strings.Contains(derivedErr, "registry.yml") {
		t.Errorf("a derived module was sent to a file that does not mention it: %s", derivedErr)
	}
	if !strings.Contains(derivedErr, "base-artifacts:build --version=12") {
		t.Errorf("the derived module was not told how to get the core: %s", derivedErr)
	}
	if !strings.Contains(derivedErr, "Built here: 10, 11") {
		t.Errorf("the list is not ascending: %s", derivedErr)
	}
}

// SelectCoreVersionError is the message a refused core produces.
func SelectCoreVersionError(t *testing.T, module cockpit.Module, requested string) string {
	t.Helper()

	_, err := SelectCoreVersion(module, requested)
	if err == nil {
		t.Fatalf("%q was accepted for %s", requested, module.Name)
	}

	return err.Error()
}

// A module with no cores at all has nothing to check against, and says so
// rather than reaching past the end of its list.
func TestAModuleWithNoCoresIsRefusedRatherThanCrashing(t *testing.T) {
	_, err := SelectCoreVersion(cockpit.Module{Name: "token"}, "")
	if err == nil {
		t.Fatal("it selected a core from an empty list")
	}
	if !strings.Contains(err.Error(), "nothing to check against") {
		t.Errorf("message %v", err)
	}
}

// Only open merge requests can be checked or reviewed, and the refusal links
// to the one it is talking about.
func TestAMergeRequestThatIsNotOpenIsRefused(t *testing.T) {
	for _, state := range []string{"merged", "closed", "locked"} {
		client := aGitlab()
		client.mergeRequest.State = state

		_, err := NewMrResolver(watched, client, nil).Resolve("pathauto", 12, "")
		if err == nil {
			t.Errorf("a %s merge request was accepted", state)

			continue
		}
		if !strings.Contains(err.Error(), state) {
			t.Errorf("the refusal does not say what state it is in: %v", err)
		}
		if !strings.Contains(err.Error(), client.mergeRequest.WebURL) {
			t.Errorf("the refusal does not link to it: %v", err)
		}
		// And nothing further was asked about it.
		if client.didAsk("merge ref") {
			t.Errorf("it kept working on a %s merge request: %v", state, client.asked)
		}
	}
}

// A merge request that cannot be fetched names the module and the iid: a bare
// 404 from GitLab says nothing about what was being looked for.
func TestAMergeRequestThatCannotBeFetchedNamesWhatWasAskedFor(t *testing.T) {
	client := aGitlab()
	client.mrFailure = &gitlab.Failure{Message: "404 Not Found"}

	_, err := NewMrResolver(watched, client, nil).Resolve("pathauto", 99, "")
	if err == nil {
		t.Fatal("a missing merge request resolved")
	}
	for _, expected := range []string{"!99", "pathauto", "404"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the refusal does not mention %q: %v", expected, err)
		}
	}
}

// A project that cannot be read goes through the shared failure wording, which
// is where a near-miss on a derived name gets offered the watched name it
// nearly matched.
func TestAProjectThatCannotBeReadGoesThroughTheSharedWording(t *testing.T) {
	client := aGitlab()
	client.projectFailure = &gitlab.Failure{Message: "404 Not Found"}

	_, err := NewMrResolver(watched, client, nil).Resolve("pathautoo", 12, "")
	if err == nil {
		t.Fatal("an unreachable project resolved")
	}
	if !strings.Contains(err.Error(), "pathauto") {
		t.Errorf("the near-miss was not offered: %v", err)
	}
	// And it stopped there.
	if client.didAsk("merge request") {
		t.Errorf("it asked about a merge request in a project it could not read: %v", client.asked)
	}
}

// Checking a branch on a core it never claimed fails at composer resolution
// and reads as though the contribution is broken.
func TestACoreTheTargetBranchDoesNotDeclareIsRefused(t *testing.T) {
	client := aGitlab()
	client.infoYAML = "name: Pathauto\ncore_version_requirement: ^10.2 || ^11\n"

	// Tracked by the registry, so the local check passes and the branch is
	// the only thing left to refuse it.
	tracksTwelve := map[string]cockpit.Module{
		"pathauto": {
			Name: "pathauto", Project: "project/pathauto",
			CoreVersions: []string{"11", "10", "12"}, Watched: true,
		},
	}

	_, err := NewMrResolver(tracksTwelve, client, []string{"10", "11", "12"}).
		Resolve("pathauto", 12, "12")
	if err == nil {
		t.Fatal("it checked a branch against a core it does not declare")
	}
	if !strings.Contains(err.Error(), "^10.2 || ^11") {
		t.Errorf("the refusal does not quote the constraint: %v", err)
	}
	if !strings.Contains(err.Error(), "say nothing about the module") {
		t.Errorf("the refusal does not say why it would be misleading: %v", err)
	}
	// It names cores that are both declared and built here — answering one
	// refusal with another would be no help.
	if !strings.Contains(err.Error(), "--version=10 or --version=11") {
		t.Errorf("the refusal does not name a core that would work: %v", err)
	}
	// Read from the target branch, which is where the contribution lands.
	if !client.didAsk("file pathauto.info.yml@2.0.x") {
		t.Errorf("it read the wrong branch: %v", client.asked)
	}
}

// A declared core is accepted, which is the ordinary case.
func TestADeclaredCoreResolves(t *testing.T) {
	client := aGitlab()
	client.infoYAML = "core_version_requirement: ^10.2 || ^11 || ^12\n"

	context, err := NewMrResolver(watched, client, []string{"10", "11", "12"}).
		Resolve("pathauto", 12, "11")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if context.CoreMajor != "11" {
		t.Errorf("core %q", context.CoreMajor)
	}
}

// Silence whenever the branch cannot be read: refusing on not-knowing would
// block work over a file that merely failed to fetch.
func TestABranchThatCannotBeReadDoesNotBlockAnything(t *testing.T) {
	for name, info := range map[string]string{
		"no info.yml at that path":   "",
		"no constraint in it":        "name: Pathauto\ntype: module\n",
		"a constraint nobody parses": "core_version_requirement: sometime after lunch\n",
	} {
		client := aGitlab()
		client.infoYAML = info

		if _, err := NewMrResolver(watched, client, []string{"10", "11"}).
			Resolve("pathauto", 12, "11"); err != nil {
			t.Errorf("%s blocked a check: %v", name, err)
		}
	}
}

// When nothing is built here, the suggestion falls back to what the registry
// tracks — and when the branch declares nothing this machine can run, the
// remedy is to build one rather than to pass a flag that would refuse again.
func TestTheSuggestionOnlyNamesCoresThisMachineCouldRun(t *testing.T) {
	client := aGitlab()
	client.infoYAML = "core_version_requirement: ^12\n"

	_, err := NewMrResolver(watched, client, []string{"10", "11"}).Resolve("pathauto", 12, "11")
	if err == nil {
		t.Fatal("it checked a branch against a core it does not declare")
	}
	if strings.Contains(err.Error(), "--version=12") {
		t.Errorf("it suggested a core with no base artifacts: %v", err)
	}
	if !strings.Contains(err.Error(), "base-artifacts:build") {
		t.Errorf("the refusal does not say how to get a usable core: %v", err)
	}
}

// The strict lookup, for the commands that narrow into the watchlist. Its
// refusal lists what is registered, in a stable order.
func TestTheStrictLookupListsWhatIsRegistered(t *testing.T) {
	registry := map[string]cockpit.Module{
		"token":    {Name: "token"},
		"pathauto": {Name: "pathauto"},
	}

	if module, err := RequireModule(registry, "pathauto"); err != nil || module.Name != "pathauto" {
		t.Errorf("got %+v (%v)", module, err)
	}

	_, err := RequireModule(registry, "webform")
	if err == nil {
		t.Fatal("an unregistered module resolved")
	}
	if !strings.Contains(err.Error(), "pathauto, token") {
		t.Errorf("the list is missing or unordered: %v", err)
	}

	_, err = RequireModule(nil, "webform")
	if err == nil || !strings.Contains(err.Error(), "(none)") {
		t.Errorf("an empty registry reads as %v", err)
	}
}

// A patch context carries what the adapter needs to apply the patch, and what
// the results cache needs to key the verdict.
func TestAPatchContextCarriesTheApplicationAndTheRevision(t *testing.T) {
	context := PatchContext{
		Module:     cockpit.Module{Name: "pathauto"},
		CoreMajor:  "11",
		Issue:      drupal.Issue{Nid: 3601234, Title: "Fix the thing"},
		Patch:      drupal.IssueFile{Name: "fix-3601234-12.patch", URL: "https://www.drupal.org/files/x.patch"},
		LocalPath:  "/cache/patches/fix-3601234-12.patch",
		BaseBranch: "2.0.x",
	}

	application := context.Application()
	if application.IssueNid != 3601234 || application.Name != "fix-3601234-12.patch" {
		t.Errorf("got %+v", application)
	}
	if application.LocalPath != "/cache/patches/fix-3601234-12.patch" {
		t.Errorf("local path %q", application.LocalPath)
	}
	// The branch the issue is filed against, which is what stops a 2.0.x patch
	// being applied to the default branch and read as needing a re-roll.
	if application.BaseBranch != "2.0.x" {
		t.Errorf("base branch %q", application.BaseBranch)
	}

	// Keyed on the source URL and not the bytes: the dashboard has to judge
	// staleness from the attachment list without downloading anything.
	if context.Revision() != patches.Revision(context.Patch.URL) {
		t.Errorf("revision %q", context.Revision())
	}
	if context.Revision() == "" {
		t.Error("a patch with a URL produced no revision")
	}
}

// A patch nobody could place carries no base branch, which the adapter reads
// as "work it out from the working copy".
func TestAPatchWithNoKnownBaseBranchSaysSoRatherThanGuessing(t *testing.T) {
	context := PatchContext{
		Issue: drupal.Issue{Nid: 1}, Patch: drupal.IssueFile{Name: "x.patch"},
	}

	if context.Application().BaseBranch != "" {
		t.Errorf("it invented a base branch: %q", context.Application().BaseBranch)
	}
}

// A project that cannot be read stops the resolution there, and goes through
// the shared wording — which is where a near-miss gets offered the watched
// name it nearly matched.
func TestAnUnreachableProjectStopsTheResolution(t *testing.T) {
	client := aGitlab()
	client.projectFailure = &gitlab.Failure{Message: "404 Not Found"}

	_, err := NewMrResolver(watched, client, nil).Resolve("pathauto", 12, "")
	if err == nil {
		t.Fatal("an unreachable project resolved")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("GitLab's own words were dropped: %v", err)
	}
	if client.didAsk("merge request") {
		t.Errorf("it asked about a merge request in a project it could not read: %v", client.asked)
	}
}

// With no cockpit to ask what is built, the suggestion falls back to what the
// registry entry tracks — naming a core from nowhere would be no help either.
func TestWithNothingBuiltTheSuggestionComesFromTheRegistry(t *testing.T) {
	client := aGitlab()
	client.infoYAML = "core_version_requirement: ^10\n"

	_, err := NewMrResolver(watched, client, nil).Resolve("pathauto", 12, "11")
	if err == nil {
		t.Fatal("it checked a branch against a core it does not declare")
	}
	if !strings.Contains(err.Error(), "--version=10") {
		t.Errorf("the registry's own cores were not offered: %v", err)
	}
}

// The cores worth suggesting are the ones this machine could actually run.
// Naming a core the branch declares but nothing here has built would answer
// one refusal with another.
func TestTheSuggestionPrefersWhatIsBuiltOverWhatIsTracked(t *testing.T) {
	client := aGitlab()
	// The branch declares 10 and 12; only 10 and 11 are built here.
	client.infoYAML = "core_version_requirement: ^10 || ^12\n"

	tracksTwelve := map[string]cockpit.Module{
		"pathauto": {
			Name: "pathauto", Project: "project/pathauto",
			CoreVersions: []string{"11", "10", "12"}, Watched: true,
		},
	}

	_, err := NewMrResolver(tracksTwelve, client, []string{"10", "11"}).Resolve("pathauto", 12, "11")
	if err == nil {
		t.Fatal("it checked a branch against a core it does not declare")
	}
	if !strings.Contains(err.Error(), "--version=10") {
		t.Errorf("the one usable core was not offered: %v", err)
	}
	if strings.Contains(err.Error(), "--version=12") {
		t.Errorf("it offered a core with no base artifacts here: %v", err)
	}
}

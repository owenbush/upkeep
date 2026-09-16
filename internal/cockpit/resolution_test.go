package cockpit

import (
	"slices"
	"strings"
	"testing"
)

func watchlist() map[string]Module {
	return map[string]Module{
		"pathauto": {
			Name: "pathauto", Project: "someone/fork-of-pathauto",
			CoreVersions: []string{"10", "11"}, Watched: true,
		},
		"token": {Name: "token", Project: "project/token", CoreVersions: []string{"11"}, Watched: true},
	}
}

// A maintainer's core_versions is a deliberate statement about what they
// support, and outranks anything inferred.
func TestARegistryEntryOutranksWhatIsOnDisk(t *testing.T) {
	module, err := ResolveModule(watchlist(), "pathauto", []string{"10", "11", "12", "13"})
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}

	if !slices.Equal(module.CoreVersions, []string{"10", "11"}) {
		t.Errorf("cores %v, want the registry's", module.CoreVersions)
	}
	if module.Project != "someone/fork-of-pathauto" {
		t.Errorf("project %q, want the registry's", module.Project)
	}
	if !module.Watched {
		t.Error("a registered module did not read as watched")
	}
}

// The registry is a watchlist now: an unregistered module is workable.
func TestAnUnregisteredModuleIsDerivedFromTheConventionAndTheDisk(t *testing.T) {
	module, err := ResolveModule(watchlist(), "webform", []string{"10", "11", "12"})
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}

	if module.Project != "project/webform" {
		t.Errorf("project %q", module.Project)
	}
	if module.Watched {
		t.Error("a derived module read as watched — which changes what a refusal may say")
	}
}

// core_versions[0] is what a run picks when --version is absent, and the
// artifact layout sorts ascending. Passed through unchanged, asking about an
// unregistered module would answer for the oldest core built on this machine.
func TestADerivedModulesCoresAreNewestFirst(t *testing.T) {
	module, err := ResolveModule(watchlist(), "webform", []string{"10", "11", "12"})
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}

	if !slices.Equal(module.CoreVersions, []string{"12", "11", "10"}) {
		t.Errorf("cores %v, want newest first", module.CoreVersions)
	}
}

// Deriving must not mutate what it was handed.
func TestDerivingDoesNotReverseTheCallersSlice(t *testing.T) {
	onDisk := []string{"10", "11", "12"}
	if _, err := ResolveModule(watchlist(), "webform", onDisk); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}

	if !slices.Equal(onDisk, []string{"10", "11", "12"}) {
		t.Errorf("the caller's slice came back as %v", onDisk)
	}
}

// "Module is not registered" was protecting configuration this can now derive,
// but it was also the only thing catching a typo — and turning one into a clone
// of project/pathuato would make the tool worse rather than freer.
func TestANameThatCannotBeAModuleIsRefusedOutright(t *testing.T) {
	for _, name := range []string{"PathAuto", "path-auto", "2fa", "../escape", "", "path auto"} {
		if _, err := ResolveModule(watchlist(), name, []string{"11"}); err == nil {
			t.Errorf("%q was accepted as a module name", name)
		}
	}
}

// Deriving needs something to derive from, and saying so beats picking a core
// at random.
func TestNoBaseArtifactsIsARefusalThatNamesTheBuild(t *testing.T) {
	_, err := ResolveModule(watchlist(), "webform", nil)
	if err == nil {
		t.Fatal("derived cores from an empty disk")
	}
	if !strings.Contains(err.Error(), "base-artifacts:build") {
		t.Errorf("error %q does not name the recovery", err)
	}
}

// A registered module resolves even with nothing built, because its cores come
// from the file rather than the disk.
func TestARegisteredModuleNeedsNoBaseArtifactsToResolve(t *testing.T) {
	if _, err := ResolveModule(watchlist(), "pathauto", nil); err != nil {
		t.Errorf("unexpected refusal: %v", err)
	}
}

// Until drupal.org says there is nothing there, a name upkeep has never heard
// of is an ordinary thing to ask about. The suggestion belongs at the failure.
func TestADerivedNameThatDoesNotExistIsToldWhatItNearlyMatched(t *testing.T) {
	registered := watchlist()
	typo, err := ResolveModule(registered, "pathuato", []string{"11"})
	if err != nil {
		t.Fatalf("resolution refused a plausible name: %v", err)
	}

	message := ProjectFailure(registered, typo, "Resource not found (HTTP 404).")
	if !strings.Contains(message, `Did you mean "pathauto"?`) {
		t.Errorf("message %q carries no suggestion", message)
	}
	if !strings.Contains(message, "Resource not found") {
		t.Errorf("message %q drops what the API said", message)
	}
}

// A watched module's name is one the maintainer wrote down deliberately, so
// the problem is the API's answer, not the spelling.
func TestAWatchedModuleGetsNoSuggestion(t *testing.T) {
	registered := watchlist()
	module, _ := registered["pathauto"]

	message := ProjectFailure(registered, module, "Endpoint closed to API access (HTTP 403).")
	if strings.Contains(message, "Did you mean") {
		t.Errorf("message %q second-guessed a name the maintainer wrote", message)
	}
}

// Transpositions and dropped letters are the mistakes that happen, and a
// prefix test misses them entirely.
func TestDidYouMeanCatchesTyposAndNotUnrelatedNames(t *testing.T) {
	registered := watchlist()

	for _, typo := range []string{"pathuato", "pathaut", "pathautoo", "toke", "tokenn"} {
		if _, found := DidYouMean(registered, typo); !found {
			t.Errorf("%q matched nothing", typo)
		}
	}

	for _, unrelated := range []string{"webform", "commerce_shipping", "views_bulk_operations"} {
		if meant, found := DidYouMean(registered, unrelated); found {
			t.Errorf("%q was matched to %q", unrelated, meant)
		}
	}
}

// The threshold scales with length, and the floor is what keeps short names
// matchable at all: "token" is five characters, so a proportional threshold
// alone would be 1 and a two-character slip would suggest nothing.
func TestTheThresholdHasAFloorSoShortNamesStillMatch(t *testing.T) {
	registered := map[string]Module{"token": {Name: "token"}}

	// Two transpositions away, and five characters long: 5/4 is 1, so only the
	// floor of 2 catches it.
	if _, found := DidYouMean(registered, "tkoen"); !found {
		t.Error("a two-character slip in a five-character name matched nothing")
	}

	// And the floor is a floor, not a free pass: three away is still too far.
	if meant, found := DidYouMean(registered, "abcde"); found {
		t.Errorf("suggested %q for an unrelated five-character name", meant)
	}
}

// The threshold scales with length, so short names are not matched to
// everything.
func TestAnEmptyWatchlistSuggestsNothing(t *testing.T) {
	if meant, found := DidYouMean(map[string]Module{}, "pathauto"); found {
		t.Errorf("suggested %q from an empty watchlist", meant)
	}
}

// Two candidates at the same distance must resolve the same way every run,
// rather than however the map happened to iterate.
func TestTheSuggestionIsStableAcrossRuns(t *testing.T) {
	// All the same distance from the name, and all within the threshold, so
	// the only thing deciding between them is the order they are considered.
	// Each is one insertion from the name, so every candidate is equally
	// close and the only thing deciding between them is the order they are
	// considered.
	tied := map[string]Module{
		"paths_aab": {Name: "paths_aab"},
		"paths_aac": {Name: "paths_aac"},
		"paths_aad": {Name: "paths_aad"},
		"paths_aae": {Name: "paths_aae"},
	}

	first, found := DidYouMean(tied, "paths_aa")
	if !found {
		t.Fatal("nothing matched, so this proves nothing about tie-breaking")
	}
	for range 50 {
		if again, _ := DidYouMean(tied, "paths_aa"); again != first {
			t.Fatalf("suggested %q then %q", first, again)
		}
	}
}

func TestIsRegisteredAnswersProvenance(t *testing.T) {
	registered := watchlist()

	if !IsRegistered(registered, "pathauto") {
		t.Error("a registered module read as derived")
	}
	if IsRegistered(registered, "webform") {
		t.Error("an unregistered module read as registered")
	}
}

func TestProjectForIsTheConvention(t *testing.T) {
	if got := ProjectFor("field_visibility_conditions"); got != "project/field_visibility_conditions" {
		t.Errorf("got %q", got)
	}
}

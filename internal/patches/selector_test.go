package patches

import (
	"slices"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/drupal"
)

func patch(name string, timestamp int64, size int64) drupal.IssueFile {
	return drupal.IssueFile{
		Name: name, URL: "https://www.drupal.org/files/issues/" + name,
		Timestamp: timestamp, Size: size,
	}
}

func issueWith(files ...drupal.IssueFile) drupal.Issue {
	return drupal.Issue{Nid: 3597808, Title: "Drupal 12 compatibility", Files: files}
}

// Picking the wrong one wastes a full environment build and reports a verdict
// on code nobody submitted.
func TestOnePatchSettlesItself(t *testing.T) {
	selection := Select(issueWith(patch("3597808-9-d11.patch", 100, 4200)), "", false)

	if selection.Chosen == nil {
		t.Fatalf("not settled: %+v", selection)
	}
	if selection.Chosen.Name != "3597808-9-d11.patch" {
		t.Errorf("chose %q", selection.Chosen.Name)
	}
	if selection.Ambiguous {
		t.Error("one candidate read as ambiguous")
	}
}

// Several and no instruction is a question for the operator, not an error and
// not an answer — only the command knows whether there is anyone there to ask.
func TestSeveralPatchesAreAmbiguousRatherThanGuessedAt(t *testing.T) {
	selection := Select(issueWith(
		patch("3597808-9-d11.patch", 100, 4200),
		patch("3597808-14-d11.patch", 200, 4300),
	), "", false)

	if !selection.Ambiguous {
		t.Fatalf("settled on %+v without being asked", selection.Chosen)
	}
	if selection.Chosen != nil {
		t.Error("an ambiguous selection carries a choice")
	}
	if len(selection.Candidates) != 2 {
		t.Errorf("%d candidates offered", len(selection.Candidates))
	}
}

func TestLatestTakesTheNewestWithoutAsking(t *testing.T) {
	selection := Select(issueWith(
		patch("3597808-9-d11.patch", 100, 4200),
		patch("3597808-14-d11.patch", 200, 4300),
	), "", true)

	if selection.Chosen == nil {
		t.Fatalf("not settled: %+v", selection)
	}
	if selection.Chosen.Name != "3597808-14-d11.patch" {
		t.Errorf("chose %q, want the newest", selection.Chosen.Name)
	}
}

// An unmatched name is a problem, never a silent fallback to something else.
func TestAnUnmatchedNameIsARefusalThatListsWhatIsThere(t *testing.T) {
	selection := Select(issueWith(
		patch("3597808-9-d11.patch", 100, 4200),
		patch("3597808-14-d11.patch", 200, 4300),
	), "3597808-99-d11.patch", false)

	if selection.Chosen != nil {
		t.Fatalf("fell back to %q", selection.Chosen.Name)
	}
	if selection.Problem == "" {
		t.Fatal("no problem reported")
	}
	for _, want := range []string{"3597808-9-d11.patch", "3597808-14-d11.patch"} {
		if !strings.Contains(selection.Problem, want) {
			t.Errorf("problem %q does not list %q", selection.Problem, want)
		}
	}
}

func TestANameMatchesRegardlessOfCase(t *testing.T) {
	selection := Select(issueWith(patch("3597808-9-D11.patch", 100, 4200)), "3597808-9-d11.PATCH", false)

	if selection.Chosen == nil {
		t.Fatalf("not settled: %+v", selection)
	}
}

// drupal.org keeps the filenames identical but the URLs distinct, so a name
// shared by several re-uploads settles on the most recent of them.
func TestASharedNameSettlesOnTheNewestUpload(t *testing.T) {
	older := patch("rector.patch", 100, 4200)
	older.URL = "https://www.drupal.org/files/issues/rector.patch"
	newer := patch("rector.patch", 200, 4300)
	newer.URL = "https://www.drupal.org/files/issues/rector_0.patch"

	selection := Select(issueWith(older, newer), "rector.patch", false)
	if selection.Chosen == nil {
		t.Fatalf("not settled: %+v", selection)
	}
	if selection.Chosen.URL != newer.URL {
		t.Errorf("chose %q, want the newest upload", selection.Chosen.URL)
	}
}

// An issue routinely carries an interdiff and a screenshot alongside the
// patches.
func TestOnlyPatchesAreCandidates(t *testing.T) {
	candidates := Candidates(issueWith(
		patch("3597808-9-d11.patch", 100, 4200),
		patch("interdiff-4-9.txt", 150, 800),
		patch("screenshot.png", 160, 90000),
		patch("3597808-14.diff", 200, 4300),
	))

	names := []string{}
	for _, candidate := range candidates {
		names = append(names, candidate.Name)
	}
	if !slices.Equal(names, []string{"3597808-14.diff", "3597808-9-d11.patch"}) {
		t.Errorf("got %v", names)
	}
}

func TestNoPatchesAtAllIsAProblemThatCountsTheAttachments(t *testing.T) {
	selection := Select(issueWith(
		patch("screenshot.png", 100, 90000),
		patch("notes.txt", 110, 200),
	), "", false)

	if selection.Problem == "" {
		t.Fatal("no problem reported")
	}
	if !strings.Contains(selection.Problem, "2 attachment(s)") {
		t.Errorf("problem %q does not count what is there", selection.Problem)
	}
}

// The comment number is the ordering when the filename carries one, because
// that is the order `upkeep patches` prints.
func TestCommentNumbersOrderAheadOfTimestamps(t *testing.T) {
	// The later comment was uploaded with an *earlier* timestamp, which
	// happens when somebody back-fills a re-roll.
	candidates := Candidates(issueWith(
		patch("3597808-9-d11.patch", 900, 4200),
		patch("3597808-14-d11.patch", 100, 4300),
	))

	if candidates[0].Name != "3597808-14-d11.patch" {
		t.Errorf("first is %q, want the higher comment number", candidates[0].Name)
	}
}

// The Project Update Bot re-uploads under the same filename on every run, so a
// picker offering four identical lines cannot be answered.
func TestLabelsAreDistinctEvenWhenEverythingElseCollides(t *testing.T) {
	identical := []drupal.IssueFile{
		patch("rector.patch", 0, 0),
		patch("rector.patch", 0, 0),
		patch("rector.patch", 0, 0),
	}

	labels := Labels(identical)
	if len(labels) != 3 {
		t.Fatalf("got %v", labels)
	}
	seen := map[string]bool{}
	for _, label := range labels {
		if seen[label] {
			t.Fatalf("label %q appears twice in %v", label, labels)
		}
		seen[label] = true
	}
}

// Usually the upload date separates them, and the ordinal is only the
// fallback.
func TestLabelsUseTheDateBeforeReachingForAnOrdinal(t *testing.T) {
	labels := Labels([]drupal.IssueFile{
		patch("rector.patch", 1780000000, 4200),
		patch("rector.patch", 1750000000, 4200),
	})

	for _, label := range labels {
		if strings.Contains(label, "(#") {
			t.Errorf("label %q reached for an ordinal where the date separates them: %v", label, labels)
		}
	}
	if labels[0] == labels[1] {
		t.Errorf("labels %v are identical", labels)
	}
}

func TestADescriptionCarriesWhatDistinguishesTheUpload(t *testing.T) {
	got := Describe(patch("3597808-9-d11.patch", 1780000000, 4300))

	for _, want := range []string{"3597808-9-d11.patch", "comment 9", "4.2 KB", "2026-"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not carry %q", got, want)
		}
	}
}

// A file with nothing to say about itself is still nameable.
func TestADescriptionFallsBackToTheNameAlone(t *testing.T) {
	if got := Describe(patch("rector.patch", 0, 0)); got != "rector.patch" {
		t.Errorf("got %q", got)
	}
}

func TestSizesAreRenderedAtHumanScale(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		want  string
	}{
		{0, ""},
		{512, "512 B"},
		{4300, "4.2 KB"},
		{2 * 1024 * 1024, "2.0 MB"},
	} {
		got := Describe(patch("x.patch", 0, tc.bytes))
		if tc.want == "" {
			if got != "x.patch" {
				t.Errorf("%d bytes rendered as %q", tc.bytes, got)
			}

			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%d bytes rendered as %q, want %q in it", tc.bytes, got, tc.want)
		}
	}
}

// drupal.org mints a distinct URL for every upload, which is what makes the
// URL a faithful stand-in for "which upload was this".
func TestTheRevisionIsTheUrlAndHasTheShapeAShaHas(t *testing.T) {
	first := Revision("https://www.drupal.org/files/issues/rector.patch")
	second := Revision("https://www.drupal.org/files/issues/rector_0.patch")

	if first == second {
		t.Error("two uploads of the same filename hashed the same")
	}
	if len(first) != 40 {
		t.Errorf("revision %q is %d characters, want a SHA's 40", first, len(first))
	}
	// And the results cache requires 7-64 lower-case hex.
	for _, c := range first {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("revision %q is not lower-case hex", first)
		}
	}
	if Revision("https://x/a.patch") != Revision("https://x/a.patch") {
		t.Error("the same URL hashed differently twice")
	}
}

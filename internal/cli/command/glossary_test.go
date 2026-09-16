package command

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/workflow"
)

// phpDefinition is one entry as the PHP publishes it.
type phpDefinition struct {
	Term    string `json:"term"`
	Meaning string `json:"meaning"`
	Where   string `json:"where"`
}

// The glossary is held to the PHP's, entry for entry and in order.
//
// The corpus is generated from Glossary::terms() rather than transcribed,
// because thirty-eight paragraphs retyped by hand is thirty-eight chances to
// change a sentence nobody would notice had changed — and these are the
// sentences a confused maintainer reads.
func TestTheGlossaryMatchesThePhpOneExactly(t *testing.T) {
	contents, err := os.ReadFile("../../../testdata/glossary.json")
	if err != nil {
		t.Fatalf("read the corpus: %v", err)
	}

	var expected []phpDefinition
	if err := json.Unmarshal(contents, &expected); err != nil {
		t.Fatalf("corpus: %v", err)
	}

	if len(Glossary) != len(expected) {
		t.Fatalf("%d terms here, %d in the PHP", len(Glossary), len(expected))
	}

	for i, want := range expected {
		got := Glossary[i]
		if got.Term != want.Term {
			t.Errorf("term %d is %q, want %q", i, got.Term, want.Term)

			continue
		}
		if got.Meaning != want.Meaning {
			t.Errorf("%q means:\n  %q\nthe PHP says:\n  %q", got.Term, got.Meaning, want.Meaning)
		}
		if got.Where != want.Where {
			t.Errorf("%q appears in %q, the PHP says %q", got.Term, got.Where, want.Where)
		}
	}
}

// Every term says where it appears, because the same word means different
// things in different places — "review" is both a gate verdict and an issue
// status.
func TestEveryTermSaysWhereItIsPrinted(t *testing.T) {
	seen := map[string]bool{}
	for _, definition := range Glossary {
		if definition.Where == "" {
			t.Errorf("%q does not say where it appears", definition.Term)
		}
		if definition.Meaning == "" {
			t.Errorf("%q means nothing", definition.Term)
		}
		if seen[definition.Term] {
			t.Errorf("%q is defined twice", definition.Term)
		}
		seen[definition.Term] = true
	}
}

// A half-remembered word finds its definition: the search matches the term and
// the meaning, and folds case.
func TestTheSearchMatchesTheTermAndTheMeaning(t *testing.T) {
	byTerm := SearchGlossary("RTBC")
	if len(byTerm) != 1 || byTerm[0].Term != "RTBC" {
		t.Errorf("got %+v", byTerm)
	}

	// Case-folded, because nobody types a gate status in capitals to look it
	// up.
	if lowered := SearchGlossary("rtbc"); len(lowered) != len(byTerm) {
		t.Errorf("case changed the answer: %d vs %d", len(lowered), len(byTerm))
	}

	// And by meaning, which is the half that makes it useful: somebody who
	// remembers "the bot" but not what the row said still finds it.
	byMeaning := SearchGlossary("Project Update Bot")
	if len(byMeaning) < 2 {
		t.Errorf("searching a meaning found %d terms", len(byMeaning))
	}

	// Surrounding space is a typing accident, not a query.
	if padded := SearchGlossary("  rtbc  "); len(padded) != 1 {
		t.Errorf("a padded query found %d", len(padded))
	}

	// Nothing is everything.
	if all := SearchGlossary(""); len(all) != len(Glossary) {
		t.Errorf("an empty query found %d of %d", len(all), len(Glossary))
	}
}

// Not knowing a word is not a failure: the command did what was asked and
// reported the answer, which happens to be "no such term".
func TestAnUnknownTermIsAnAnswerRatherThanAFailure(t *testing.T) {
	code, stdout, stderr := invoke(t, "explain", "zzzz")

	if code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stdout, "zzzz") {
		t.Errorf("it did not say what it could not find: %q", stdout)
	}
	// And it says how to see what there is.
	if !strings.Contains(stdout, "lists every term") {
		t.Errorf("no way out was offered: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("an answer was reported as a problem: %q", stderr)
	}
}

// Bare, it lists everything and says how to narrow it.
func TestExplainBareListsEveryTerm(t *testing.T) {
	code, stdout, _ := invoke(t, "explain")
	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}

	for _, definition := range Glossary {
		if !strings.Contains(stdout, definition.Term) {
			t.Errorf("%q was not listed", definition.Term)
		}
		// Where it appears, because the same word means different things in
		// different places and a definition alone does not say which.
		if !strings.Contains(stdout, "("+definition.Where+")") {
			t.Errorf("%q does not say where it is printed", definition.Term)
		}
	}
	if !strings.Contains(stdout, "Narrow it with") {
		t.Errorf("it did not say how to narrow: %q", stdout[:200])
	}

	// Wrapped, not run out to whatever width the sentence happens to be: a
	// paragraph on one line is what a terminal reflows into a mess.
	for _, line := range strings.Split(stdout, "\n") {
		if len(line) > wrapWidth+len("    ") {
			t.Errorf("a line ran past the wrap width (%d): %q", len(line), line)
		}
	}
}

// Narrowed, it does not repeat the listing preamble — the preamble is there to
// explain a wall of text, and one definition is not one.
func TestANarrowedExplainDoesNotPrintTheListingPreamble(t *testing.T) {
	_, stdout, _ := invoke(t, "explain", "RTBC")

	if strings.Contains(stdout, "Narrow it with") {
		t.Errorf("a narrowed search was told to narrow: %q", stdout)
	}
	if !strings.Contains(stdout, "Reviewed & tested") {
		t.Errorf("the definition is missing: %q", stdout)
	}
}

// Definitions are wrapped at word boundaries, and a word longer than the width
// is left whole: the long words here are command names and paths, and half of
// one is worse than a line that overhangs.
func TestDefinitionsWrapAtWordsAndNeverCutOne(t *testing.T) {
	lines := wrapText("the quick brown fox jumps over the lazy dog", 12)
	for _, line := range lines {
		if len(line) > 12 {
			t.Errorf("line %q is wider than asked", line)
		}
	}
	if strings.Join(lines, " ") != "the quick brown fox jumps over the lazy dog" {
		t.Errorf("wrapping lost or changed words: %v", lines)
	}

	long := wrapText("run upkeep base-artifacts:build --version=11 now", 10)
	if !containsLine(long, "base-artifacts:build") {
		t.Errorf("a long word was cut: %v", long)
	}

	// Nothing wraps to one empty line rather than to nothing at all.
	if got := wrapText("", 10); len(got) != 1 || got[0] != "" {
		t.Errorf("got %q", got)
	}
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}

	return false
}

// The glossary needs no cockpit, no token and no network — it is a glossary,
// and it has to work from inside whatever confusion sent somebody to it.
func TestExplainNeedsNothingSetUp(t *testing.T) {
	t.Setenv("UPKEEP_COCKPIT", "/nonexistent")

	if code, _, _ := invoke(t, "explain", "stale"); code != workflow.OK {
		t.Errorf("exit %d with no cockpit", code)
	}
}

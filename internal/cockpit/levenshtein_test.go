package cockpit

import (
	"encoding/json"
	"os"
	"testing"
)

// The threshold that decides "did you mean" is a distance, so the distance has
// to be PHP's: an implementation that disagrees about one suggests a different
// module name than the tool this replaces.
//
// corpus.json holds PHP's own levenshtein() answers, generated once from the
// PHP implementation and committed.
func TestLevenshteinMatchesPhp(t *testing.T) {
	raw, err := os.ReadFile("../../corpus.json")
	if err != nil {
		t.Fatalf("corpus: %v (a committed fixture — see git history for the PHP that produced it)", err)
	}

	var corpus struct {
		Distances []struct {
			A        string `json:"a"`
			B        string `json:"b"`
			Distance int    `json:"distance"`
		} `json:"distances"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatalf("corpus: %v", err)
	}
	if len(corpus.Distances) == 0 {
		t.Fatal("the corpus holds no edit distances; regenerate it")
	}

	diverged := 0
	for _, tc := range corpus.Distances {
		if got := levenshtein(tc.A, tc.B); got != tc.Distance {
			if diverged < 10 {
				t.Errorf("levenshtein(%q, %q) = %d, PHP says %d", tc.A, tc.B, got, tc.Distance)
			}
			diverged++
		}
	}
	if diverged > 0 {
		t.Fatalf("%d of %d cases diverged", diverged, len(corpus.Distances))
	}
	t.Logf("%d edit distances, no divergence", len(corpus.Distances))
}

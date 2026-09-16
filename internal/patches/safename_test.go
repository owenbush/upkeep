package patches

import (
	"encoding/json"
	"os"
	"testing"
)

// The name arrives from a remote API and becomes a path segment, so this is
// the one place in the patch surface where a divergence is a security
// difference rather than a cosmetic one — and both implementations cache the
// downloaded patch under the name this produces, so they must agree byte for
// byte or the same patch lands at two paths.
//
// The answers are committed; git history holds the PHP that produced them.
func TestSafeNameMatchesPhp(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/safenames.json")
	if err != nil {
		t.Fatalf("answers: %v (a committed fixture — see git history for the PHP that produced it)", err)
	}

	var cases []struct {
		Name string `json:"name"`
		Safe string `json:"safe"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("answers: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no answers recorded")
	}

	for _, tc := range cases {
		if got := SafeName(tc.Name); got != tc.Safe {
			t.Errorf("SafeName(%q) = %q, PHP gives %q", tc.Name, got, tc.Safe)
		}
	}
}

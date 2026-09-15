package maintenance

import (
	"testing"
	"time"
)

// A typo like --older-than=30 must never silently mean "30 seconds", so a bare
// number is refused rather than defaulted.
func TestABareNumberIsNotADuration(t *testing.T) {
	for _, input := range []string{"30", "", "d", "30D", "30 d", "-30d", "30dd", "1.5d", "30y", " 30d"} {
		if got, err := ParseDuration(input); err == nil {
			t.Errorf("%q was accepted as %v", input, got)
		}
	}
}

func TestEveryUnitIsUnderstood(t *testing.T) {
	for input, want := range map[string]time.Duration{
		"45s": 45 * time.Second,
		"90m": 90 * time.Minute,
		"12h": 12 * time.Hour,
		"30d": 30 * 24 * time.Hour,
		"2w":  14 * 24 * time.Hour,
		"0d":  0,
	} {
		got, err := ParseDuration(input)
		if err != nil {
			t.Errorf("%q: %v", input, err)

			continue
		}
		if got != want {
			t.Errorf("%q = %v, want %v", input, got, want)
		}
	}
}

// The refusal has to say what a duration looks like, because the person
// reading it has just typed something that is not one.
func TestTheRefusalNamesTheSyntax(t *testing.T) {
	_, err := ParseDuration("30")
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"30d", "12h", "w, d, h, m, s"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// Binary units, matching what du measures — a GiB reported as a GB would make
// upkeep's number disagree with the operator's.
func TestSizesAreRenderedInBinaryUnits(t *testing.T) {
	for bytes, want := range map[int64]string{
		0:                  "0 B",
		512:                "512 B",
		1023:               "1023 B",
		1024:               "1.0 KiB",
		1536:               "1.5 KiB",
		1024 * 1024:        "1.0 MiB",
		1024 * 1024 * 1024: "1.0 GiB",
		// The boundary a decimal implementation gets wrong: 1000 bytes is not
		// a kilobyte here.
		1000: "1000 B",
	} {
		if got := HumanBytes(bytes); got != want {
			t.Errorf("%d bytes rendered as %q, want %q", bytes, got, want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}

	return -1
}

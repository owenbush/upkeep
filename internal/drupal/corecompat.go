// Package drupal holds Drupal's own version semantics.
package drupal

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Applies reports whether a module branch declaring constraint supports the
// given Drupal core major version.
//
// The question is whether the constraint overlaps the whole of that major —
// [N.0.0, N+1.0.0) — not whether some particular version inside it satisfies
// the constraint. composer/semver, which the PHP implementation used, answers
// it by intersecting intervals, and the corpus in corpus.json holds its
// answers so this one can be held to them exactly.
func Applies(constraint string, coreMajor int) (bool, error) {
	ranges, err := intervalsFor(constraint)
	if err != nil {
		return false, err
	}

	major := interval{version{coreMajor, 0, 0}, version{coreMajor + 1, 0, 0}}
	for _, r := range ranges {
		if r.overlaps(major) {
			return true, nil
		}
	}

	return false, nil
}

// CoreCompatibility is which Drupal cores a module branch declares support
// for.
//
// The fact the dashboard was missing. A *branch* supports several cores at
// once — measured live: pathauto's single 8.x-1.x declares
// ^10.2 || ^11 || ^12 — so treating a core version as part of a row's identity
// multiplied every row by the size of a test matrix while saying nothing new.
// Core belongs to the evidence; this is what says which cores the evidence
// could meaningfully be gathered on.
type CoreCompatibility struct {
	// Cores are the major versions declared, ascending.
	Cores []string
}

// CompatibilityFrom reads a core_version_requirement constraint.
//
// It reports false when the constraint is absent or will not parse. That means
// "cannot tell", and every caller must fall back to the tracked set whole —
// the behaviour before any of this existed. An unreadable constraint is not
// evidence that a branch supports nothing.
//
// candidates are the major versions worth asking about.
func CompatibilityFrom(constraint string, candidates []string) (CoreCompatibility, bool) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return CoreCompatibility{}, false
	}

	cores := []string{}
	for _, core := range candidates {
		if !wholeNumber.MatchString(core) {
			continue
		}
		major, err := strconv.Atoi(core)
		if err != nil {
			continue
		}
		applies, err := Applies(constraint, major)
		if err != nil {
			// Unparseable is unparseable for every candidate, so this is the
			// whole constraint failing rather than one core.
			return CoreCompatibility{}, false
		}
		if applies {
			cores = append(cores, core)
		}
	}

	sort.Slice(cores, func(a, b int) bool {
		left, _ := strconv.Atoi(cores[a])
		right, _ := strconv.Atoi(cores[b])

		return left < right
	})

	return CoreCompatibility{Cores: cores}, true
}

var (
	wholeNumber            = regexp.MustCompile(`^\d+$`)
	coreVersionRequirement = regexp.MustCompile(`(?m)^core_version_requirement:[ \t]*(.+?)[ \t]*$`)
)

// ConstraintIn is the raw constraint an info.yml declares, unparsed.
//
// What the snapshot stores. A cache holds *data*, not a resolved answer: the
// tracked core list can change between the fetch and the read (a registry edit
// costs nothing and goes to no network), and a snapshot holding "10, 11"
// rather than "^10.2 || ^11 || ^12" would answer for a question nobody asked
// yet.
//
// Read as one line rather than through a YAML parser. A Drupal info.yml is
// YAML, but the only line wanted here is a scalar, and a constraint containing
// || is exactly the kind of value a strict parser rejects or mangles depending
// on quoting. Reading the one line is both narrower and more robust than
// handing the whole file to a parser that could refuse it over something
// unrelated further down.
func ConstraintIn(infoYAML string) string {
	match := coreVersionRequirement.FindStringSubmatch(infoYAML)
	if match == nil {
		return ""
	}

	return strings.Trim(match[1], "'\" \t")
}

// CompatibilityFromInfoYAML reads the constraint out of an info.yml and
// resolves it.
func CompatibilityFromInfoYAML(infoYAML string, candidates []string) (CoreCompatibility, bool) {
	constraint := ConstraintIn(infoYAML)
	if constraint == "" {
		return CoreCompatibility{}, false
	}

	return CompatibilityFrom(constraint, candidates)
}

// Declares reports whether a tracked core is one this branch declares.
func (c CoreCompatibility) Declares(core string) bool { return slices.Contains(c.Cores, core) }

// ApplicableTo is the cores worth testing on: what the registry tracks,
// narrowed to what the branch declares.
//
// Wrong in both directions before this existed. Checking pathauto's 8.x-1.x on
// a core it does not declare produces a failure that means nothing, and a
// registry tracking 10 and 11 silently hid that the branch also claims 12.
//
// An empty intersection yields the tracked set unchanged rather than nothing:
// a branch that appears to support none of the cores you track is far more
// likely to be a constraint this misread than a real state, and showing no
// rows would hide the module entirely.
func (c CoreCompatibility) ApplicableTo(tracked []string) []string {
	applicable := []string{}
	for _, core := range tracked {
		if c.Declares(core) {
			applicable = append(applicable, core)
		}
	}
	if len(applicable) == 0 {
		return tracked
	}

	return applicable
}

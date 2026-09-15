package drupal

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A half-open version range, [lo, hi), over Drupal core version numbers.
//
// composer/semver answers "does this constraint support core N?" by
// intersecting intervals, and nothing less will do. Probing sample versions
// inside the major looks equivalent and is not: a constraint bounded inside
// the major — ">=10.50 <10.51", "~10.4.0", "10.6.*" — is missed unless a probe
// happens to land in its window, and the miss always reads as "does not
// support this core". A module vanishing from the dashboard for a core it
// really supports is the worst failure this tool has, so the approximation was
// abandoned rather than tuned.
type interval struct {
	lo, hi version // hi is exclusive
}

type version struct{ major, minor, patch int }

var unbounded = version{major: 1 << 30}

func (v version) compare(o version) int {
	for _, pair := range [][2]int{{v.major, o.major}, {v.minor, o.minor}, {v.patch, o.patch}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (i interval) overlaps(o interval) bool {
	return i.lo.compare(o.hi) < 0 && o.lo.compare(i.hi) < 0
}

func (i interval) intersect(o interval) (interval, bool) {
	lo, hi := i.lo, i.hi
	if o.lo.compare(lo) > 0 {
		lo = o.lo
	}
	if o.hi.compare(hi) < 0 {
		hi = o.hi
	}
	if lo.compare(hi) >= 0 {
		return interval{}, false
	}
	return interval{lo, hi}, true
}

var termPattern = regexp.MustCompile(`^(\^|~|>=|<=|>|<|=|!=)?\s*v?([0-9]+|\*|[xX])(?:\.([0-9]+|\*|[xX]))?(?:\.([0-9]+|\*|[xX]))?(?:[-+].*)?$`)

// intervalsFor turns a constraint string into the ranges it admits.
//
// Only the operators Drupal's core_version_requirement actually uses are
// supported; anything else is an error rather than a guess, because a
// constraint read wrongly is a module shown against the wrong core.
func intervalsFor(constraint string) ([]interval, error) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return nil, fmt.Errorf("empty constraint")
	}

	var out []interval
	for _, group := range strings.Split(constraint, "||") {
		current := interval{lo: version{}, hi: unbounded}
		terms := strings.Fields(strings.ReplaceAll(group, ",", " "))
		if len(terms) == 0 {
			return nil, fmt.Errorf("empty alternative in %q", constraint)
		}

		ok := true
		for _, term := range terms {
			bounds, err := boundsFor(term)
			if err != nil {
				return nil, err
			}
			if current, ok = current.intersect(bounds); !ok {
				break // this alternative admits nothing
			}
		}
		if ok {
			out = append(out, current)
		}
	}

	return out, nil
}

func boundsFor(term string) (interval, error) {
	if term == "*" || term == "x" || term == "X" {
		return interval{lo: version{}, hi: unbounded}, nil
	}

	m := termPattern.FindStringSubmatch(term)
	if m == nil {
		return interval{}, fmt.Errorf("unsupported constraint term %q", term)
	}

	op := m[1]
	major, majorWild := number(m[2])
	minor, _, minorWild := optional(m[3])
	patch, patchGiven, patchWild := optional(m[4])

	if majorWild {
		return interval{lo: version{}, hi: unbounded}, nil
	}

	exact := version{major, minor, patch}

	// A wildcard is a range regardless of the operator in front of it.
	switch {
	case minorWild:
		return interval{version{major, 0, 0}, version{major + 1, 0, 0}}, nil
	case patchWild:
		return interval{version{major, minor, 0}, version{major, minor + 1, 0}}, nil
	}

	switch op {
	case "^":
		// Caret is open to the next major, whatever depth it was written at.
		return interval{exact, version{major + 1, 0, 0}}, nil
	case "~":
		// Tilde is open to the next *given* place: ~10.4.0 stops at 10.5,
		// ~10.4 stops at 11.
		if patchGiven {
			return interval{exact, version{major, minor + 1, 0}}, nil
		}
		return interval{exact, version{major + 1, 0, 0}}, nil
	case ">=":
		return interval{exact, unbounded}, nil
	case ">":
		return interval{version{major, minor, patch + 1}, unbounded}, nil
	case "<":
		return interval{version{}, exact}, nil
	case "<=":
		return interval{version{}, version{major, minor, patch + 1}}, nil
	case "", "=":
		// A bare version is one exact version, never the range its written
		// depth suggests: composer reads "10" as =10.0.0 and "10.4" as
		// =10.4.0, filling the omitted places with zero rather than spanning
		// them. Reading them as ranges made "7.8 7" — two pins that can never
		// both hold — look satisfiable, which a fuzz corpus of 2,703 random
		// constraints caught and a corpus of real ones never would have: real
		// core_version_requirement strings are carets.
		return interval{exact, version{major, minor, patch + 1}}, nil
	}

	return interval{}, fmt.Errorf("unsupported operator %q in %q", op, term)
}

func number(s string) (int, bool) {
	if s == "*" || s == "x" || s == "X" {
		return 0, true
	}
	n, _ := strconv.Atoi(s)
	return n, false
}

func optional(s string) (value int, given bool, wild bool) {
	if s == "" {
		return 0, false, false
	}
	n, isWild := number(s)
	return n, true, isWild
}

package drupal

import (
	"regexp"
	"strings"
)

// The issue's version field says which branch a patch belongs on, and it holds
// whatever anyone typed. Sampled live: "2.0.0", "8.0.x-dev", "4.6.x-dev",
// "5.1", "6.14", and "x.y.z" fifty-six times.
//
// So candidates are proposed most-specific-first and the caller intersects
// them with the branches that actually exist. Nothing here parses
// authoritatively: a version naming no real branch simply matches nothing and
// the caller falls back.
var (
	devSuffix     = regexp.MustCompile(`-dev$`)
	legacyContrib = regexp.MustCompile(`^(\d+\.x)-(\d+)\.`)
	semverish     = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.\d+)?`)
)

// BranchCandidates are the branch names a version field might mean,
// most-specific first.
func BranchCandidates(version string) []string {
	raw := strings.TrimSpace(version)
	if raw == "" {
		return nil
	}

	// "2.0.x-dev" and "2.0.x" are the same branch; the suffix is
	// drupal.org's, not git's.
	base := devSuffix.ReplaceAllString(raw, "")

	candidates := []string{base}

	// Legacy contrib: "8.x-1.4" and "8.x-1.x" are both the 8.x-1.x branch.
	if m := legacyContrib.FindStringSubmatch(base); m != nil {
		candidates = append(candidates, m[1]+"-"+m[2]+".x")
	}

	// Semver-ish: 2.0.0 lives on 2.0.x, and some projects branch at 2.x.
	if m := semverish.FindStringSubmatch(base); m != nil {
		candidates = append(candidates, m[1]+"."+m[2]+".x", m[1]+".x")
	}

	return uniqueNonEmpty(candidates)
}

// ResolveBranch picks the first candidate that is a branch the project really
// has, or "" when none is.
func ResolveBranch(version string, existingBranches []string) string {
	exists := make(map[string]bool, len(existingBranches))
	for _, branch := range existingBranches {
		exists[branch] = true
	}

	for _, candidate := range BranchCandidates(version) {
		if exists[candidate] {
			return candidate
		}
	}

	return ""
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}

	return out
}

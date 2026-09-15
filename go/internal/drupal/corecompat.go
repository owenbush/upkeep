// Package drupal holds Drupal's own version semantics.
package drupal

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

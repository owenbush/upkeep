package adapter

import "fmt"

// PatchPromotion is what promoting a patch onto a work branch actually
// achieved.
//
// Promotion used to be all or nothing: the patch applied and was committed, or
// it did not and you were told to re-roll it. For a patch that has gone stale
// — or one cut against a release tarball, which can never apply to a git
// checkout at all — that is a dead end. The work of re-rolling is exactly the
// work the failed apply was doing: put what still fits onto a branch, fix what
// does not, commit, publish. So a partial promotion is a first-class outcome
// rather than an error, and this says which it was.
//
// A partial promotion is deliberately *not* committed. The tree is left dirty
// with .rej files beside the files that would not take, because a commit
// carrying the patch author's attribution should say what the author wrote —
// and half of it, plus rejects, is not that yet.
type PatchPromotion struct {
	// sha is the commit carrying the patch, empty when hunks were rejected.
	sha string
	// Applied are the files the patch changed successfully.
	Applied []string
	// Rejected are the files left with a .rej beside them.
	Rejected []string
}

// CommittedPromotion is a promotion that applied whole and was committed.
func CommittedPromotion(sha string, applied []string) PatchPromotion {
	return PatchPromotion{sha: sha, Applied: applied}
}

// PartialPromotion is a promotion that applied what it could and left the rest
// as rejects.
func PartialPromotion(applied, rejected []string) PatchPromotion {
	return PatchPromotion{Applied: applied, Rejected: rejected}
}

// IsComplete reports whether the patch applied whole.
func (p PatchPromotion) IsComplete() bool { return p.sha != "" }

// RequireSHA is the commit that carries the patch. It reports an error when
// asked of a partial promotion, which has none.
func (p PatchPromotion) RequireSHA() (string, error) {
	if p.sha == "" {
		return "", fmt.Errorf("a partial promotion has no commit")
	}

	return p.sha, nil
}

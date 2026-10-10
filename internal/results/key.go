// Package results is the file-backed store for local check results, and what
// a row knows locally across the cores it applies to.
//
// LocalEvidence lives here rather than with the dashboard, where the PHP tree
// keeps it. In PHP the dashboard depends on the gate (for a verdict) and the
// gate depends on the dashboard (for evidence), which is legal there and is
// not an import cycle Go will compile. Evidence is the thing both sides
// actually share, so it sits below both.
package results

import (
	"fmt"
	"strconv"
)

// Key is what a cached check result is *about*: a merge request, or a patch on
// an issue.
//
// The two live in one store and must never collide. Both are identified by a
// number, and nothing separates the ranges — a module could plausibly have
// merge request !3597808 while an issue carries node id 3597808 — so the
// distinction is made structurally, in the path segment, rather than left to
// the hope that the numbers stay apart. A patch result read as a merge-request
// result would put patch evidence in front of the fast-lane gate, which is the
// one thing that must not happen.
type Key struct {
	Segment string
	IsPatch bool
	Number  int
}

// MergeRequestKey keys a result by merge-request IID.
func MergeRequestKey(iid int) (Key, error) {
	if err := assertPositive(iid, "merge request IID"); err != nil {
		return Key{}, err
	}

	return Key{Segment: strconv.Itoa(iid), IsPatch: false, Number: iid}, nil
}

// PatchKey keys a result by the node id of the issue the patch is on.
func PatchKey(issueNid int) (Key, error) {
	if err := assertPositive(issueNid, "issue node id"); err != nil {
		return Key{}, err
	}

	return Key{Segment: "patch-" + strconv.Itoa(issueNid), IsPatch: true, Number: issueNid}, nil
}

func assertPositive(value int, what string) error {
	if value < 1 {
		return fmt.Errorf("a cached result is keyed by a positive %s, got %d", what, value)
	}

	return nil
}

// Package adapter is everything engine-specific — ddev, ddev-drupal-contrib,
// docker, git mechanics — behind one interface the rest of upkeep talks to.
package adapter

import (
	"regexp"
	"strconv"
	"strings"
)

const (
	mrPrefix    = "mr-"
	patchPrefix = "patch-"
)

var managedBranch = regexp.MustCompile(`^(` + mrPrefix + `|` + patchPrefix + `)\d+$`)

// ManagedBranchForMergeRequest is the local branch an applied merge request is
// checked out on.
//
// Two operations put the working copy on a branch of their own — applying a
// merge request and applying a patch file — and both must be recognised by the
// base-branch resolution, because a managed branch is never a valid *base*.
// Resolving a base of patch-3597808 would silently test the next contribution
// on top of the previous one; naming both prefixes in one place is what stops
// the second operation from having to remember the first one's convention.
func ManagedBranchForMergeRequest(iid int) string { return mrPrefix + strconv.Itoa(iid) }

// ManagedBranchForPatch is the local branch an applied patch is checked out
// on.
//
// Keyed by issue, not by file: re-applying a different patch from the same
// issue replaces the branch rather than accumulating one branch per re-roll.
func ManagedBranchForPatch(nid int) string { return patchPrefix + strconv.Itoa(nid) }

// IsManagedBranch reports whether a branch name is one upkeep created, and so
// never a base.
func IsManagedBranch(branch string) bool { return managedBranch.MatchString(branch) }

// maxSlugLength is long enough to be recognisable in `git branch`, short
// enough to type.
const maxSlugLength = 40

var (
	nonSlug     = regexp.MustCompile(`[^a-z0-9]+`)
	workBranch  = regexp.MustCompile(`^\d{4,}(-|$)`)
	slugTrimmer = "-"
)

// IssueBranch is the branch a maintainer's own work on an issue lives on.
//
// Named to drupal.org's issue-fork convention — <nid>-<slug> — which is not
// decoration: it is the shape the issue-reference reader already parses, so a
// branch started here is one every other part of this tool (and drupal.org
// itself) recognises as belonging to that issue. Push it and the merge request
// that follows is linked to the issue with nothing further to configure.
//
// **A work branch is not a managed branch.** The managed ones — mr-<iid>,
// patch-<nid> — are reset from the base on every apply, because re-testing
// somebody else's contribution must test that contribution alone. A work
// branch holds the only copy of something a human wrote. Nothing in this tool
// may reset, force, or discard it, and the two kinds are deliberately unable
// to be confused: no managed prefix can ever begin with a digit, and a work
// branch always does.
type IssueBranch struct {
	IssueNid int
	Name     string
}

// IssueBranchFor is the branch for an issue, named from its title.
//
// It takes the id and title rather than an issue model so this stays clear of
// the drupal.org models: branch naming is adapter territory.
//
// The slug is cosmetic — everything that matters keys on the leading node id —
// so a title that reduces to nothing still yields a usable branch rather than
// a failure.
func IssueBranchFor(issueNid int, title string) IssueBranch {
	slug := Slug(title)
	if slug == "" {
		return IssueBranch{IssueNid: issueNid, Name: strconv.Itoa(issueNid)}
	}

	return IssueBranch{IssueNid: issueNid, Name: strconv.Itoa(issueNid) + "-" + slug}
}

// NamedIssueBranch is an explicitly named branch, still anchored to its issue.
func NamedIssueBranch(issueNid int, name string) IssueBranch {
	return IssueBranch{IssueNid: issueNid, Name: name}
}

// Matches reports whether a branch name is a work branch for this issue — the
// test that lets `start` resume yesterday's work instead of starting beside
// it.
func (b IssueBranch) Matches(branch string) bool {
	if branch == b.Name {
		return true
	}

	nid := strconv.Itoa(b.IssueNid)
	if !strings.HasPrefix(branch, nid) {
		return false
	}
	rest := branch[len(nid):]

	return rest == "" || strings.HasPrefix(rest, "-")
}

// IsWorkBranch reports whether any branch name looks like issue work, whoever
// created it.
//
// Used to refuse the disposable-branch machinery a target it must not touch:
// `git checkout -B` on one of these would discard commits nobody else has a
// copy of.
func IsWorkBranch(branch string) bool { return workBranch.MatchString(branch) }

// Slug reduces a title to a branch-safe slug: lowercase, words joined by
// hyphens, truncated on a word boundary so the name stays readable.
func Slug(title string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), slugTrimmer)
	if len(slug) <= maxSlugLength {
		return slug
	}

	cut := slug[:maxSlugLength]
	if lastHyphen := strings.LastIndex(cut, "-"); lastHyphen >= 0 {
		cut = cut[:lastHyphen]
	}

	return strings.Trim(cut, slugTrimmer)
}

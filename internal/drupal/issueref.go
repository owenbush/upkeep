package drupal

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Which issue a merge request is about.
//
// Two strengths of claim, and the difference is load-bearing. A reference that
// asserts *authorship* — the "Issue #NNN" title convention, an issue-fork
// branch name, a drupal.org issue URL in the auto-populated description —
// means this merge request is the issue's fix. A loose "Relates to #NNN"
// mention is a cross-reference, which is fine for a label and wrong as grounds
// for suppressing the issue's patches from `upkeep patches`.
var (
	titleIssue  = regexp.MustCompile(`(?i)Issue\s*#(\d+)`)
	branchIssue = regexp.MustCompile(`^(\d{4,})-`)
	relatesTo   = regexp.MustCompile(`(?i)Relates to\s*#(\d+)`)
	// closesIssue is GitLab's own closing vocabulary, which is how a merge
	// request declares what it fixes when the issue queue is GitLab's rather
	// than drupal.org's.
	//
	// The four verb families are GitLab's documented default closing pattern:
	// clos(e|es|ed|ing), fix(|es|ed|ing), resolv(e|es|ed|ing) and
	// implement(|s|ed|ing). Nothing else — "Relates to" stays rejected by the
	// owning rule below, and the same reasoning applies here: a reference that
	// does not claim to fix the issue must not be able to claim the issue.
	//
	// **Read from the description upkeep already has**, not from GitLab's
	// /closes_issues endpoint, which would be one request per merge request.
	// That cost is on record: the ISSUE cell used to make a per-merge-request
	// lookup and it was 155 requests on a pathauto refresh. The endpoint is
	// the authority, and the description is where the authority reads it from.
	//
	// Any number of digits, unlike branchIssue's four-or-more: a bare number
	// in a branch name needs the length to be unambiguous, where "#12" after
	// "Closes" does not — and a GitLab-native queue starts at #1.
	closesIssue = regexp.MustCompile(
		`(?i)\b(?:clos(?:e|es|ed|ing)|fix(?:es|ed|ing)?|resolv(?:e|es|ed|ing)|` +
			`implement(?:s|ed|ing)?)\b:?\s+#(\d+)`)
	issueURL    = regexp.MustCompile(`drupal\.org/project/[^/]+/issues/(\d+)`)
	nodeURL     = regexp.MustCompile(`drupal\.org/node/(\d+)`)
	forkPathNid = regexp.MustCompile(`^issue/.+-(\d{4,})$`)
)

// ExtractIssue finds the issue a merge request refers to, by any means.
func ExtractIssue(title, sourceBranch, description string) (int, bool) {
	return resolveIssue(title, sourceBranch, description, false)
}

// ExtractOwningIssue finds the issue a merge request claims to *fix*.
//
// A bare "Relates to #NNN" yields nothing, on purpose: otherwise anybody
// mentioning an issue could suppress its patches.
func ExtractOwningIssue(title, sourceBranch, description string) (int, bool) {
	return resolveIssue(title, sourceBranch, description, true)
}

// IssueFromForkPath reads the issue out of an issue fork's project path.
//
// "issue/pathauto-3616056" means drupal.org created that repository *for*
// issue 3616056 — a stronger claim than anything in a merge request's own
// metadata, because it is a fact about how the fork came to exist rather than
// a string somebody typed.
//
// It is also the only thing that pairs a Project Update Bot merge request to
// its issue: those are titled "Automated Project Update Bot fixes", branch
// `project-update-bot-only`, and their description says only "Relates to #NNN",
// which the owning rule rejects deliberately. Correct, and it left every bot
// merge request paired to nothing until forks were read.
func IssueFromForkPath(pathWithNamespace string) (int, bool) {
	m := forkPathNid.FindStringSubmatch(strings.TrimSpace(pathWithNamespace))
	if m == nil {
		return 0, false
	}

	return atoi(m[1]), true
}

// IssueURL is where a human goes to read the issue.
func IssueURL(nid int) string {
	return fmt.Sprintf("https://www.drupal.org/node/%d", nid)
}

func resolveIssue(title, sourceBranch, description string, owningOnly bool) (int, bool) {
	if m := titleIssue.FindStringSubmatch(title); m != nil {
		return atoi(m[1]), true
	}
	if m := branchIssue.FindStringSubmatch(sourceBranch); m != nil {
		return atoi(m[1]), true
	}
	if description == "" {
		return 0, false
	}

	return fromText(description, owningOnly)
}

func fromText(text string, owningOnly bool) (int, bool) {
	if m := titleIssue.FindStringSubmatch(text); m != nil {
		return atoi(m[1]), true
	}
	// Ranked with the owning signals rather than below them: "Closes #N" is a
	// declaration that this merge request fixes that issue, which is a
	// stronger claim than drupal.org's "Issue #N" title convention and far
	// stronger than a mention. Applies in both modes for that reason.
	if m := closesIssue.FindStringSubmatch(text); m != nil {
		return atoi(m[1]), true
	}
	if !owningOnly {
		if m := relatesTo.FindStringSubmatch(text); m != nil {
			return atoi(m[1]), true
		}
	}
	if m := issueURL.FindStringSubmatch(text); m != nil {
		return atoi(m[1]), true
	}
	if m := nodeURL.FindStringSubmatch(text); m != nil {
		return atoi(m[1]), true
	}

	return 0, false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)

	return n
}

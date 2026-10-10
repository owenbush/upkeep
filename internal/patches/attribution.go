package patches

import (
	"fmt"
	"strings"

	"github.com/owenbush/upkeep/internal/drupal"
)

// Attribution is the commit message a promoted patch is carried into a merge
// request under.
//
// Promoting somebody's patch is the one operation in this tool that moves
// another person's work under your own name. Nothing technical stops it and
// nothing technical would notice; what stops it is that the commit says whose
// work it is, in the place a reader of the branch will actually look.
//
// The subject follows drupal.org's own commit convention —
// "Issue #NNN by author: Title" — which is both what maintainers already read
// and, not incidentally, a string ExtractOwningIssue parses, so the resulting
// merge request pairs with its issue the same way every other one does.
//
// **There is deliberately no --author and no Co-authored-by:.** Both want an
// email address, and drupal.org's API publishes a username and a profile URL
// and no address at all. Synthesising one would put a claim about somebody
// else's identity into permanent history on the strength of a guess. A named
// trailer says the true thing instead: here is the account, here is the file
// it was posted as, here is where it came from. Credit on drupal.org is
// allocated through the issue-credit system regardless, which is a browser
// action on the issue and remains the promoter's to do.
type Attribution struct {
	IssueNid   int
	IssueTitle string
	PatchName  string
	PatchURL   string
	// Author is nil when the file records no owner, or the account could not
	// be read.
	Author *drupal.User
	// PromotedBy is empty when the promoter is not recorded.
	PromotedBy string
}

// AttributionFor builds the message for one patch.
func AttributionFor(
	issue drupal.Issue,
	patch drupal.IssueFile,
	author *drupal.User,
	promotedBy string,
) Attribution {
	return Attribution{
		IssueNid:   issue.Nid,
		IssueTitle: issue.Title,
		PatchName:  patch.Name,
		PatchURL:   patch.URL,
		Author:     author,
		PromotedBy: promotedBy,
	}
}

// Subject is drupal.org's convention, and the reason the merge request needs
// no separate title rule: "Issue #NNN by someone: Title".
func (a Attribution) Subject() string {
	credit := ""
	if a.Author != nil {
		credit = " by " + a.Author.Name
	}

	return fmt.Sprintf("Issue #%d%s: %s", a.IssueNid, credit, a.IssueTitle)
}

// Message is the subject, the provenance, and the trailers that make it
// checkable.
func (a Attribution) Message() string {
	lines := []string{a.Subject(), "", a.provenance(), ""}

	if a.Author != nil {
		lines = append(lines, fmt.Sprintf("Patch-author: %s <%s>", a.Author.Name, a.Author.ProfileURL))
	}
	lines = append(lines,
		"Patch-file: "+a.PatchName,
		"Patch-source: "+a.PatchURL,
		"Issue: "+drupal.IssueURL(a.IssueNid),
	)
	if a.PromotedBy != "" {
		lines = append(lines, "Promoted-by: "+a.PromotedBy)
	}

	return strings.Join(lines, "\n") + "\n"
}

// provenance is the paragraph a reviewer reads. It says the two things a
// promoted commit has to say and that no amount of trailer parsing conveys:
// this is somebody else's change, and the person who pushed it is not claiming
// it.
func (a Attribution) provenance() string {
	if a.Author == nil {
		return fmt.Sprintf(
			"Applied from the patch %q posted on the issue, and promoted to a merge\n"+
				"request with `upkeep patch:promote`. The change is the patch author's work;\n"+
				"drupal.org records no account for the file, so they are not named here — "+
				"credit\nthem on the issue.",
			a.PatchName,
		)
	}

	return fmt.Sprintf(
		"Applied from the patch %q, posted to the issue by %s, and\n"+
			"promoted to a merge request with `upkeep patch:promote`. The change is %s's\n"+
			"work; whoever opens the merge request is carrying it over, not authoring it.",
		a.PatchName, a.Author.Name, a.Author.Name,
	)
}

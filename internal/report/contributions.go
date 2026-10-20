package report

import (
	"github.com/owenbush/upkeep/internal/patches"
)

// ContributionsPayload is `issues` and `patches`.
//
// One shape for both, because they are two views of the same thing: `issues`
// lists every open issue and what has been contributed to it, `patches`
// narrows to the ones carrying patch files. A consumer that can read one can
// read the other, which is worth more than a payload tuned to each table.
type ContributionsPayload struct {
	Issues []ContributionLine `json:"issues"`
}

// ContributionLine is one issue and the work attached to it.
type ContributionLine struct {
	Module string    `json:"module"`
	Issue  *IssueRef `json:"issue"`

	// Kind is what arrived: patch-only, patch-and-merge-request,
	// patch-with-empty-merge-request, merge-request-only, or nothing. The
	// distinction a merge-request view cannot make, and the reason `patches`
	// exists.
	Kind string `json:"kind"`

	// MergeRequests is every merge request claiming authorship of this issue,
	// including empty ones — an empty merge request is why `kind` can be
	// patch-with-empty-merge-request, so dropping them here would hide the
	// evidence for the field beside them.
	MergeRequests []MergeRequestRef `json:"merge_requests"`

	// Landed is work on this issue that has already merged, which an open
	// issue can carry.
	Landed *Landing `json:"landed"`

	// Next is the command to run, taken from the caller so it is the same
	// string the table's NEXT column prints.
	Next string `json:"next"`
}

// Contributions builds the payload for `issues` and `patches`.
//
// next is supplied rather than computed here because the command owns that
// judgement and this package must not become a second opinion about it. It
// also keeps the dependency pointing the right way: the command layer imports
// report, never the reverse.
func Contributions(
	contributions []patches.Contribution, next func(patches.Contribution) string,
) ContributionsPayload {
	lines := make([]ContributionLine, 0, len(contributions))
	for _, contribution := range contributions {
		mrs := make([]MergeRequestRef, 0, len(contribution.MergeRequests))
		for _, mr := range contribution.MergeRequests {
			mrs = append(mrs, *mergeRequestRef(mr))
		}

		line := ContributionLine{
			Module:        contribution.Module,
			Issue:         issueRef(contribution.Issue),
			Kind:          kindName(contribution.Kind()),
			MergeRequests: mrs,
		}
		if next != nil {
			line.Next = next(contribution)
		}

		if landed := contribution.Landed(); landed != nil {
			line.Landed = &Landing{
				IID:       landed.IID,
				MergedAt:  landed.MergedAt,
				WebURL:    landed.WebURL,
				NewerWork: contribution.HasWorkNewerThanLanding(),
			}
		}

		lines = append(lines, line)
	}

	return ContributionsPayload{Issues: lines}
}

// kindName is the kind as a stable token.
//
// Not patches.Kind.SummaryLabel, which is prose for a summary line ("patch +
// MR", "nothing attached") and would make a consumer parse spaces and
// punctuation. These are the same five states under names that can be compared
// with ==.
func kindName(kind patches.Kind) string {
	switch kind {
	case patches.PatchOnly:
		return "patch-only"
	case patches.PatchAndMergeRequest:
		return "patch-and-merge-request"
	case patches.PatchWithEmptyMergeRequest:
		return "patch-with-empty-merge-request"
	case patches.MergeRequestOnly:
		return "merge-request-only"
	case patches.Nothing:
		return "nothing"
	default:
		return "unknown"
	}
}

package patches

// Kind is how the work on an issue has been delivered: as patch files, as a
// merge request that carries changes, as both, or as neither.
//
// The distinction the patch surface exists to draw is not "merge request or no
// merge request" but "is the work reachable from the MR-centric dashboard?" —
// so a merge request that carries no changes counts as no merge request at
// all, and an issue holding both a patch and a real one is worth showing
// rather than hiding: the patch may be newer than the branch, or may be what
// the branch should have been built from.
type Kind int

const (
	// PatchOnly is patch files, no merge request referencing the issue.
	PatchOnly Kind = iota
	// PatchAndMergeRequest is patch files, and a merge request that carries
	// changes.
	PatchAndMergeRequest
	// PatchWithEmptyMergeRequest is patch files, and merge requests — all of
	// them empty.
	PatchWithEmptyMergeRequest
	// MergeRequestOnly is no patch files; a merge request carries the work.
	MergeRequestOnly
	// Nothing is neither patch files nor a merge request that carries
	// anything.
	Nothing
)

// IsCoveredByMergeRequest reports whether this kind is reachable from the
// MR-centric dashboard, and so has no business on a patch report.
func (k Kind) IsCoveredByMergeRequest() bool { return k == MergeRequestOnly }

// HasSubstantiveMergeRequest reports whether any merge request carries the
// work for this issue.
func (k Kind) HasSubstantiveMergeRequest() bool {
	return k == PatchAndMergeRequest || k == MergeRequestOnly
}

// SummaryLabel is the segment label for the summary line. Empty for kinds
// never summarised.
func (k Kind) SummaryLabel() string {
	switch k {
	case PatchOnly:
		return "patch-only"
	case PatchAndMergeRequest:
		return "patch + MR"
	case PatchWithEmptyMergeRequest:
		return "patch, empty MR"
	case Nothing:
		return "nothing attached"
	default:
		return ""
	}
}

package drupal

// IssueStatus is a drupal.org issue status, by its api-d7 numeric value.
type IssueStatus int

const (
	StatusActive                IssueStatus = 1
	StatusFixed                 IssueStatus = 2
	StatusClosedDuplicate       IssueStatus = 3
	StatusPostponed             IssueStatus = 4
	StatusClosedWontFix         IssueStatus = 5
	StatusClosedWorksAsDesigned IssueStatus = 6
	StatusClosedFixed           IssueStatus = 7
	StatusNeedsReview           IssueStatus = 8
	StatusNeedsWork             IssueStatus = 13
	StatusRtbc                  IssueStatus = 14
	StatusPatchToBePorted       IssueStatus = 15
	StatusPostponedNeedsInfo    IssueStatus = 16
	StatusClosedOutdated        IssueStatus = 17
	StatusClosedCannotReproduce IssueStatus = 18
)

var statusLabels = map[IssueStatus]string{
	StatusActive:                "Active",
	StatusFixed:                 "Fixed",
	StatusClosedDuplicate:       "Closed (duplicate)",
	StatusPostponed:             "Postponed",
	StatusClosedWontFix:         "Closed (won't fix)",
	StatusClosedWorksAsDesigned: "Closed (works as designed)",
	StatusClosedFixed:           "Closed (fixed)",
	StatusNeedsReview:           "Needs review",
	StatusNeedsWork:             "Needs work",
	StatusRtbc:                  "Reviewed & tested by the community",
	StatusPatchToBePorted:       "Patch (to be ported)",
	StatusPostponedNeedsInfo:    "Postponed (maintainer needs more info)",
	StatusClosedOutdated:        "Closed (outdated)",
	StatusClosedCannotReproduce: "Closed (cannot reproduce)",
}

// allStatuses is the declaration order, because callers render lists of them
// and a map's iteration order in Go is deliberately random.
var allStatuses = []IssueStatus{
	StatusActive, StatusFixed, StatusClosedDuplicate, StatusPostponed, StatusClosedWontFix,
	StatusClosedWorksAsDesigned, StatusClosedFixed, StatusNeedsReview, StatusNeedsWork,
	StatusRtbc, StatusPatchToBePorted, StatusPostponedNeedsInfo, StatusClosedOutdated,
	StatusClosedCannotReproduce,
}

var closedStatuses = map[IssueStatus]bool{
	StatusFixed: true, StatusClosedDuplicate: true, StatusClosedWontFix: true,
	StatusClosedWorksAsDesigned: true, StatusClosedFixed: true, StatusClosedOutdated: true,
	StatusClosedCannotReproduce: true,
}

// IsOpen reports whether the issue is still live, as opposed to resolved or
// abandoned.
func (s IssueStatus) IsOpen() bool { return !closedStatuses[s] }

// AwaitsMaintainer reports whether the ball is with the maintainer rather than
// the contributor. Needs review and RTBC await a maintainer's verdict.
func (s IssueStatus) AwaitsMaintainer() bool {
	return s == StatusNeedsReview || s == StatusRtbc
}

// Known reports whether this is a status drupal.org actually issues.
func (s IssueStatus) Known() bool {
	_, ok := statusLabels[s]

	return ok
}

func (s IssueStatus) String() string {
	if label, ok := statusLabels[s]; ok {
		return label
	}

	return "unknown status"
}

// OpenStatuses is every status an issue can hold while it is still someone's
// problem.
//
// The canonical list. "Which issues does upkeep look at?" used to be answered
// separately by each command, and both said Needs review and RTBC — the two
// statuses a *contribution* sits in. That made the tool blind to the 55% of a
// project's open queue where work has not started yet: on pathauto, 17 Active,
// 18 Needs work and 16 Postponed issues were invisible, which is exactly where
// a maintainer's own work begins.
func OpenStatuses() []IssueStatus {
	open := make([]IssueStatus, 0, len(allStatuses))
	for _, status := range allStatuses {
		if status.IsOpen() {
			open = append(open, status)
		}
	}

	return open
}

// AwaitingReviewStatuses is the subset carrying a contribution to review — the
// historical scan, kept because "what needs reviewing?" is still a different
// question from "what is open?".
func AwaitingReviewStatuses() []IssueStatus {
	return []IssueStatus{StatusNeedsReview, StatusRtbc}
}

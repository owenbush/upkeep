package report

// The three write commands that produce something a caller needs afterwards.
//
// These are in the JSON surface even though the skill tells an agent not to
// reach them unasked, because the thing an agent needs when a human *has*
// asked is exactly what they return: a URL to open, a merge request to report,
// a SHA to build on. Printing those as prose and asking a program to find them
// again is the parsing problem this package exists to remove.

// ReviewPayload is `review`.
type ReviewPayload struct {
	Module string `json:"module"`
	Core   string `json:"core"`
	IID    int    `json:"iid"`
	Title  string `json:"title"`
	// SiteURL is the running site with the merge request applied.
	SiteURL string `json:"site_url"`
	// LoginURL is one-time and empty when the engine produced none.
	LoginURL string `json:"login_url,omitempty"`
	Fixture  string `json:"fixture,omitempty"`
}

// PublishPayload is `publish`.
type PublishPayload struct {
	Module string `json:"module"`
	Issue  int    `json:"issue"`
	Branch string `json:"branch"`
	// Destination is the project the branch was pushed to: an issue fork, or
	// the canonical project for a project whose issues live in GitLab.
	Destination string `json:"destination"`
	SHA         string `json:"sha,omitempty"`
	// MergeRequest is the one now open for this branch.
	MergeRequest *MergeRequestRef `json:"merge_request"`
	// Created separates opening a merge request from updating one that was
	// already open, which re-running publish after more commits does. Both are
	// success and they are not the same event.
	Created      bool   `json:"created"`
	TargetBranch string `json:"target_branch,omitempty"`
}

// PromotePayload is `patch:promote`.
type PromotePayload struct {
	Module string `json:"module"`
	Issue  int    `json:"issue"`
	Branch string `json:"branch"`
	// Committed is false for a --partial run, which applies what fits and
	// commits nothing: half somebody's patch is not what they wrote, and the
	// attribution rule is why it is never committed under their name.
	Committed bool   `json:"committed"`
	SHA       string `json:"sha,omitempty"`
	// Rejects are the hunks that did not apply, left as .rej files.
	Rejects []string `json:"rejects"`
}

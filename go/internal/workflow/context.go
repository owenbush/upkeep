package workflow

import (
	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/patches"
)

// MrContext is everything the single-merge-request commands need to act: the
// module, its GitLab project, the resolved open merge request, and the core
// major the run targets.
type MrContext struct {
	Module       cockpit.Module
	Project      gitlab.Project
	MergeRequest gitlab.MergeRequest
	CoreMajor    string

	// MergeRefSHA is the SHA of the merge request's /merge ref — the branch
	// merged into the current tip of its target.
	//
	// That is what a check of this merge request is actually about: it is the
	// tree the adapter checks out and the tree CI analyses. Empty when GitLab
	// publishes no merge ref, which happens when the merge request conflicts
	// with its target — the same case the adapter falls back to the branch on,
	// so both halves fall back together.
	MergeRefSHA string
}

// PatchContext is everything a patch command needs after resolution: which
// module and core it acts on, which issue the work belongs to, which of that
// issue's patches was chosen, and where the file now sits on disk.
//
// The patch-side counterpart of MrContext. It carries the issue as well as the
// file because the report is about a *contribution*, and a maintainer reading
// "phpcs failed" needs to know which issue to go and say so on.
type PatchContext struct {
	Module    cockpit.Module
	CoreMajor string
	Issue     drupal.Issue
	Patch     drupal.IssueFile
	LocalPath string

	// BaseBranch is the branch the issue is filed against, resolved against
	// the project's real branches. Empty when it could not be told, which the
	// adapter reads as "work it out from the working copy".
	BaseBranch string
}

// Application is what the adapter needs to apply this patch.
func (c PatchContext) Application() adapter.PatchApplication {
	return adapter.PatchApplication{
		IssueNid:   c.Issue.Nid,
		Name:       c.Patch.Name,
		LocalPath:  c.LocalPath,
		BaseBranch: c.BaseBranch,
	}
}

// Revision is what identifies the patch that was checked, for the results
// cache — derived from the source URL rather than the downloaded bytes,
// because the dashboard has to judge staleness from the attachment list
// without downloading anything.
func (c PatchContext) Revision() string { return patches.Revision(c.Patch.URL) }

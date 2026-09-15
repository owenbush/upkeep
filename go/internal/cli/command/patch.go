package command

import (
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/patches"
	"github.com/owenbush/upkeep/internal/workflow"
)

// PatchSurface is what the patch commands are wired with.
//
// Grouped rather than passed one by one, because all four take exactly this
// set and a command acquiring a fifth dependency should be visible here.
type PatchSurface struct {
	Engines adapter.Factory
	Clients cli.GitlabClients
	Issues  IssueClients
	Prompts func(*cobra.Command) cli.Prompt
	// Downloader fetches the patch file. Separate from the API clients
	// because it talks to drupal.org's file host rather than its API, and a
	// test needs to answer it without answering the other.
	Downloader *http.Client
}

// addPatchSurface is the argument and flag set the patch commands share.
//
// The patch-side mirror of the merge-request surface, deliberately: a patch
// contribution should cost a maintainer exactly what a merge request costs.
func addPatchSurface(cmd *cobra.Command) {
	cli.AddTargetCore(cmd)
	cmd.Flags().String("file", "",
		"Apply this exact attachment by filename instead of choosing (e.g. 3597808-9-d11.patch)")
	cmd.Flags().String("url", "",
		"Fetch the patch from this URL instead of the issue's attachments "+
			"(a fork, a re-roll posted elsewhere)")
	cmd.Flags().Bool("latest", false,
		"Take the newest patch without asking, even when the issue carries several")
	cli.AddCockpit(cmd)
	cli.AddProjectsRoot(cmd)
	cli.AddNoUpdate(cmd)
	cli.AddVerbose(cmd)
	cli.AddModuleCompletion(cmd)
}

// issueNid is the issue node id rule: a positive integer.
//
// Refused here rather than turned into a request for node 0 and a confusing
// miss. Same rule, same reasoning, as the merge-request one.
func issueNid(raw string) (int, error) {
	nid, err := strconv.Atoi(raw)
	if err != nil || nid < 1 || strings.ContainsAny(raw, "+-") {
		return 0, fmt.Errorf(
			"the <issue> argument must be a drupal.org issue node id (a positive integer), got %q",
			raw,
		)
	}

	return nid, nil
}

// resolvePatchContext resolves module, core, issue and patch, and downloads
// the file.
func resolvePatchContext(
	cmd *cobra.Command, surface PatchSurface, moduleName, rawNid string,
) (*cockpit.Cockpit, workflow.PatchContext, error) {
	where, module, coreMajor, err := resolveSubject(cmd, moduleName)
	if err != nil {
		return nil, workflow.PatchContext{}, err
	}

	issue, err := requireIssue(cmd, surface.Issues, rawNid)
	if err != nil {
		return nil, workflow.PatchContext{}, err
	}

	patch, err := choosePatch(cmd, surface, issue)
	if err != nil {
		return nil, workflow.PatchContext{}, err
	}

	// The chosen patch already carries the URL: when --url named something the
	// issue does not list, choosePatch built a file around that URL, and when
	// it named an attachment it matched on the URL. So there is no second
	// source to reconcile here — reading the flag again would be a branch
	// nothing can take differently.
	cli.Progressf(cmd, "Patch: %s", patches.Describe(patch))
	localPath, err := patches.NewFetcher(surface.Downloader, where.PatchCachePath()).
		Fetch(issue.Nid, patch.Name, patch.URL)
	if err != nil {
		return nil, workflow.PatchContext{}, err
	}

	return where, workflow.PatchContext{
		Module:     module,
		CoreMajor:  coreMajor,
		Issue:      issue,
		Patch:      patch,
		LocalPath:  localPath,
		BaseBranch: resolveBaseBranch(cmd, surface, module, issue),
	}, nil
}

// choosePatch settles which of an issue's patches this run is about.
//
// The one thing the patch side has that the merge-request side does not is a
// *choice*: an issue routinely carries several, so when the operator has not
// pinned one and there is a terminal to ask at, they are asked.
func choosePatch(
	cmd *cobra.Command, surface PatchSurface, issue drupal.Issue,
) (drupal.IssueFile, error) {
	if explicit := cli.Flag(cmd, "url"); explicit != "" {
		// The URL names the file; the issue is still required, because it is
		// what the branch, the commit message and the report are keyed on. A
		// named attachment still wins for its metadata when the URL happens to
		// be one of them.
		for _, candidate := range patches.Candidates(issue) {
			if candidate.URL == explicit {
				return candidate, nil
			}
		}

		return drupal.IssueFile{
			Name: patches.SafeName(path.Base(explicit)), URL: explicit,
		}, nil
	}

	selection := patches.Select(issue, cli.Flag(cmd, "file"), cli.Switched(cmd, "latest"))
	if selection.Problem != "" {
		return drupal.IssueFile{}, fmt.Errorf("%s", selection.Problem)
	}
	if selection.Chosen != nil {
		return *selection.Chosen, nil
	}

	return askWhichPatch(cmd, surface, selection.Candidates), nil
}

// askWhichPatch asks, or takes the newest and says so.
func askWhichPatch(
	cmd *cobra.Command, surface PatchSurface, candidates []drupal.IssueFile,
) drupal.IssueFile {
	prompt := surface.Prompts(cmd)

	if !prompt.Interactive() {
		// Nothing to ask and nobody to ask: take the newest and say so,
		// because a scripted run that silently guessed would report a verdict
		// on a patch the operator never named.
		cli.Warnf(cmd,
			"Issue carries %d patches and none was named; taking the newest (%s). "+
				"Pass --file or --latest to make this explicit.",
			len(candidates), candidates[0].Name)

		return candidates[0]
	}

	// Labels must be distinct or the answer cannot be mapped back: the update
	// bot re-uploads under one filename, so an issue can carry several
	// attachments whose plain descriptions are identical.
	labels := patches.Labels(candidates)
	labels[0] += " [newest]"

	answer := prompt.Choose("Which patch should be applied?", labels)
	for i, label := range labels {
		if label == answer {
			return candidates[i]
		}
	}

	return candidates[0]
}

// resolveBaseBranch is the branch the issue is filed against, confirmed to
// exist.
//
// An issue carries a version and its patches are cut from that branch.
// Resolving it turns "does not apply to 1.0.x" — which was true, and useless,
// because the patch was never meant for 1.0.x — into applying it where it
// belongs.
//
// Everything here degrades to "": the adapter then resolves the base from the
// working copy exactly as before. A version field nobody set, a project whose
// branches cannot be listed, and a version naming no real branch are all
// ordinary, and none is worth refusing over.
func resolveBaseBranch(
	cmd *cobra.Command, surface PatchSurface, module cockpit.Module, issue drupal.Issue,
) string {
	if len(drupal.BranchCandidates(issue.Version)) == 0 {
		return ""
	}

	// Read-only, and the patch surface is otherwise GitLab-free: without a
	// token this reads anonymously, and a private project simply answers
	// nothing, which is the same "cannot tell" as every other failure here.
	client := cli.ReadingClient(cmd, surface.Clients)

	project, failure := client.Project(module.Project)
	if failure != nil {
		return ""
	}

	branches, failure := client.BranchNames(*project)
	if failure != nil {
		return ""
	}

	branch := drupal.ResolveBranch(issue.Version, branches)
	if branch == "" {
		// Said out loud: the issue names a version, the project has no such
		// branch, and the apply is about to use a different one.
		cli.Warnf(cmd,
			"Issue version %q matches no branch on %s (%s); using the working copy's base.",
			issue.Version, module.Project, strings.Join(branches, ", "))

		return ""
	}

	cli.Progressf(cmd, "Base branch: %s (from the issue version %q)", branch, issue.Version)

	return branch
}

// describePatchContext is the one-line summary printed before the long steps.
func describePatchContext(cmd *cobra.Command, context workflow.PatchContext, requestedCore string) {
	cli.Progressf(cmd, "Issue #%d %q (%s)",
		context.Issue.Nid, context.Issue.Title, context.Issue.Status.ShortLabel())

	defaulted := ""
	if requestedCore == "" {
		defaulted = " (default: the first core version listed for the module)"
	}
	cli.Progressf(cmd, "Target: Drupal core %s%s, module %s",
		context.CoreMajor, defaulted, context.Module.Name)
}

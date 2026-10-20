package command

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/report"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewIssue builds the issue command.
//
// Shows the drupal.org issue linked to a merge request and opens it in the
// browser — the quickest path to changing an issue status, since the
// drupal.org API is read-only and the change is a browser action.
func NewIssue(clients cli.GitlabClients, issues IssueClients, browser cli.Browser) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue <module> <mr>",
		Short: "Show and open the drupal.org issue linked to a merge request",
		Args:  cobra.ExactArgs(2),
	}
	cli.Describe(cmd, cli.Traits{
		Changes:    cli.ChangesNothing,
		Credential: cli.CredentialNone,
	})
	cli.AddNoOpen(cmd)
	cli.AddCockpit(cmd)
	cli.AddModuleCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runIssue(cmd, clients, issues, browser, args[0], args[1])
	})

	return cmd
}

func runIssue(
	cmd *cobra.Command, clients cli.GitlabClients, issues IssueClients,
	browser cli.Browser, moduleName, rawIID string,
) (int, error) {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return 0, err
	}
	modules, err := cli.Modules(where)
	if err != nil {
		return 0, err
	}
	module, err := cli.ResolveModule(where, modules, moduleName)
	if err != nil {
		return 0, err
	}
	iid, err := cli.MrIID(rawIID)
	if err != nil {
		return 0, err
	}

	client := cli.ReadingClient(cmd, clients)

	project, failure := client.Project(module.Project)
	if failure != nil {
		return 0, fmt.Errorf("could not resolve project [%s]: %s", failure.ShortCode(), failure.Message)
	}

	mergeRequest, failure := client.MergeRequest(*project, iid)
	if failure != nil {
		return 0, fmt.Errorf("could not fetch MR !%d [%s]: %s", iid, failure.ShortCode(), failure.Message)
	}

	nid, found := drupal.ExtractIssue(
		mergeRequest.Title, mergeRequest.SourceBranch, mergeRequest.Description)
	if !found {
		return 0, fmt.Errorf(
			"no issue number found in MR !%d. Checked title (%q) and branch (%q) — "+
				"neither contains an issue reference",
			iid, mergeRequest.Title, mergeRequest.SourceBranch,
		)
	}

	issueURL := drupal.IssueURL(nid)

	cli.Printf(cmd, "Issue #%d\n", nid)
	if issue, readable := issues.Issues().Issue(module.Name, nid); readable {
		describeIssue(cmd, issue)
		issueURL = issue.URL
	} else {
		cli.Printf(cmd, "  URL:        %s\n", issueURL)
		cli.Println(cmd,
			"  (Could not fetch issue details from drupal.org — the URL is still valid.)")
	}
	cli.Printf(cmd, "  MR:         !%d %q\n", mergeRequest.IID, mergeRequest.Title)
	cli.Println(cmd, "")

	if !cli.Switched(cmd, cli.FlagNoOpen) {
		if browser.Open(issueURL) {
			cli.Println(cmd, "Opened in browser. Change the status on the drupal.org issue page.")
		} else {
			cli.Printf(cmd, "Could not open browser automatically. Visit: %s\n", issueURL)
		}
	}

	return workflow.OK, nil
}

// describeIssue prints what drupal.org knows, omitting the fields it does not.
//
// Omitted rather than shown blank: an empty "Priority:" line is a field
// somebody forgot, while its absence is the API not having said.
func describeIssue(cmd *cobra.Command, issue drupal.Issue) {
	cli.Printf(cmd, "  Title:      %s\n", issue.Title)
	cli.Printf(cmd, "  Status:     %s\n", issue.Status)

	for _, field := range []struct {
		label string
		value string
	}{
		{"Priority", issue.PriorityLabel()},
		{"Category", issue.Category},
		{"Version", issue.Version},
		{"Component", issue.Component},
	} {
		if field.value != "" {
			cli.Printf(cmd, "  %-11s %s\n", field.label+":", field.value)
		}
	}

	cli.Printf(cmd, "  URL:        %s\n", issue.URL)
}

// NewStart builds the start command.
//
// Begins work on a drupal.org issue: an environment, and a branch to write on.
// The entry point upkeep was missing — every other verb starts from a
// *contribution*, which meant the half of the job where a maintainer writes
// the fix happened somewhere else entirely.
//
// The branch follows drupal.org's issue-fork convention, so it is the shape
// drupal.org, GitLab and this tool's own reference parsing all recognise.
// Resumes rather than restarts: an existing branch is checked out as it
// stands, and nothing here resets, forces or discards — unlike the disposable
// branches, this may hold the only copy of something a human wrote.
func NewStart(engines adapter.Factory, issues IssueClients) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start <module> <issue>",
		Short: "Start (or resume) work on a drupal.org issue",
		Long: `Begins work on an issue nobody has contributed to yet: provisions the
environment and opens a branch named to drupal.org's issue-fork convention, so
the merge request that follows is linked to the issue.

  cd $(upkeep start pathauto 3223746)
  upkeep start pathauto 3223746 --version=11

Resumes rather than restarts: an existing branch is checked out as it stands,
and nothing here ever resets or discards.
When the work is ready: upkeep publish <module> <issue>.`,
		Args: cobra.ExactArgs(2),
	}
	cli.Describe(cmd, cli.Traits{
		Changes:    cli.ChangesWorkingCopy,
		Credential: cli.CredentialNone,
	})
	cli.AddTargetCore(cmd)
	cli.AddCockpit(cmd)
	cli.AddProjectsRoot(cmd)
	cli.AddNoUpdate(cmd)
	cli.AddVerbose(cmd)
	cmd.Flags().String("branch", "",
		"Name the branch yourself instead of deriving it from the issue title")
	cmd.Flags().String("base", "",
		"Branch to start from (default: whatever the module working copy is currently on)")
	cli.AddModuleCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runStart(cmd, engines, issues, args[0], args[1])
	})

	return cmd
}

func runStart(
	cmd *cobra.Command, engines adapter.Factory, issues IssueClients, moduleName, rawNid string,
) (int, error) {
	where, module, coreMajor, err := resolveSubject(cmd, moduleName)
	if err != nil {
		return 0, err
	}

	issue, err := requireIssue(cmd, issues, module.Name, rawNid)
	if err != nil {
		return 0, err
	}

	branch := branchFor(cmd, issue)

	cli.Progressf(cmd, "Issue #%d %q (%s)", issue.Nid, issue.Title, issue.Status.ShortLabel())
	cli.Progressf(cmd, "Target: Drupal core %s, module %s", coreMajor, module.Name)

	engine, err := cli.Engine(cmd, engines, where)
	if err != nil {
		return 0, err
	}

	environment, err := ensureEnvironment(cmd, engine, module, coreMajor)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Branch")
	// An empty base means "resolve it from the working copy" — the adapter
	// owns that knowledge and resolves it the same way the apply paths do, so
	// a branch started here and a contribution checked out here share an
	// origin.
	resumed, err := engine.StartWork(environment, branch, cli.Flag(cmd, "base"), cli.BaseRefresh(cmd))
	if err != nil {
		return 0, err
	}

	began := "Started"
	if resumed {
		began = "Resumed"
	}
	cli.Progressf(cmd,
		"%s %s. Write your fix, then: upkeep check %s --working-copy, and upkeep publish %s %d",
		began, branch.Name, module.Name, module.Name, issue.Nid)
	cli.Progressf(cmd, "Issue: %s", issue.URL)

	cli.Println(cmd, environment.ProjectPath)

	return workflow.OK, nil
}

// requireIssue reads an issue, refusing a node id that cannot be one.
func requireIssue(
	cmd *cobra.Command, issues IssueClients, machineName, rawNid string,
) (drupal.Issue, error) {
	nid, err := issueNid(rawNid)
	if err != nil {
		return drupal.Issue{}, err
	}

	cli.Progressf(cmd, "Resolving issue #%d ...", nid)
	issue, found := issues.Issues().Issue(machineName, nid)
	if !found {
		return drupal.Issue{}, fmt.Errorf(
			"issue #%d could not be read for %s. Check the issue "+
				"number (it is the number in the issue URL)", nid, machineName,
		)
	}

	return issue, nil
}

// branchFor is the work branch this run acts on.
// publishCore is the core whose environment actually holds the branch.
//
// **Defaulting this was dangerous and that is why it is gone.** An environment
// is one per (module x core), and every other command defaults the core to
// `core_versions[0]`. For publish that default is a guess about *where the
// work is*, and the guess is wrong in the case the tool most encourages:
// `modules:track` appends, deliberately, so a module that gained core 12
// still defaults to 11 while the work happens against 12.
//
// Benign when it misses — PushWork refuses a branch the working copy is not
// on. Not benign when it hits: the same branch checked out in two
// environments at different commits means publish pushes the ones from the
// environment nobody was looking at, to a merge request that is already open.
// No `--force` bounds it to a fast-forward, so remote history survives; the
// wrong work still lands on somebody's contribution.
//
// So the branch decides. It is the thing the operator named, it exists in at
// most a few places, and asking each is a local git call — no round trip, no
// guess. An explicit `--version` still wins outright: it was already validated
// against the module's tracked cores, and overruling somebody who said which
// core they meant would be its own surprise.
func publishCore(
	cmd *cobra.Command, engine adapter.Engine,
	module cockpit.Module, requestedCore, branch string,
) (string, error) {
	if cli.Flag(cmd, cli.FlagVersion) != "" {
		return requestedCore, nil
	}

	holding := []string{}
	elsewhere := []string{}
	for _, core := range module.CoreVersions {
		status, exists := engine.InspectWorkingCopy(module.Name, core)
		if !exists {
			continue
		}
		if status.CurrentBranch == branch {
			holding = append(holding, core)

			continue
		}
		on := status.CurrentBranch
		if on == "" {
			on = "a detached HEAD"
		}
		elsewhere = append(elsewhere, fmt.Sprintf("    Drupal %s — on %s", core, on))
	}

	switch len(holding) {
	case 1:
		cli.Progressf(cmd, "Publishing from the Drupal %s environment, which is on %s.",
			holding[0], branch)

		return holding[0], nil

	case 0:
		where := "no environment for this module has one yet"
		if len(elsewhere) > 0 {
			where = "the environments there are:" + "\n" + strings.Join(elsewhere, "\n")
		}

		return "", fmt.Errorf(
			"no environment for %q is on %q, so there is nothing to publish — %s\n"+
				"Check the branch out where you want to work on it, or name the core with --version=N",
			module.Name, branch, where,
		)

	default:
		return "", fmt.Errorf(
			"%q is checked out in more than one environment for %q (Drupal %s), and they may hold "+
				"different commits.\nName the one to publish from: --version=%s",
			branch, module.Name, strings.Join(holding, " and "), holding[0],
		)
	}
}

func branchFor(cmd *cobra.Command, issue drupal.Issue) adapter.IssueBranch {
	if named := cli.Flag(cmd, "branch"); named != "" {
		return adapter.NamedIssueBranch(issue.Nid, named)
	}

	return adapter.IssueBranchFor(issue.Nid, issue.Title)
}

// NewPublish builds the publish command.
//
// Pushes the work branch for an issue and opens the merge request for it — the
// step that closes the loop. Everything upkeep already does well begins at a
// merge request, and until this existed the only way to *get* one was to leave
// the tool.
//
// **It opens a merge request; it never merges one.** Those are opposite acts:
// proposing work for review is the thing the one-approval stance exists to
// protect, not the thing it restricts.
func NewPublish(
	engines adapter.Factory, clients cli.GitlabClients, issues IssueClients,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish <module> <issue>",
		Short: "Push an issue's work branch and open a merge request for it",
		Long: `Pushes the work branch for an issue and opens its merge request — after which
it is an ordinary MR that dashboard, check and merge already handle.

  upkeep publish pathauto 3223746
  upkeep publish pathauto 3223746 --draft

Opens merge requests; never merges one. Re-running after more commits updates
the existing MR rather than opening a second.`,
		Args: cobra.ExactArgs(2),
	}
	cli.Describe(cmd, cli.Traits{
		Changes:    cli.ChangesRemote,
		Credential: cli.CredentialRequired,
		JSON:       true,
	})
	cli.AddTargetCore(cmd)
	cli.AddCockpit(cmd)
	cli.AddProjectsRoot(cmd)
	cli.AddVerbose(cmd)
	cli.AddJSON(cmd)
	cmd.Flags().String("branch", "", "Publish this branch instead of deriving it")
	cmd.Flags().String("title", "", "Merge request title (default: from the issue)")
	cmd.Flags().String("target", "",
		"Branch to merge into (default: the base the work branch was started from)")
	cmd.Flags().Bool("draft", false, "Open it as a draft")
	cmd.Flags().String("description", "",
		"Merge request body, after the issue reference (default: a one-line note)")
	cmd.Flags().String("description-file", "",
		"Read the merge request body from this file instead")
	cmd.Flags().Bool("no-footer", false,
		"Leave the body empty rather than adding upkeep's own note")
	cli.AddModuleCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runPublish(cmd, engines, clients, issues, args[0], args[1])
	})

	return cmd
}

func runPublish(
	cmd *cobra.Command, engines adapter.Factory, clients cli.GitlabClients, issues IssueClients,
	moduleName, rawNid string,
) (int, error) {
	where, module, coreMajor, err := resolveSubject(cmd, moduleName)
	if err != nil {
		return 0, err
	}

	// The node id is checked before the credential is: a typo in the argument
	// should not read as "you have no GitLab token".
	if _, err := issueNid(rawNid); err != nil {
		return 0, err
	}

	// Publishing writes, so this is the strict path: pushing and opening a
	// merge request both need a credential, and finding that out after the
	// push would leave a branch on a remote with nothing pointing at it.
	client, err := cli.WritingClient(cmd, clients)
	if err != nil {
		return 0, err
	}

	issue, err := requireIssue(cmd, issues, module.Name, rawNid)
	if err != nil {
		return 0, err
	}
	branch := branchFor(cmd, issue)

	project, failure := client.Project(module.Project)
	if failure != nil {
		modules, _ := cli.Modules(where)

		return 0, fmt.Errorf("%s", cockpit.ProjectFailure(modules, module, failure.Message))
	}

	cli.Progressf(cmd, "Destination")
	route, err := publishRouteFor(client, issues, *project, issue, module.Name)
	if err != nil {
		return 0, err
	}
	cli.Progressf(cmd, "Pushing to %s", route.Where)

	engine, err := cli.Engine(cmd, engines, where)
	if err != nil {
		return 0, err
	}

	// Which environment holds the branch, rather than whichever core happens
	// to be first in the registry. See publishCore.
	coreMajor, err = publishCore(cmd, engine, module, coreMajor, branch.Name)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Push")
	environment, err := engine.EnsureEnv(module, coreMajor)
	if err != nil {
		return 0, err
	}

	sha, err := engine.PushWork(environment, branch, route.Remote)
	if err != nil {
		return 0, err
	}
	cli.Progressf(cmd, "Pushed %s at %s.", branch.Name, shortSHA(sha))

	cli.Progressf(cmd, "Merge request")
	existing, failure := client.MergeRequestForBranch(*project, branch.Name, &route.Source)
	if failure != nil {
		return 0, fmt.Errorf(
			"the branch was pushed, but its merge requests could not be listed: %s", failure.Message)
	}
	if existing != nil {
		// Re-running publish after more commits is the normal way to update a
		// merge request: the push above already moved it.
		if cli.WantsJSON(cmd) {
			return workflow.OK, emitJSON(cmd, "publish", publishPayload(
				module.Name, issue.Nid, branch.Name, route, sha, existing, false, ""), nil)
		}

		cli.Printf(cmd, "Updated the open merge request for this branch: !%d\n", existing.IID)
		cli.Println(cmd, existing.WebURL)

		return workflow.OK, nil
	}

	target, err := publishTarget(cmd, engine, environment, *project, module.Name, issue.Nid)
	if err != nil {
		return 0, err
	}

	// Posted to the project that *holds the branch*, naming the canonical one
	// as the destination. Backwards-looking until you remember that the branch
	// is the subject of the request — and for a same-project branch the two
	// are one, which CreateMergeRequest handles by omitting target_project_id.
	description, err := publishDescription(cmd, route.Reference)
	if err != nil {
		return 0, err
	}

	created, failure := client.CreateMergeRequest(
		route.Source, branch.Name, target,
		mergeRequestTitle(cmd, issue),
		description,
		project,
	)
	if failure != nil {
		// The failure's own browser URL, which the client builds from the
		// project this was posted against — the PHP had a fallback here for a
		// nullable field, and in Go every failure constructor is handed one.
		return 0, fmt.Errorf(
			"the branch was pushed, but the merge request could not be opened: %s\n"+
				"Open it in the browser: %s", failure.Message, failure.BrowserURL)
	}

	if cli.WantsJSON(cmd) {
		return workflow.OK, emitJSON(cmd, "publish", publishPayload(
			module.Name, issue.Nid, branch.Name, route, sha, created, true, target), nil)
	}

	cli.Printf(cmd, "Opened !%d against %s.\n", created.IID, target)
	cli.Println(cmd, created.WebURL)
	cli.Printf(cmd,
		"It is now an ordinary MR: upkeep check %s %d, and it appears on the dashboard.\n",
		module.Name, created.IID)

	return workflow.OK, nil
}

// publishTarget is what the merge request is opened against.
//
// The base the work was cut from, else what the project itself calls default.
// Never a tracked core major: those name versions of Drupal, not branches, and
// no contrib project has one called "11" — defaulting to one made every
// publish target a branch that does not exist.
func publishTarget(
	cmd *cobra.Command,
	engine adapter.Engine,
	environment adapter.Environment,
	project gitlab.Project,
	moduleName string,
	nid int,
) (string, error) {
	if named := cli.Flag(cmd, "target"); named != "" {
		return named, nil
	}
	if recorded := engine.RecordedBaseBranch(environment); recorded != "" {
		return recorded, nil
	}
	if project.DefaultBranch != "" {
		return project.DefaultBranch, nil
	}

	return "", fmt.Errorf(
		"the branch was pushed, but upkeep does not know what to open the merge request "+
			"against.\nName it: upkeep publish %s %d --target=<branch> (a branch on the "+
			"project, like 2.0.x — not a core version)", moduleName, nid)
}

// publishPayload describes what publish just did.
//
// Both exits go through it, because "opened" and "updated" are the same event
// with one field different — and a consumer told only "success" cannot tell
// whether a merge request now exists that did not before.
func publishPayload(
	module string, nid int, branch string, route publishRoute, sha string,
	mr *gitlab.MergeRequest, created bool, target string,
) report.PublishPayload {
	payload := report.PublishPayload{
		Module:       module,
		Issue:        nid,
		Branch:       branch,
		Destination:  route.Where,
		SHA:          sha,
		Created:      created,
		TargetBranch: target,
	}
	if mr != nil {
		payload.MergeRequest = &report.MergeRequestRef{
			IID:          mr.IID,
			Title:        mr.Title,
			Draft:        mr.Draft,
			SourceBranch: mr.SourceBranch,
			WebURL:       mr.WebURL,
			HeadSHA:      mr.HeadSHA,
		}
	}

	return payload
}

// publishRoute is where a published branch goes, and what the merge request
// opened from it says.
type publishRoute struct {
	// Where names the destination for the progress line.
	Where string
	// Remote is where the branch is pushed.
	Remote adapter.GitRemote
	// Source is the project that will hold the branch. A merge-request lookup
	// narrows by it, and the request is opened from it.
	Source gitlab.Project
	// Reference is how the merge request declares the issue it fixes, and it
	// is always the first line of the description — which is where every
	// reader looks for it: GitLab's closing pattern, upkeep's own pairing,
	// and a person opening the page.
	//
	// The route owns this and the flags do not, because a description without
	// it is a merge request that pairs with nothing on the dashboard that
	// opened it.
	Reference string
}

// publishRouteFor decides where the work goes back to.
//
// **The issue fork wins wherever there is one**, which keeps every drupal.org
// project and every *migrated* issue behaving exactly as before: `helper` has
// 85 issue forks, all for issues that predate its migration, and a merge
// request for one of those still belongs on its fork.
//
// The canonical project is the fall back, and only for a project whose issues
// are on GitLab. There the refusal it replaces could never be satisfied: a
// GitLab-native issue has no drupal.org issue page, so nothing can mint a
// fork for it, and the old message sent people to `drupal.org/node/<nid>` to
// click a button that is not there. Worse, GitLab continues its iid sequence
// from the migrated node ids, so that URL resolves to an unrelated node —
// measured on helper issue 3619173, where drupal.org node 3619173 is a German
// translation correction. Sending somebody to the wrong page confidently is
// worse than not answering.
//
// A project that has *not* moved keeps the old refusal, because there the
// fork is genuinely missing and clicking the button is genuinely the fix.
func publishRouteFor(
	client *gitlab.Client,
	issues IssueClients,
	project gitlab.Project,
	issue drupal.Issue,
	moduleName string,
) (publishRoute, error) {
	fork, failure := client.IssueFork(project, issue.Nid)
	if failure != nil {
		return publishRoute{}, fmt.Errorf(
			"the issue fork for #%d could not be read: %s", issue.Nid, failure.Message)
	}

	if fork != nil {
		if err := assertPushable(*fork, fork.PathWithNamespace, issue.Nid); err != nil {
			return publishRoute{}, err
		}

		return publishRoute{
			Where:  fork.PathWithNamespace,
			Remote: adapter.IssueForkRemote(issue.Nid, fork.SSHURL),
			Source: *fork,
			// `Closes #<nid>`, which is what drupal.org itself auto-populates
			// — measured on pathauto, three of six open merge requests are
			// exactly that line and nothing else.
			//
			// It used to be `Fixes <issue.URL>`, and that was only reliable
			// by accident: the pairing matches a /node/ or a
			// /project/x/issues/ URL, which is the shape api-d7 happens to
			// produce. `drupal.org/i/<nid>` is an equally real drupal.org
			// link and matches neither, so a description built from a URL
			// that arrived in that form paired with nothing. The number
			// cannot have that problem.
			Reference: fmt.Sprintf("Closes #%d", issue.Nid),
		}, nil
	}

	if !readFromGitLab(issues.Issues(), moduleName) {
		return publishRoute{}, missingIssueForkError(issue.Nid, moduleName)
	}

	if err := assertPushable(project, project.PathWithNamespace, issue.Nid); err != nil {
		return publishRoute{}, err
	}

	return publishRoute{
		Where:  project.PathWithNamespace,
		Remote: adapter.CanonicalRemote(project.SSHURL),
		Source: project,
		// `Closes #N`, not the issue URL. Two reasons, and both matter: it is
		// GitLab's own closing vocabulary, so GitLab links the merge request
		// to the issue and closes it on merge; and it is what upkeep's *own*
		// pairing reads — drupal.ExtractOwningIssue matches `Closes #N` and a
		// drupal.org URL, and a work-items URL matches neither, so an MR
		// described by its URL would have paired with nothing on the very
		// dashboard that opened it.
		Reference: fmt.Sprintf("Closes #%d", issue.Nid),
	}, nil
}

// readFromGitLab reports whether this project's issues came from GitLab.
//
// Asked of the reader that already answered, after the issue has been read, so
// it is a record of what happened rather than a second guess at it. A reader
// that cannot say is treated as drupal.org, which is the behaviour every
// release so far had.
func readFromGitLab(source IssueSource, machineName string) bool {
	reader, says := source.(QueueReader)
	if !says {
		return false
	}

	queue, decided := reader.QueueFor(machineName)

	return decided && queue == QueueGitLab
}

// assertPushable refuses a destination the branch cannot actually go to.
//
// Asked before the push, not diagnosed after it: discovering either of these
// afterwards would leave a branch on a remote the operator never chose.
//
// Push access first. Unknown is unknown — an unauthenticated read omits the
// permissions entirely — and unknown proceeds, because refusing on an absent
// field would block pushes that work.
//
// Then an SSH URL, which is never assembled: git.drupalcode.org serves the web
// and the API while advertising SSH on git.drupal.org, so a URL built by
// swapping the scheme points at a host that does not answer. Its absence is an
// anomalous shape for either destination — drupal.org advertises one for every
// project and every fork — so both get the same words rather than one of them
// getting advice invented for a case nobody has seen.
func assertPushable(project gitlab.Project, where string, nid int) error {
	if project.CanPush() == gitlab.No {
		return fmt.Errorf(
			"you do not have push access to %s, so the branch cannot go there yet.\n\n%s",
			where, adapter.PushAuthorizationHelp(nid))
	}
	if project.SSHURL == "" {
		return fmt.Errorf(
			"%s reports no SSH URL, so upkeep does not know where to push. "+
				"Report this — it is a shape this tool has not seen.", where)
	}

	return nil
}

// missingIssueForkError is the refusal for a drupal.org project whose issue
// has no fork yet.
func missingIssueForkError(nid int, moduleName string) error {
	return fmt.Errorf(
		"issue #%d has no issue fork yet, and that is where the branch has to go — on "+
			"drupal.org a merge request comes from a fork at issue/<module>-<nid>, never "+
			"from the project itself.\n\n"+
			"  1. Open %s\n"+
			"  2. Click \"Create issue fork\" (under the issue summary)\n"+
			"  3. Re-run: upkeep publish %s %d\n\n"+
			"upkeep does not create it: drupal.org mints the fork *and* links it to the "+
			"issue, and one made straight from the GitLab API would be a repository "+
			"nothing points at.",
		nid, drupal.IssueURL(nid), moduleName, nid)
}

// publishFooter is the line upkeep adds when nobody gave it anything better.
//
// Suppressible, because it is upkeep's words in somebody else's merge request
// and plenty of maintainers would rather it were not there. Nothing reads it:
// the issue reference does the pairing, and this is a courtesy note.
const publishFooter = "Opened with `upkeep publish`."

// publishDescription composes the merge request body.
//
// `<reference>` then a blank line then the body, which is the shape
// drupal.org's own merge requests take. Measured on pathauto's open ones:
// three of six are the bare reference and nothing else — the form drupal.org
// auto-populates — and the rest are that reference followed by the author's
// prose under their own headings, up to three thousand characters of it. So
// the useful thing upkeep can do is put the reference where it belongs and
// get out of the way.
//
// The body comes from --description, or --description-file, or the footer, or
// nothing. Supplying one **replaces the footer** rather than appending to it:
// somebody who wrote a description did not ask for upkeep's signature under
// it, and needing a second flag to say so would be a poor trade.
func publishDescription(cmd *cobra.Command, reference string) (string, error) {
	text := cli.Flag(cmd, "description")
	file := cli.Flag(cmd, "description-file")

	if text != "" && file != "" {
		return "", fmt.Errorf(
			"--description and --description-file both give the body, so only one can be " +
				"used. Pass the text, or the path to a file holding it")
	}

	body := strings.TrimSpace(text)
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("could not read the description from %q: %w", file, err)
		}
		body = strings.TrimSpace(string(raw))
		if body == "" {
			return "", fmt.Errorf(
				"%q is empty, so there is no description in it. Drop the flag to use the "+
					"default, or --no-footer for the issue reference alone", file)
		}
	}

	if body == "" && !cli.Switched(cmd, "no-footer") {
		body = publishFooter
	}
	if body == "" {
		return reference, nil
	}

	return reference + "\n\n" + body, nil
}

// mergeRequestTitle is the drupal.org convention, so the merge request is
// linked to its issue by the same rule this tool parses everywhere else.
func mergeRequestTitle(cmd *cobra.Command, issue drupal.Issue) string {
	title := cli.Flag(cmd, "title")
	if title == "" {
		title = fmt.Sprintf("Issue #%d: %s", issue.Nid, issue.Title)
	}
	if cli.Switched(cmd, "draft") && !strings.HasPrefix(title, "Draft: ") {
		title = "Draft: " + title
	}

	return title
}

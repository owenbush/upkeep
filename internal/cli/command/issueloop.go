package command

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gitlab"
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
	if issue, readable := issues.Issues().Issue(nid); readable {
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

	issue, err := requireIssue(cmd, issues, rawNid)
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
	cmd *cobra.Command, issues IssueClients, rawNid string,
) (drupal.Issue, error) {
	nid, err := issueNid(rawNid)
	if err != nil {
		return drupal.Issue{}, err
	}

	cli.Progressf(cmd, "Resolving issue #%d via drupal.org ...", nid)
	issue, found := issues.Issues().Issue(nid)
	if !found {
		return drupal.Issue{}, fmt.Errorf(
			"drupal.org issue #%d could not be read. Check the node id "+
				"(it is the number in the issue URL)", nid,
		)
	}

	return issue, nil
}

// branchFor is the work branch this run acts on.
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
	cli.AddTargetCore(cmd)
	cli.AddCockpit(cmd)
	cli.AddProjectsRoot(cmd)
	cli.AddVerbose(cmd)
	cmd.Flags().String("branch", "", "Publish this branch instead of deriving it")
	cmd.Flags().String("title", "", "Merge request title (default: from the issue)")
	cmd.Flags().String("target", "",
		"Branch to merge into (default: the base the work branch was started from)")
	cmd.Flags().Bool("draft", false, "Open it as a draft")
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

	issue, err := requireIssue(cmd, issues, rawNid)
	if err != nil {
		return 0, err
	}
	branch := branchFor(cmd, issue)

	project, failure := client.Project(module.Project)
	if failure != nil {
		modules, _ := cli.Modules(where)

		return 0, fmt.Errorf("%s", cockpit.ProjectFailure(modules, module, failure.Message))
	}

	cli.Progressf(cmd, "Issue fork")
	fork, err := requireIssueFork(client, *project, issue.Nid, module.Name)
	if err != nil {
		return 0, err
	}
	cli.Progressf(cmd, "Pushing to %s", fork.PathWithNamespace)

	engine, err := cli.Engine(cmd, engines, where)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Push")
	environment, err := engine.EnsureEnv(module, coreMajor)
	if err != nil {
		return 0, err
	}

	sha, err := engine.PushWork(
		environment, branch, adapter.IssueForkRemote(issue.Nid, fork.SSHURL))
	if err != nil {
		return 0, err
	}
	cli.Progressf(cmd, "Pushed %s at %s.", branch.Name, shortSHA(sha))

	cli.Progressf(cmd, "Merge request")
	existing, failure := client.MergeRequestForBranch(*project, branch.Name, fork)
	if failure != nil {
		return 0, fmt.Errorf(
			"the branch was pushed, but its merge requests could not be listed: %s", failure.Message)
	}
	if existing != nil {
		// Re-running publish after more commits is the normal way to update a
		// merge request: the push above already moved it.
		cli.Printf(cmd, "Updated the open merge request for this branch: !%d\n", existing.IID)
		cli.Println(cmd, existing.WebURL)

		return workflow.OK, nil
	}

	target, err := publishTarget(cmd, engine, environment, *project, module.Name, issue.Nid)
	if err != nil {
		return 0, err
	}

	// Posted to the *fork*, which holds the branch, naming the canonical
	// project as the destination. Backwards-looking until you remember that
	// the branch is the subject of the request.
	created, failure := client.CreateMergeRequest(
		*fork, branch.Name, target,
		mergeRequestTitle(cmd, issue),
		fmt.Sprintf("Fixes %s\n\nOpened with `upkeep publish`.", issue.URL),
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

// requireIssueFork is the issue fork, or a refusal explaining how to make one.
//
// upkeep does not create it. The fork is minted by drupal.org's own issue
// page, which is also what associates it with the issue — one conjured
// straight from the GitLab API would be a repository nothing links to, which
// is harder to clean up than the click was to make.
//
// Checked *before* anything is pushed: discovering it afterwards would leave a
// branch on a remote the operator never chose.
func requireIssueFork(
	client *gitlab.Client, project gitlab.Project, nid int, moduleName string,
) (*gitlab.Project, error) {
	fork, failure := client.IssueFork(project, nid)
	if failure != nil {
		return nil, fmt.Errorf("the issue fork for #%d could not be read: %s", nid, failure.Message)
	}

	if fork == nil {
		return nil, fmt.Errorf(
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

	// Asked before the push, not diagnosed after it. Unknown is unknown — an
	// unauthenticated read omits the permissions entirely — and unknown
	// proceeds, because refusing on an absent field would block pushes that
	// work.
	if fork.CanPush() == gitlab.No {
		return nil, fmt.Errorf(
			"you do not have push access to %s, so the branch cannot go there yet.\n\n%s",
			fork.PathWithNamespace, adapter.PushAuthorizationHelp(nid))
	}

	if fork.SSHURL == "" {
		return nil, fmt.Errorf(
			"the issue fork %s reports no SSH URL, so upkeep does not know where to push. "+
				"Report this — it is a shape this tool has not seen.", fork.PathWithNamespace)
	}

	return fork, nil
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

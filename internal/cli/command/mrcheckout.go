package command

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewMrCheckout builds the mr:checkout command.
//
// The gap between reading a merge request and working on one. `check` and
// `review` exist to answer "is this contribution good", and both put the
// working copy on the managed mr-<iid> branch — force-updated on every apply,
// refused by `publish`, so a commit on it is a commit waiting to be destroyed.
// Taking a merge request *further* needed its source branch, and getting that
// meant knowing three things upkeep already knows and the operator should not
// have to: that the branch lives on an issue fork rather than the project,
// which fork, and what URL GitLab will accept a push to.
//
// So it does what the manual sequence did — resolve the fork, add the remote,
// fetch the branch, check it out — and ends by printing the `publish` that
// sends the work back.
//
// **It does not ask whether the code declares the target core**, which is the
// one thing separating it from `check`. The reason somebody takes over a merge
// request is often that it does *not* support the core yet: a Project Update
// Bot MR that stops short of the info.yml change is the common case, and the
// work is adding it. Refusing to check out the branch because the branch lacks
// what you are about to add would be the tool declining its own purpose.
// `check` asks that question, at the point a verdict is produced, where it
// means something.
func NewMrCheckout(engines adapter.Factory, clients cli.GitlabClients) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mr:checkout <module> <iid>",
		Short: "Check out a merge request's own branch to work on it, with the remote to push back",
		Long: `Puts the module working copy on a merge request's source branch — the branch a
push can go back to — and adds the remote it came from.

  upkeep mr:checkout jumplinks 1
  upkeep mr:checkout jumplinks 1 --version=12

Unlike ` + "`check`" + ` and ` + "`review`" + `, which fetch the merge ref onto a disposable
branch to produce a verdict, this is for changing the contribution: commit here
and ` + "`upkeep publish`" + ` updates the merge request that is already open on it.

It does not require the code to declare the target core. Taking over a merge
request to *add* that support is the usual reason to run this.

Nothing is reset: an existing local branch is resumed exactly as it stands.`,
		Args: cobra.ExactArgs(2),
	}
	cli.AddTargetCore(cmd)
	cli.AddCockpit(cmd)
	cli.AddProjectsRoot(cmd)
	cli.AddVerbose(cmd)
	cli.AddModuleCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runMrCheckout(cmd, engines, clients, args[0], args[1])
	})

	return cmd
}

func runMrCheckout(
	cmd *cobra.Command, engines adapter.Factory, clients cli.GitlabClients,
	moduleName, rawIID string,
) (int, error) {
	where, module, coreMajor, err := resolveSubject(cmd, moduleName)
	if err != nil {
		return 0, err
	}

	iid, err := cli.MrIID(rawIID)
	if err != nil {
		return 0, err
	}

	// Reading is enough to resolve all of this, and a token is not required to
	// look: the fork, the branch and the push URL are all public. The push
	// itself is `publish`'s business and needs an SSH key, not a token.
	client := cli.ReadingClient(cmd, clients)

	project, failure := client.Project(module.Project)
	if failure != nil {
		modules, _ := cli.Modules(where)

		return 0, fmt.Errorf("%s", cockpit.ProjectFailure(modules, module, failure.Message))
	}

	mergeRequest, failure := client.MergeRequest(*project, iid)
	if failure != nil {
		return 0, fmt.Errorf(
			"MR !%d of module %q could not be resolved (not found or inaccessible): %s",
			iid, module.Name, failure.Message,
		)
	}

	// A merged or closed merge request still has a branch, and checking it out
	// is a reasonable thing to want — but its state is the first thing to say,
	// because a push to it will not reopen anything.
	if mergeRequest.State != "opened" {
		cli.Warnf(cmd, "MR !%d is %s. Its branch is still here, but pushing to it will not "+
			"reopen the merge request.", mergeRequest.IID, mergeRequest.State)
	}

	fork, nid, err := mergeRequestSource(client, *project, *mergeRequest)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "MR !%d %q (%s) lives on %s",
		mergeRequest.IID, mergeRequest.Title, mergeRequest.SourceBranch, fork.PathWithNamespace)

	engine, err := cli.Engine(cmd, engines, where)
	if err != nil {
		return 0, err
	}

	environment, err := engine.EnsureEnv(module, coreMajor)
	if err != nil {
		return 0, err
	}

	if err := engine.CheckoutMergeRequestBranch(
		environment,
		adapter.IssueForkRemote(nid, fork.SSHURL),
		adapter.HTTPSURL(fork.PathWithNamespace),
		mergeRequest.SourceBranch,
	); err != nil {
		return 0, err
	}

	reportMrCheckout(cmd, module.Name, coreMajor, environment, mergeRequest.SourceBranch, nid)

	return workflow.OK, nil
}

// mergeRequestSource is the project a merge request's branch lives on, and the
// issue it was forked for.
//
// Resolved through the fork map rather than by looking the source project up
// by id, because the id alone does not say which issue it belongs to — and the
// issue is what names the remote and what `publish` is given. One request for
// every fork of the project, which is also how the dashboard pairs a Project
// Update Bot merge request to its issue.
func mergeRequestSource(
	client *gitlab.Client, project gitlab.Project, mergeRequest gitlab.MergeRequest,
) (gitlab.Project, int, error) {
	// Two conditions, two messages, because one of them would be a lie told in
	// the other's words: a zero source project means GitLab did not say where
	// the branch is, which is not the same as saying it is on the project.
	if mergeRequest.SourceProjectID == 0 {
		return gitlab.Project{}, 0, fmt.Errorf(
			"MR !%d does not say which project its branch %q is on, so upkeep cannot tell where a push "+
				"would go.\nCheck it out with git directly, or read it with `upkeep check %d`",
			mergeRequest.IID, mergeRequest.SourceBranch, mergeRequest.IID,
		)
	}

	if mergeRequest.SourceProjectID == project.ID {
		return gitlab.Project{}, 0, fmt.Errorf(
			"MR !%d's branch %q is on %s itself rather than on an issue fork, and upkeep only knows "+
				"how to push to a fork.\nCheck it out with git directly, or open the work as a new "+
				"merge request from an issue fork with `upkeep start` and `upkeep publish`",
			mergeRequest.IID, mergeRequest.SourceBranch, project.PathWithNamespace,
		)
	}

	nids, failure := client.IssueForkNids(project)
	if failure != nil {
		return gitlab.Project{}, 0, fmt.Errorf(
			"MR !%d's branch is on another project, and this project's issue forks could not be "+
				"listed to say which: %s",
			mergeRequest.IID, failure.Message,
		)
	}

	nid, paired := nids[mergeRequest.SourceProjectID]
	if !paired {
		return gitlab.Project{}, 0, fmt.Errorf(
			"MR !%d's branch %q is on a project that is not an issue fork of %s, so there is no issue "+
				"to publish it against",
			mergeRequest.IID, mergeRequest.SourceBranch, project.PathWithNamespace,
		)
	}

	fork, failure := client.IssueFork(project, nid)
	if failure != nil {
		return gitlab.Project{}, 0, fmt.Errorf(
			"the issue fork for issue %d could not be read: %s", nid, failure.Message)
	}
	if fork == nil {
		return gitlab.Project{}, 0, fmt.Errorf(
			"issue %d has no fork, though MR !%d says its branch is there",
			nid, mergeRequest.IID)
	}

	return *fork, nid, nil
}

// reportMrCheckout says where the work is and how to send it back.
//
// The publish line is printed rather than described, because it carries the
// issue node id and the branch name — two things the operator has no reason to
// know and every reason to get wrong.
func reportMrCheckout(
	cmd *cobra.Command, moduleName, coreMajor string,
	environment adapter.Environment, branch string, nid int,
) {
	cli.Println(cmd, "")
	cli.Printf(cmd, "  Environment  %s\n", environment.ProjectName)
	cli.Printf(cmd, "  Branch       %s\n", branch)
	cli.Printf(cmd, "  Module path  %s\n", environment.ProjectPath+"/module")
	cli.Println(cmd, "")
	cli.Printf(cmd, "  Check it as you work:  upkeep check %s --working-copy --version=%s\n",
		moduleName, coreMajor)
	cli.Printf(cmd, "  Send the work back:    upkeep publish %s %d --branch %s\n",
		moduleName, nid, branch)
	cli.Println(cmd, "")
}

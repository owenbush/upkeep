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

	origin, err := mergeRequestSource(client, *project, *mergeRequest)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "MR !%d %q (%s) lives on %s",
		mergeRequest.IID, mergeRequest.Title, mergeRequest.SourceBranch, origin.Where)

	engine, err := cli.Engine(cmd, engines, where)
	if err != nil {
		return 0, err
	}

	environment, err := engine.EnsureEnv(module, coreMajor)
	if err != nil {
		return 0, err
	}

	if err := engine.CheckoutMergeRequestBranch(
		environment, origin.Remote, origin.FetchURL, mergeRequest.SourceBranch,
	); err != nil {
		return 0, err
	}

	reportMrCheckout(cmd, module.Name, coreMajor, environment, *mergeRequest, origin)

	return workflow.OK, nil
}

// mergeRequestOrigin is where a merge request's branch lives, and what it
// takes to push back to it.
type mergeRequestOrigin struct {
	// Where is the project holding the branch, for the report.
	Where string
	// Remote carries the SSH URL to push to, under a name publish reuses.
	Remote adapter.GitRemote
	// FetchURL is the anonymous HTTPS URL, so taking over a merge request
	// needs no key.
	FetchURL string
	// IssueNid is the issue the fork was made for, or 0 when the branch is on
	// the project itself and no issue fork is involved.
	IssueNid int
}

// mergeRequestSource is where a merge request's branch lives.
//
// **Two shapes, both ordinary.** A branch on an issue fork is what drupal.org's
// issue queue produces: the issue page mints the fork, and a contributor has no
// write access to the project. Measured on pathauto, 100 of 100 open merge
// requests came from a fork. A project whose issues live in GitLab has no such
// step, so a maintainer pushes branches straight to the canonical repository —
// on `helper`, four of five open merge requests are same-project. This refused
// that case outright, which made it useless on exactly those projects;
// generalising from one project was the mistake.
//
// The fork is resolved through the fork map rather than by looking the source
// project up by id, because the id alone does not say which issue it belongs
// to — and the issue is what names the remote and what `publish` is given.
func mergeRequestSource(
	client *gitlab.Client, project gitlab.Project, mergeRequest gitlab.MergeRequest,
) (mergeRequestOrigin, error) {
	// GitLab did not say where the branch is, which is not the same as saying
	// it is on the project — so it is not guessed either way.
	if mergeRequest.SourceProjectID == 0 {
		return mergeRequestOrigin{}, fmt.Errorf(
			"MR !%d does not say which project its branch %q is on, so upkeep cannot tell where a "+
				"push would go.\nCheck it out with git directly, or read it with `upkeep check %d`",
			mergeRequest.IID, mergeRequest.SourceBranch, mergeRequest.IID,
		)
	}

	if mergeRequest.SourceProjectID == project.ID {
		if project.SSHURL == "" {
			return mergeRequestOrigin{}, fmt.Errorf(
				"MR !%d's branch %q is on %s itself, but GitLab advertises no SSH URL for it, so "+
					"there is nowhere to push back to",
				mergeRequest.IID, mergeRequest.SourceBranch, project.PathWithNamespace,
			)
		}

		return mergeRequestOrigin{
			Where:    project.PathWithNamespace + " itself",
			Remote:   adapter.CanonicalRemote(project.SSHURL),
			FetchURL: adapter.HTTPSURL(project.PathWithNamespace),
		}, nil
	}

	nids, failure := client.IssueForkNids(project)
	if failure != nil {
		return mergeRequestOrigin{}, fmt.Errorf(
			"MR !%d's branch is on another project, and this project's issue forks could not be "+
				"listed to say which: %s",
			mergeRequest.IID, failure.Message,
		)
	}

	nid, paired := nids[mergeRequest.SourceProjectID]
	if !paired {
		return mergeRequestOrigin{}, fmt.Errorf(
			"MR !%d's branch %q is on a project that is neither %s nor one of its issue forks, so "+
				"upkeep cannot tell where a push would go",
			mergeRequest.IID, mergeRequest.SourceBranch, project.PathWithNamespace,
		)
	}

	fork, failure := client.IssueFork(project, nid)
	if failure != nil {
		return mergeRequestOrigin{}, fmt.Errorf(
			"the issue fork for issue %d could not be read: %s", nid, failure.Message)
	}
	if fork == nil {
		return mergeRequestOrigin{}, fmt.Errorf(
			"issue %d has no fork, though MR !%d says its branch is there",
			nid, mergeRequest.IID)
	}

	return mergeRequestOrigin{
		Where:    fork.PathWithNamespace,
		Remote:   adapter.IssueForkRemote(nid, fork.SSHURL),
		FetchURL: adapter.HTTPSURL(fork.PathWithNamespace),
		IssueNid: nid,
	}, nil
}

// reportMrCheckout says where the work is and how to send it back.
//
// The line is printed rather than described because it carries values the
// operator has no reason to know and every reason to get wrong: the issue node
// id, the branch name, and the core — an environment is one per module *and*
// core, and publish is the one command that works that out for itself.
//
// Which line depends on where the branch lives. An issue fork has an issue, so
// `publish` can open or update a merge request against it. A branch on the
// project itself has no issue to publish against, and the merge request is
// already open on it — so pushing the branch is the whole job, and naming a
// command that would refuse would be worse than naming git.
func reportMrCheckout(
	cmd *cobra.Command, moduleName, coreMajor string,
	environment adapter.Environment, mergeRequest gitlab.MergeRequest,
	origin mergeRequestOrigin,
) {
	branch := mergeRequest.SourceBranch

	cli.Println(cmd, "")
	cli.Printf(cmd, "  Environment  %s\n", environment.ProjectName)
	cli.Printf(cmd, "  Branch       %s\n", branch)
	cli.Printf(cmd, "  Module path  %s\n", environment.ProjectPath+"/module")
	cli.Println(cmd, "")
	cli.Printf(cmd, "  Check it as you work:  upkeep check %s --working-copy --version=%s\n",
		moduleName, coreMajor)

	if origin.IssueNid != 0 {
		cli.Printf(cmd, "  Send the work back:    upkeep publish %s %d --branch %s --version=%s\n",
			moduleName, origin.IssueNid, branch, coreMajor)
	} else {
		cli.Printf(cmd, "  Send the work back:    git -C %s/module push %s %s\n",
			environment.ProjectPath, origin.Remote.Name, branch)
		cli.Printf(cmd, "                         MR !%d is already open on that branch, so the "+
			"push updates it. There is no issue fork to publish against.\n", mergeRequest.IID)
	}
	cli.Println(cmd, "")
}

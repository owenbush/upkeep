package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/drupalorgcli"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/naming"
	"github.com/owenbush/upkeep/internal/workflow"
)

// The workflow actions drupalorg-cli offers, under upkeep's module-scoped
// conventions: <module> first, then the issue.
//
// `mr:logs` is not here: it is native (mrlogs.go), because drupalorg-cli's
// asks the merge request's target project for the pipeline's jobs, and a
// pipeline for a branch on an issue fork lives in the fork.
//
// Thin on purpose. What upkeep adds is three things the CLI cannot know:
//
//   - **Which project.** The issue is passed as `project/<machine>#<nid>`, never
//     a bare number, so an action cannot land on an unrelated issue of the same
//     number. A migrated project's GitLab iids continue drupal.org's node ids,
//     so a bare number names *something* on both sides.
//   - **Whether anybody meant it.** The commands that post on drupal.org under
//     the maintainer's name — fork, get-access, the assign family, the label
//     family — go through the same gate as `publish`: a terminal, or `--yes`.
//     They are slash commands on a work item, which is a comment a contributor
//     reads. setup-remote and the fork checkout act locally, and are not
//     gated.
//   - **Where.** setup-remote and the fork checkout run git in their working
//     directory, so they run in the module's working tree — the one
//     `env:path` names. An absent environment is refused, not provisioned,
//     for env:path's reason: nobody asked for a ten-minute build.
//
// The CLI's own output is passed through as it printed it.

// postingAction is one of the gated issue commands.
type postingAction struct {
	name  string // the upkeep command and the CLI command, which are the same
	extra string // the arguments after <issue>, for Use
	// minExtra is how many of those the CLI requires.
	minExtra int
	short    string
	// does completes "issue:x <ref> ... under your name": what goes public.
	does string
	// destroys is set where the action replaces rather than adds.
	destroys string
}

var postingActions = []postingAction{
	{name: "issue:fork", short: "Create the issue fork for an issue (posts /do:fork)",
		does: "posts /do:fork on %s"},
	{name: "issue:get-access", short: "Request push access to an issue fork (posts /do:get-access)",
		does: "posts /do:get-access on %s"},
	{name: "issue:assign", extra: " [users...]", short: "Assign users to an issue (default: you)",
		does: "assigns %s"},
	{name: "issue:unassign", extra: " [users...]", short: "Unassign users from an issue (default: you)",
		does: "unassigns %s"},
	{name: "issue:reassign", extra: " <users...>", minExtra: 1,
		short: "Replace an issue's assignees", does: "replaces the assignees of %s",
		destroys: "replaces the issue's current assignees"},
	{name: "issue:label", extra: " <labels...>", minExtra: 1, short: "Add labels to an issue",
		does: "labels %s"},
	{name: "issue:unlabel", extra: " <labels...>", minExtra: 1, short: "Remove labels from an issue",
		does: "removes labels from %s"},
	{name: "issue:relabel", extra: " <labels...>", minExtra: 1, short: "Replace an issue's labels",
		does: "replaces the labels of %s", destroys: "replaces the issue's current labels"},
}

// NewDrupalorgCommands builds every command backed by drupalorg-cli.
func NewDrupalorgCommands(engines adapter.Factory, runner *drupalorgcli.Runner) []*cobra.Command {
	commands := make([]*cobra.Command, 0, len(postingActions)+2)
	for _, action := range postingActions {
		commands = append(commands, newPostingCommand(runner, action))
	}

	return append(commands,
		newWorkingTreeCommand(engines, runner, "issue:setup-remote", "issue:setup-remote", "",
			"Add an issue fork's git remote to the module working copy"),
		newWorkingTreeCommand(engines, runner, "issue:fork-checkout", "issue:checkout", " [branch]",
			"Check out an issue fork's branch in the module working copy"),
	)
}

func newPostingCommand(runner *drupalorgcli.Runner, action postingAction) *cobra.Command {
	cmd := &cobra.Command{
		Use:   action.name + " <module> <issue>" + action.extra,
		Short: action.short,
		Long: action.short + ", through drupalorg-cli.\n\n" +
			"The issue is passed qualified by the module's project, so it cannot land on another " +
			"project's issue of the same number. This posts on drupal.org under your name, so it " +
			"needs a terminal to confirm at, or --yes.",
		Args: cobra.MinimumNArgs(2 + action.minExtra),
	}
	cli.Describe(cmd, cli.Traits{
		Changes:    cli.ChangesRemote,
		Credential: cli.CredentialRequired,
		Destroys:   action.destroys,
	})
	cli.AddCockpit(cmd)
	cli.AddModuleCompletion(cmd)
	addWriteConfirmation(cmd, "posting this under your name")

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		project, err := resolveProjectPath(cmd, args[0])
		if err != nil {
			return 0, err
		}
		nid, err := issueNid(args[1])
		if err != nil {
			return 0, err
		}
		ref := fmt.Sprintf("%s#%d", project, nid)

		if err := assertDeliberateWrite(cmd, action.name+" "+
			fmt.Sprintf(action.does, ref)+" under your name"); err != nil {
			return 0, err
		}

		return runDrupalorg(cmd, runner, "", action.name, append([]string{ref}, args[2:]...))
	})

	return cmd
}

// newWorkingTreeCommand is setup-remote or the fork checkout: a CLI command
// that takes a bare nid and acts on the git repository it runs in.
func newWorkingTreeCommand(
	engines adapter.Factory, runner *drupalorgcli.Runner, name, cliName, extra, short string,
) *cobra.Command {
	maxArgs := 2
	if extra != "" {
		maxArgs = 3
	}
	cmd := &cobra.Command{
		Use:   name + " <module> <issue>" + extra,
		Short: short,
		Long: short + ", through drupalorg-cli's " + cliName + ".\n\n" +
			"Runs inside the module working copy of an existing environment — the directory " +
			"`upkeep env:path` names, plus /module. It does not provision one.",
		Args: cobra.RangeArgs(2, maxArgs),
	}
	cli.Describe(cmd, cli.Traits{
		Changes:    cli.ChangesWorkingCopy,
		Credential: cli.CredentialNone,
	})
	addEnvironmentFlags(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		nid, err := issueNid(args[1])
		if err != nil {
			return 0, err
		}
		environment, err := locateEnvironment(cmd, engines, args[0])
		if err != nil {
			return 0, err
		}

		return runDrupalorg(cmd, runner, filepath.Join(environment, "module"), cliName,
			append([]string{strconv.Itoa(nid)}, args[2:]...))
	})

	return cmd
}

// runDrupalorg runs one CLI command and passes its output on.
//
// Text, not --format=json: the output is for the person who ran the command,
// and the CLI's text is what it wrote for them. The arguments go after "--",
// so a label or user name starting with a dash cannot be read as an option.
// dir is "" for the commands that do not care where they run.
func runDrupalorg(
	cmd *cobra.Command, runner *drupalorgcli.Runner, dir, name string, args []string,
) (int, error) {
	argv := append([]string{name, "--no-interaction", "--"}, args...)

	var (
		out     []byte
		failure *gitlab.Failure
	)
	if dir == "" {
		out, failure = runner.RunRaw(cmd.Context(), argv)
	} else {
		out, failure = runner.RunRawIn(cmd.Context(), dir, argv)
	}
	if failure != nil {
		return 0, errors.New(failure.Message)
	}

	cli.Printf(cmd, "%s", out)

	return workflow.OK, nil
}

// resolveProjectPath is the drupal.org project a module name means, for the
// commands that need only that — no core, no environment.
//
// The registry wins where there is one, as everywhere. Where there is no
// registry at all and none was named, drupal.org's `project/<name>`
// convention answers, as it does for an unregistered module: these commands
// act on drupal.org, and refusing `upkeep mr:logs content_sync 22` for want
// of a cockpit would be a restriction nothing requires. A cockpit somebody
// named that has no registry is still an error, never guessed past.
func resolveProjectPath(cmd *cobra.Command, name string) (string, error) {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return "", err
	}

	named := cmd.Flags().Changed(cli.FlagCockpit) || os.Getenv(cockpit.EnvVar) != ""
	if _, statErr := os.Stat(where.RegistryPath()); named || statErr == nil {
		modules, err := cli.Modules(where)
		if err != nil {
			return "", err
		}
		if module, registered := modules[name]; registered {
			return module.Project, nil
		}
	}

	if !naming.IsModuleName(name) {
		return "", fmt.Errorf(
			"not a Drupal module machine name: %q. Expected lower-case letters, digits and "+
				"underscores, starting with a letter — e.g. \"pathauto\"", name)
	}

	return cockpit.ProjectFor(name), nil
}

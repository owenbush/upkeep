package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// drupalorgAction is one wrapped command. This table is the single source
// for both the cobra command and the contract `livecheck --cli-contract`
// holds drupal.org-cli to (DrupalorgSignatures), so the arguments upkeep
// passes and the arguments it checks the CLI takes cannot drift apart.
type drupalorgAction struct {
	name    string // the upkeep command
	cliName string // the CLI command, where it differs from name
	// args is the CLI's positional signature as upkeep fills it: args[0] is
	// always the issue (from <issue>, so upkeep always passes it, whatever
	// the CLI's own shape for it), and the rest are taken as given after it.
	args  []drupalorgcli.Arg
	short string
	// posts is set for the actions that post on drupal.org under your name,
	// and are gated. The others run git in the module working copy.
	posts bool
	// does completes "issue:x <ref> ... under your name": what goes public.
	does string
	// destroys is set where the action replaces rather than adds.
	destroys string
}

// noInteraction is passed on every run, and checked for.
const noInteraction = "no-interaction"

var (
	// The posting commands take `project#nid`, a required ref.
	argRef = drupalorgcli.Arg{Name: "ref", Shape: drupalorgcli.Required}
	// The local ones take a bare nid, which the CLI would otherwise infer
	// from the branch; upkeep always passes it.
	argNid = drupalorgcli.Arg{Name: "nid", Shape: drupalorgcli.Optional}

	argUsers = drupalorgcli.Arg{Name: "users", Shape: drupalorgcli.Variadic}
	// argSomeUsers is argUsers made required: replacing assignees with nobody is
	// not a request.
	argSomeUsers = drupalorgcli.Arg{Name: "users", Shape: drupalorgcli.RequiredVariadic}
	argLabels    = drupalorgcli.Arg{Name: "labels", Shape: drupalorgcli.RequiredVariadic}
)

var drupalorgActions = []drupalorgAction{
	{name: "issue:fork", args: []drupalorgcli.Arg{argRef}, posts: true,
		short: "Create the issue fork for an issue (posts /do:fork)", does: "posts /do:fork on %s"},
	{name: "issue:get-access", args: []drupalorgcli.Arg{argRef}, posts: true,
		short: "Request push access to an issue fork (posts /do:get-access)",
		does:  "posts /do:get-access on %s"},
	{name: "issue:assign", args: []drupalorgcli.Arg{argRef, argUsers}, posts: true,
		short: "Assign users to an issue (default: you)", does: "assigns %s"},
	{name: "issue:unassign", args: []drupalorgcli.Arg{argRef, argUsers}, posts: true,
		short: "Unassign users from an issue (default: you)", does: "unassigns %s"},
	{name: "issue:reassign", args: []drupalorgcli.Arg{argRef, argSomeUsers}, posts: true,
		short: "Replace an issue's assignees", does: "replaces the assignees of %s",
		destroys: "replaces the issue's current assignees"},
	{name: "issue:label", args: []drupalorgcli.Arg{argRef, argLabels}, posts: true,
		short: "Add labels to an issue", does: "labels %s"},
	{name: "issue:unlabel", args: []drupalorgcli.Arg{argRef, argLabels}, posts: true,
		short: "Remove labels from an issue", does: "removes labels from %s"},
	{name: "issue:relabel", args: []drupalorgcli.Arg{argRef, argLabels}, posts: true,
		short: "Replace an issue's labels", does: "replaces the labels of %s",
		destroys: "replaces the issue's current labels"},
	{name: "issue:setup-remote", args: []drupalorgcli.Arg{argNid},
		short: "Add an issue fork's git remote to the module working copy"},
	{name: "issue:fork-checkout", cliName: "issue:checkout",
		args:  []drupalorgcli.Arg{argNid, {Name: "branch", Shape: drupalorgcli.Optional}},
		short: "Check out an issue fork's branch in the module working copy"},
}

func (a drupalorgAction) cli() string {
	if a.cliName != "" {
		return a.cliName
	}

	return a.name
}

// DrupalorgSignatures is what upkeep relies on each wrapped drupalorg-cli
// command accepting, for the live contract check.
func DrupalorgSignatures() []drupalorgcli.Signature {
	signatures := make([]drupalorgcli.Signature, 0, len(drupalorgActions))
	for _, action := range drupalorgActions {
		signatures = append(signatures, drupalorgcli.Signature{
			Command: action.cli(),
			Args:    slices.Clone(action.args),
			Options: []string{noInteraction},
		})
	}

	return signatures
}

// usage and positional are the upkeep side of the table: <module> <issue>,
// then the arguments after the issue, as cobra shows and counts them.
func (a drupalorgAction) usage() string {
	use := a.name + " <module> <issue>"
	for _, arg := range a.args[1:] {
		switch arg.Shape {
		case drupalorgcli.Required:
			use += " <" + arg.Name + ">"
		case drupalorgcli.Optional:
			use += " [" + arg.Name + "]"
		case drupalorgcli.Variadic:
			use += " [" + arg.Name + "...]"
		case drupalorgcli.RequiredVariadic:
			use += " <" + arg.Name + "...>"
		}
	}

	return use
}

func (a drupalorgAction) positional() cobra.PositionalArgs {
	least := 2
	for _, arg := range a.args[1:] {
		if arg.Shape == drupalorgcli.Required || arg.Shape == drupalorgcli.RequiredVariadic {
			least++
		}
	}
	if last := a.args[len(a.args)-1].Shape; len(a.args) > 1 &&
		(last == drupalorgcli.Variadic || last == drupalorgcli.RequiredVariadic) {
		return cobra.MinimumNArgs(least)
	}

	return cobra.RangeArgs(least, 1+len(a.args))
}

// NewDrupalorgCommands builds every command backed by drupalorg-cli.
func NewDrupalorgCommands(engines adapter.Factory, runner *drupalorgcli.Runner) []*cobra.Command {
	commands := make([]*cobra.Command, 0, len(drupalorgActions))
	for _, action := range drupalorgActions {
		if action.posts {
			commands = append(commands, newPostingCommand(runner, action))
		} else {
			commands = append(commands, newWorkingTreeCommand(engines, runner, action))
		}
	}

	return commands
}

func newPostingCommand(runner *drupalorgcli.Runner, action drupalorgAction) *cobra.Command {
	cmd := &cobra.Command{
		Use:   action.usage(),
		Short: action.short,
		Long: action.short + ", through drupalorg-cli.\n\n" +
			"The issue is passed qualified by the module's project, so it cannot land on another " +
			"project's issue of the same number. This posts on drupal.org under your name, so it " +
			"needs a terminal to confirm at, or --yes.",
		Args: action.positional(),
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

		return runDrupalorg(cmd, runner, "", action.cli(), append([]string{ref}, args[2:]...))
	})

	return cmd
}

// newWorkingTreeCommand is setup-remote or the fork checkout: a CLI command
// that takes a bare nid and acts on the git repository it runs in.
func newWorkingTreeCommand(
	engines adapter.Factory, runner *drupalorgcli.Runner, action drupalorgAction,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   action.usage(),
		Short: action.short,
		Long: action.short + ", through drupalorg-cli's " + action.cli() + ".\n\n" +
			"Runs inside the module working copy of an existing environment — the directory " +
			"`upkeep env:path` names, plus /module. It does not provision one.",
		Args: action.positional(),
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

		return runDrupalorg(cmd, runner, filepath.Join(environment, "module"), action.cli(),
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
	argv := append([]string{name, "--" + noInteraction, "--"}, args...)

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

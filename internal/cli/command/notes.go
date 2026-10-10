package command

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/config"
	"github.com/owenbush/upkeep/internal/notes"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewNotes builds the notes command.
//
// Drafts release notes for a module: what merged since its last tag, as
// paste-ready Markdown on stdout.
//
// Strictly read-only, and deliberately so: tagging and cutting a release stay
// manual. There are no tag or release flags here and there will not be — this
// drafts the text a human reads, edits and publishes.
func NewNotes(clients cli.GitlabClients) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notes <module>",
		Short: "Draft paste-ready Markdown release notes: merged MRs since the module's last tag",
		Long: `Drafts release notes for a module — everything merged since its last tag,
as Markdown on stdout ready to paste into a release.

  upkeep notes pathauto
  upkeep notes project/conditions_helper > notes.md

Drafts; it never tags and never cuts a release. The module is resolved
through the cockpit registry when there is one, and taken as a project path
when there is not.`,
		Args: cobra.ExactArgs(1),
	}
	cli.AddCockpit(cmd)
	cli.AddModuleCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runNotes(cmd, clients, args[0])
	})

	return cmd
}

func runNotes(cmd *cobra.Command, clients cli.GitlabClients, moduleName string) (int, error) {
	projectPath := notesProjectPath(cmd, moduleName)

	// Read-only, so no credential is required: git.drupalcode.org serves a
	// public project's tags and merged merge requests anonymously.
	client := cli.ReadingClient(cmd, clients)

	project, failure := client.Project(projectPath)
	if failure != nil {
		return 0, fmt.Errorf("project lookup failed [%s]: %s", failure.ShortCode(), failure.Message)
	}

	tags, failure := client.Tags(*project)
	if failure != nil {
		return 0, fmt.Errorf("tag list failed [%s]: %s", failure.ShortCode(), failure.Message)
	}

	// A tagless module — or one whose only tags GitLab reports without a
	// creation date — has no boundary, so the draft covers the whole merged
	// history. An undated tag is skipped rather than ordered last: it cannot
	// serve as a "since" boundary, and taking it would produce a draft
	// covering a range nobody asked for.
	latest, tagged := notes.LatestTag(tags)
	// UTC, not the machine's zone: this is a boundary sent to GitLab, and an
	// epoch rendered in local time asks about a different instant west of
	// Greenwich.
	since := time.Unix(0, 0).UTC()
	if tagged {
		since = latest.CreatedAt
	}

	merged, failure := client.MergedSince(*project, since)
	if failure != nil {
		return 0, fmt.Errorf("merged-MR list failed [%s]: %s", failure.ShortCode(), failure.Message)
	}

	// The module as it was asked for, not the project path: the heading names
	// the thing being released, and "project/pathauto" is where it lives.
	draft := notes.NewGenerator(config.BotPatternForCore("")).
		Generate(moduleName, latest, tagged, merged)

	cli.Println(cmd, draft)

	return workflow.OK, nil
}

// notesProjectPath is the GitLab project to draft from.
//
// The registry's path wins where there is one; otherwise the argument *is* the
// project path. This is the one place a missing registry is not an error —
// `upkeep notes project/conditions_helper` is a supported way to run this from
// anywhere, so a cockpit that cannot be read means the argument stands as
// given rather than that the command cannot proceed.
func notesProjectPath(cmd *cobra.Command, moduleName string) string {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return moduleName
	}
	// Checked rather than relying on an unreadable registry yielding an empty
	// map: the two are the same answer here today, and they are the same
	// answer by coincidence rather than by contract.
	modules, err := cli.Modules(where)
	if err != nil {
		return moduleName
	}
	if module, registered := modules[moduleName]; registered {
		return module.Project
	}

	return moduleName
}

package command

import (
	"strings"

	"github.com/spf13/cobra"

	upkeep "github.com/owenbush/upkeep"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewSkill prints the agent skill.
//
// Two ways to install it, from one file:
//
//	npx skills add owenbush/upkeep                   # the discoverable route
//	upkeep skill > ~/.claude/skills/upkeep/SKILL.md  # matches this binary
//
// The second exists because the first takes whatever is on the repository's
// default branch, which for somebody on a release is a newer tool than they
// are running. A skill describing commands the binary does not have is worse
// than no skill: an agent follows it and gets a refusal it cannot interpret.
//
// stdout carries the document and nothing else, like `env:path`, so the
// redirect above works. Anything upkeep wants to say goes to stderr.
func NewSkill() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Print the agent skill, for installing into an agent that drives upkeep",
		Long: `Prints upkeep's agent skill: what an AI agent needs to know to use this tool
without doing something it should not.

  mkdir -p ~/.claude/skills/upkeep && upkeep skill > ~/.claude/skills/upkeep/SKILL.md

Or install it from the repository, which is the discoverable route:

  npx skills add owenbush/upkeep

The version printed here is the one this binary was built with, so it cannot
describe commands you do not have.

stdout carries the document alone, so the redirect above works.`,
		Args: cobra.NoArgs,
	}
	cli.Describe(cmd, cli.Traits{
		Changes:    cli.ChangesNothing,
		Credential: cli.CredentialNone,
	})

	cmd.RunE = cli.Run(func(cmd *cobra.Command, _ []string) (int, error) {
		cli.Println(cmd, strings.TrimRight(upkeep.Skill, "\n"))

		return workflow.OK, nil
	})

	return cmd
}

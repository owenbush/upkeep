package command

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewAPIProbe builds the api:probe command.
//
// A debug command: what git.drupalcode.org actually says about one module.
// When a dashboard row looks wrong, the question is always whether upkeep read
// the API wrongly or the API said something surprising, and this answers the
// second half without a browser.
//
// Takes the module name or a full project path directly and consults no
// registry: probing a project is a question about GitLab, not about what this
// cockpit watches.
//
// Read-only, and only ever GETs.
func NewAPIProbe(clients cli.GitlabClients) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api:probe <module>",
		Short: "Probe the git.drupalcode.org API for a module: open MRs and head pipeline status",
		Long: `What GitLab actually says about a module — for when a row looks wrong and
the question is whether upkeep misread the API or the API said something
surprising.

  upkeep api:probe pathauto
  upkeep api:probe project/conditions_helper

Takes the name or the full path, consults no registry, and only ever reads.`,
		Args: cobra.ExactArgs(1),
	}
	cli.AddModuleCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runAPIProbe(cmd, clients, args[0])
	})

	return cmd
}

func runAPIProbe(cmd *cobra.Command, clients cli.GitlabClients, moduleName string) (int, error) {
	client := cli.ReadingClient(cmd, clients)

	project, failure := client.Project(moduleName)
	if failure != nil {
		return 0, fmt.Errorf("project lookup failed [%s]: %s", failure.ShortCode(), failure.Message)
	}
	cli.Printf(cmd, "%s (project id %d)\n\n", project.PathWithNamespace, project.ID)

	open, failure := client.OpenMergeRequests(*project)
	if failure != nil {
		return 0, fmt.Errorf("MR list failed [%s]: %s", failure.ShortCode(), failure.Message)
	}
	cli.Printf(cmd, "Open merge requests: %d\n", len(open))

	if len(open) == 0 {
		cli.Println(cmd, "No open MR to inspect further.")

		return workflow.OK, nil
	}

	// Re-fetched singly, because only that endpoint carries head_pipeline.
	// A failure here degrades rather than refuses: the listed payload still
	// answers everything else, and the pipeline is the one field lost.
	first := open[0]
	detailed, detailFailure := client.MergeRequest(*project, first.IID)
	mergeRequest := first
	if detailFailure == nil {
		mergeRequest = *detailed
	}

	describeProbedMR(cmd, mergeRequest)

	if detailFailure != nil {
		cli.Warnf(cmd, "Single-MR fetch failed [%s]: %s (pipeline status unavailable)",
			detailFailure.ShortCode(), detailFailure.Message)

		return workflow.OK, nil
	}

	if mergeRequest.HeadPipeline == nil {
		cli.Println(cmd, "Head pipeline: none")

		return workflow.OK, nil
	}
	cli.Printf(cmd, "Head pipeline: %s (#%d) %s\n",
		mergeRequest.HeadPipeline.Status, mergeRequest.HeadPipeline.ID,
		mergeRequest.HeadPipeline.WebURL)

	return workflow.OK, nil
}

// describeProbedMR prints the fields a surprising row is usually explained by.
//
// Every one of them is a field some part of this tool decides on: the draft
// flags the gate reads, the head SHA evidence is keyed against, the source
// branch a bot merge request is recognised by. Printed raw and unjudged —
// there is no verdict here, which is the point.
func describeProbedMR(cmd *cobra.Command, mergeRequest gitlab.MergeRequest) {
	author := mergeRequest.AuthorUsername
	if mergeRequest.AuthorID != 0 {
		author = fmt.Sprintf("%s (id %d)", author, mergeRequest.AuthorID)
	}

	cli.Printf(cmd, "\nMR !%d\n", mergeRequest.IID)
	for _, field := range [][2]string{
		{"IID", fmt.Sprint(mergeRequest.IID)},
		{"Title", mergeRequest.Title},
		{"Author", author},
		{"Source branch", mergeRequest.SourceBranch},
		{"Draft", yesNo(mergeRequest.IsDraft())},
		{"Detailed merge status", orNotAvailable(mergeRequest.DetailedMergeStatus)},
		{"Head SHA", orNotAvailable(mergeRequest.HeadSHA)},
		{"URL", mergeRequest.WebURL},
	} {
		cli.Printf(cmd, "  %-22s %s\n", field[0]+":", field[1])
	}
	cli.Println(cmd, "")
}

func yesNo(yes bool) string {
	if yes {
		return "yes"
	}

	return "no"
}

// orNotAvailable distinguishes a field GitLab did not send from one it sent
// empty — which for a head SHA is the difference between "no commits" and "the
// payload did not say", and those are answers to different questions.
func orNotAvailable(value string) string {
	if value == "" {
		return "n/a"
	}

	return value
}

package command

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// traceTailLines is how much of each failed job's log is printed: the end,
// which is where a job says why it failed. drupalorg-cli prints the same.
const traceTailLines = 100

// NewMrLogs builds the mr:logs command.
//
// **Native, not drupalorg-cli's.** The CLI's mr:logs asks the merge request's
// *target* project for the pipeline's jobs, and on drupal.org nearly every
// merge request comes from an issue fork, where the pipeline actually ran —
// so it answers 404 for the ordinary case (content_sync !22: pipeline 1013844
// is in issue/content_sync-3632781, project 250962, and project/content_sync
// does not know it). GitLab says where the pipeline ran in its project_id, and
// that is where this reads the jobs and traces. Anonymous, like every read.
//
// Jobs allowed to fail are included and labelled: drupal.org's phpcs and
// phpstan jobs are allowed to fail by default, so a pipeline that "passed with
// warnings" has failed jobs a maintainer wants to read.
func NewMrLogs(clients cli.GitlabClients) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mr:logs <module> <mr>",
		Short: "Print the logs of a merge request's failed CI jobs",
		Long: `Prints the end of each failed job's log from a merge request's latest pipeline,
including jobs that are allowed to fail.

  upkeep mr:logs content_sync 22

Reads the pipeline where it ran — the issue fork, for a merge request from
one — and needs no token.`,
		Args: cobra.ExactArgs(2),
	}
	cli.Describe(cmd, cli.Traits{
		Changes:    cli.ChangesNothing,
		Credential: cli.CredentialNone,
	})
	cli.AddCockpit(cmd)
	cli.AddModuleCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runMrLogs(cmd, clients, args[0], args[1])
	})

	return cmd
}

func runMrLogs(cmd *cobra.Command, clients cli.GitlabClients, moduleName, rawIID string) (int, error) {
	path, err := resolveProjectPath(cmd, moduleName)
	if err != nil {
		return 0, err
	}
	iid, err := cli.MrIID(rawIID)
	if err != nil {
		return 0, err
	}

	client := cli.ReadingClient(cmd, clients)

	project, failure := client.Project(path)
	if failure != nil {
		return 0, fmt.Errorf("%s could not be read: %s", path, failure.Message)
	}
	mergeRequest, failure := client.MergeRequest(*project, iid)
	if failure != nil {
		return 0, fmt.Errorf("MR !%d of %s could not be read: %s", iid, path, failure.Message)
	}

	pipeline := mergeRequest.HeadPipeline
	if pipeline == nil {
		cli.Printf(cmd, "MR !%d has no pipeline.\n", iid)

		return workflow.OK, nil
	}
	pipeline = pipelineLocation(*pipeline, *mergeRequest, *project)

	jobs, failure := client.PipelineJobs(*pipeline)
	if failure != nil {
		return 0, fmt.Errorf("the jobs of pipeline #%d could not be read: %s", pipeline.ID, failure.Message)
	}

	failed := []gitlab.Job{}
	for _, job := range jobs {
		if job.Failed() {
			failed = append(failed, job)
		}
	}

	cli.Printf(cmd, "MR !%d, pipeline #%d (%s): %s\n", iid, pipeline.ID, pipeline.RawStatus, pipeline.WebURL)
	if len(failed) == 0 {
		cli.Printf(cmd, "Pipeline #%d: no failed jobs.\n", pipeline.ID)

		return workflow.OK, nil
	}

	for _, job := range failed {
		label := "failed"
		if job.AllowFailure {
			label = "failed, allowed to fail"
		}
		cli.Printf(cmd, "\n=== %s (%s) — %s — %s\n", job.Name, job.Stage, label, job.WebURL)

		trace, failure := client.JobTrace(pipeline.ProjectID, job)
		if failure != nil {
			cli.Printf(cmd, "(trace unavailable: %s)\n", failure.Message)

			continue
		}
		for _, line := range traceTail(trace, traceTailLines) {
			cli.Println(cmd, line)
		}
	}

	return workflow.OK, nil
}

// pipelineLocation fills in where the pipeline ran when GitLab did not say:
// the project the branch is on, else the project itself.
func pipelineLocation(
	pipeline gitlab.Pipeline, mergeRequest gitlab.MergeRequest, project gitlab.Project,
) *gitlab.Pipeline {
	if pipeline.ProjectID == 0 {
		pipeline.ProjectID = mergeRequest.SourceProjectID
	}
	if pipeline.ProjectID == 0 {
		pipeline.ProjectID = project.ID
	}

	return &pipeline
}

// traceTail is the last n lines of a job trace as a reader sees them.
//
// A runner's log is written for a terminal: section markers and progress are
// redrawn after a carriage return, and lines carry colours. So each line is
// what a terminal would show — the text after its last carriage return —
// with the escape codes removed, and trailing blank lines are dropped so the
// tail ends on what the job last said.
func traceTail(trace string, n int) []string {
	lines := strings.Split(trace, "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if cut := strings.LastIndex(line, "\r"); cut >= 0 {
			line = line[cut+1:]
		}
		lines[i] = strings.TrimRight(cli.PlainText(line), " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	return lines
}

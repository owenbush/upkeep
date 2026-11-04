package gitlab

import (
	"fmt"
	"net/http"
)

// Job is one CI job of a pipeline.
type Job struct {
	ID     int
	Name   string
	Stage  string
	Status string
	// AllowFailure is GitLab's allow_failure: a job that may fail without
	// failing the pipeline. drupal.org's phpcs and phpstan jobs are, by
	// default, which is how a pipeline "passed with warnings" has failed jobs.
	AllowFailure bool
	WebURL       string
}

// Failed reports whether the job failed, allowed to or not.
func (j Job) Failed() bool { return j.Status == "failed" }

func jobFrom(p payload) Job {
	return Job{
		ID:           p.intOr("id", 0),
		Name:         p.str("name", ""),
		Stage:        p.str("stage", ""),
		Status:       p.str("status", ""),
		AllowFailure: p.boolOr("allow_failure", false),
		WebURL:       p.str("web_url", ""),
	}
}

// PipelineJobs lists a pipeline's jobs (the latest attempt of each).
//
// Addressed by the project the pipeline ran in — Pipeline.ProjectID — which for
// a merge request from an issue fork is the fork. The target project answers
// 404 for the same pipeline id, which is the bug in drupalorg-cli's mr:logs
// that this exists to avoid.
func (c *Client) PipelineJobs(pipeline Pipeline) ([]Job, *Failure) {
	projectID, pipelineID, browserURL := pipeline.ProjectID, pipeline.ID, pipeline.WebURL
	jobs := []Job{}

	for page := 1; page <= MaxPages; page++ {
		rows, failure := c.rows(
			fmt.Sprintf("%s/projects/%d/pipelines/%d/jobs?per_page=100&page=%d",
				c.apiBase, projectID, pipelineID, page),
			browserURL, "jobs",
		)
		if failure != nil {
			return nil, failure
		}
		for _, row := range rows {
			jobs = append(jobs, jobFrom(payload{data: row}))
		}
		if len(rows) < 100 {
			break
		}
	}

	return jobs, nil
}

// JobTrace is a job's log, as GitLab serves it: plain text, with the runner's
// ANSI colours and section markers left in for the caller to strip.
//
// Not memoized — a running job's trace grows — and not decoded, since it is
// not JSON. Failures are the same typed conditions as every other read.
func (c *Client) JobTrace(projectID int, job Job) (string, *Failure) {
	requestURL := fmt.Sprintf("%s/projects/%d/jobs/%d/trace", c.apiBase, projectID, job.ID)
	browserURL := job.WebURL

	response, raw, err := c.send(http.MethodGet, requestURL, nil)
	if err != nil {
		return "", transportError("HTTP transport failure: "+err.Error(), browserURL)
	}
	if response.StatusCode >= 400 {
		return "", failureFor(response.StatusCode, errorDetail(raw), browserURL, 0)
	}

	return string(raw), nil
}
